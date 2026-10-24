#!/usr/bin/env bash
# Run the certificate issuance integration test against a local Pebble CA.
#
# Pebble is configured with PEBBLE_VA_ALWAYS_VALID=1, so the DNS-01 challenge
# is treated as valid after the manual challenge is confirmed. This exercises
# account registration, ordering, CSR finalisation, certificate storage and
# the push path without touching a real DNS provider.
#
# By default Pebble runs in Docker. Set PEBBLE_BIN to a local Pebble binary to
# run it directly (useful on hosts that cannot pull images).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CONTAINER="${PEBBLE_CONTAINER:-mikvace-pebble-e2e}"
PORT="${PEBBLE_PORT:-14000}"
MANAGEMENT_PORT="${PEBBLE_MANAGEMENT_PORT:-15000}"
# Pin Pebble: v2.10+ omits the Location header on the finalize response that
# x/crypto/acme relies on. v2.5.2 is the version verified by this repository.
IMAGE="${PEBBLE_IMAGE:-ghcr.io/letsencrypt/pebble:2.5.2}"
BIN="${PEBBLE_BIN:-}"
WORK="$(mktemp -d "${TMPDIR:-/tmp}/mikvace-pebble.XXXXXX")"
CERTDIR="$WORK/certs"
PEBBLE_PID=""
mkdir -p "$CERTDIR"

cleanup() {
  if [ -n "$PEBBLE_PID" ]; then
    kill "$PEBBLE_PID" >/dev/null 2>&1 || true
    wait "$PEBBLE_PID" >/dev/null 2>&1 || true
  fi
  if [ -z "$BIN" ]; then
    docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  fi
  rm -rf "$WORK"
}
trap cleanup EXIT

for cmd in curl openssl go; do
  command -v "$cmd" >/dev/null 2>&1 || {
    echo "cert-e2e: missing required command: $cmd" >&2
    exit 1
  }
done
if [ -n "$BIN" ]; then
  [ -x "$BIN" ] || {
    echo "cert-e2e: PEBBLE_BIN is not executable: $BIN" >&2
    exit 1
  }
elif ! command -v docker >/dev/null 2>&1; then
  echo "cert-e2e: docker is required, or set PEBBLE_BIN=/path/to/pebble" >&2
  exit 1
fi

echo "==> generating a throwaway CA for Pebble"
openssl req -x509 -newkey rsa:2048 -nodes -days 2 \
  -keyout "$CERTDIR/ca.key" -out "$CERTDIR/ca.pem" \
  -subj "/CN=MikvAce Pebble E2E CA" \
  -addext "basicConstraints=critical,CA:TRUE" >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes \
  -keyout "$CERTDIR/key.pem" -out "$CERTDIR/server.csr" \
  -subj "/CN=localhost" >/dev/null 2>&1
cat >"$CERTDIR/ext.cnf" <<'EOF'
subjectAltName=DNS:localhost,IP:127.0.0.1
extendedKeyUsage=serverAuth
EOF
openssl x509 -req -in "$CERTDIR/server.csr" -CA "$CERTDIR/ca.pem" -CAkey "$CERTDIR/ca.key" \
  -CAcreateserial -out "$CERTDIR/cert.pem" -days 2 -extfile "$CERTDIR/ext.cnf" >/dev/null 2>&1
chmod 755 "$CERTDIR"
chmod 644 "$CERTDIR/ca.pem" "$CERTDIR/cert.pem" "$CERTDIR/key.pem"

if [ -n "$BIN" ]; then
  CERT_FILE="$CERTDIR/cert.pem"
  KEY_FILE="$CERTDIR/key.pem"
  LISTEN="0.0.0.0:${PORT}"
  MANAGEMENT_LISTEN="0.0.0.0:${MANAGEMENT_PORT}"
else
  CERT_FILE="/certs/cert.pem"
  KEY_FILE="/certs/key.pem"
  LISTEN="0.0.0.0:14000"
  MANAGEMENT_LISTEN="0.0.0.0:15000"
fi
cat >"$WORK/pebble-config.json" <<EOF
{
  "pebble": {
    "listenAddress": "${LISTEN}",
    "managementListenAddress": "${MANAGEMENT_LISTEN}",
    "certificate": "${CERT_FILE}",
    "privateKey": "${KEY_FILE}",
    "httpPort": 5002,
    "tlsPort": 5001,
    "ocspResponderURL": "",
    "externalAccountBindingRequired": false
  }
}
EOF
chmod 644 "$WORK/pebble-config.json"

if [ -n "$BIN" ]; then
  echo "==> starting Pebble from $BIN"
  PEBBLE_VA_ALWAYS_VALID=1 PEBBLE_VA_NOSLEEP=1 PEBBLE_WFE_NONCEREJECT=0 \
    "$BIN" -config "$WORK/pebble-config.json" >"$WORK/pebble.log" 2>&1 &
  PEBBLE_PID=$!
else
  echo "==> starting Pebble ($IMAGE)"
  docker rm -f "$CONTAINER" >/dev/null 2>&1 || true
  docker run -d --name "$CONTAINER" \
    -p "${PORT}:14000" \
    -e PEBBLE_VA_ALWAYS_VALID=1 \
    -e PEBBLE_VA_NOSLEEP=1 \
    -e PEBBLE_WFE_NONCEREJECT=0 \
    -v "$CERTDIR:/certs:ro" \
    -v "$WORK/pebble-config.json:/pebble-config.json:ro" \
    "$IMAGE" -config /pebble-config.json >/dev/null
fi

echo "==> waiting for the ACME directory"
ready=0
for _ in $(seq 1 60); do
  if curl --cacert "$CERTDIR/ca.pem" -fsS "https://localhost:${PORT}/dir" >/dev/null 2>&1; then
    ready=1
    break
  fi
  sleep 1
done
if [ "$ready" != "1" ]; then
  echo "cert-e2e: Pebble did not become ready" >&2
  if [ -n "$BIN" ]; then
    cat "$WORK/pebble.log" >&2 || true
  else
    docker logs "$CONTAINER" >&2 || true
  fi
  exit 1
fi

echo "==> running the Go integration test"
cd "$ROOT"
ACME_E2E=1 \
ACME_E2E_DIRECTORY="https://localhost:${PORT}/dir" \
SSL_CERT_FILE="$CERTDIR/ca.pem" \
go test ./internal/certmanager -run TestPebbleIssuance -count=1 -v
