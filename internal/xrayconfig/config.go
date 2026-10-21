// Package xrayconfig renders Xray-core server configuration for VLESS
// inbounds (REALITY, TLS or plain text) and embeds the panel user list.
package xrayconfig

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

const (
	CertPath   = "/data/certs/cert.pem"
	KeyPath    = "/data/certs/key.pem"
	AccessLog  = "/data/log/access.log"
	APIPort    = 61000
	InboundTag = "vless-in"
)

// GenerateRealityKeypair creates an X25519 keypair in the encoding expected by
// Xray REALITY (raw URL-safe base64).
func GenerateRealityKeypair() (privateKey, publicKey string, err error) {
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(key.Bytes()), enc.EncodeToString(key.PublicKey().Bytes()), nil
}

// GenerateShortID creates a random REALITY short id (8 hex chars).
func GenerateShortID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Normalize fills defaults for an Xray node. It also guarantees that REALITY
// key material and at least one short id exist.
func Normalize(n *model.Node) error {
	x := &n.Xray
	x.Transport = strings.ToLower(strings.TrimSpace(x.Transport))
	if x.Transport == "" {
		x.Transport = "tcp"
	}
	switch x.Transport {
	case "tcp", "ws", "grpc":
	default:
		return fmt.Errorf("unsupported Xray transport %q", x.Transport)
	}
	x.Security = strings.ToLower(strings.TrimSpace(x.Security))
	if x.Security == "" {
		x.Security = "reality"
	}
	switch x.Security {
	case "reality", "tls", "none":
	default:
		return fmt.Errorf("unsupported Xray security %q", x.Security)
	}
	if x.Transport == "tcp" && x.Security != "none" && x.Flow == "" {
		x.Flow = "xtls-rprx-vision"
	}
	if x.Fingerprint == "" {
		x.Fingerprint = "chrome"
	}
	if x.WSPath == "" {
		x.WSPath = "/"
	}
	if x.GRPCServiceName == "" {
		x.GRPCServiceName = "grpc"
	}
	if x.APIPort == 0 {
		x.APIPort = APIPort
	}
	if x.InboundTag == "" {
		x.InboundTag = InboundTag
	}
	if x.Security == "reality" {
		if x.RealityDest == "" {
			x.RealityDest = "www.cloudflare.com:443"
		}
		if len(x.RealitySNI) == 0 {
			host := strings.Split(x.RealityDest, ":")[0]
			x.RealitySNI = []string{host}
		}
		if x.RealityPrivateKey == "" || x.RealityPublicKey == "" {
			priv, pub, err := GenerateRealityKeypair()
			if err != nil {
				return err
			}
			x.RealityPrivateKey, x.RealityPublicKey = priv, pub
		}
		if len(x.RealityShortIDs) == 0 {
			x.RealityShortIDs = []string{GenerateShortID()}
		}
	}
	if x.Security == "tls" {
		if len(x.ALPN) == 0 {
			x.ALPN = []string{"h2", "http/1.1"}
		}
	}
	if len(x.ALPN) == 0 && x.Security == "reality" {
		x.ALPN = []string{"h2", "http/1.1"}
	}
	return nil
}

// Render produces the Xray JSON config and its SHA-256 hash.
func Render(n *model.Node, users []model.User) (string, string, error) {
	if n == nil {
		return "", "", fmt.Errorf("nil node")
	}
	x := n.Xray
	if x.RealityPrivateKey == "" && x.Security == "reality" {
		return "", "", fmt.Errorf("REALITY private key is not configured")
	}
	apiPort := x.APIPort
	if apiPort == 0 {
		apiPort = APIPort
	}
	inboundTag := x.InboundTag
	if inboundTag == "" {
		inboundTag = InboundTag
	}
	nowT := time.Now().UTC()
	clients := []map[string]any{}
	for _, u := range users {
		if !u.Enabled || u.XrayUUID == "" || u.Expired(nowT) || u.TrafficExceeded() {
			continue
		}
		c := map[string]any{"id": u.XrayUUID, "email": u.UserID, "level": 0}
		if x.Transport == "tcp" && x.Security != "none" && x.Flow != "" {
			c["flow"] = x.Flow
		}
		clients = append(clients, c)
	}
	stream := map[string]any{"network": x.Transport}
	switch x.Security {
	case "reality":
		rs := map[string]any{
			"dest":        x.RealityDest,
			"serverNames": x.RealitySNI,
			"privateKey":  x.RealityPrivateKey,
			"shortIds":    x.RealityShortIDs,
		}
		if x.RealitySpiderX != "" {
			rs["spiderX"] = x.RealitySpiderX
		}
		stream["security"] = "reality"
		stream["realitySettings"] = rs
	case "tls":
		serverName := ""
		if len(x.RealitySNI) > 0 {
			serverName = x.RealitySNI[0]
		}
		if serverName == "" {
			serverName = n.Domain
		}
		stream["security"] = "tls"
		stream["tlsSettings"] = map[string]any{
			"serverName": serverName,
			"minVersion": "1.2",
			"alpn":       x.ALPN,
			"certificates": []map[string]any{{
				"certificateFile": CertPath,
				"keyFile":         KeyPath,
			}},
		}
	default:
		stream["security"] = "none"
	}
	switch x.Transport {
	case "ws":
		headers := map[string]any{}
		if x.WSHost != "" {
			headers["Host"] = x.WSHost
		}
		stream["wsSettings"] = map[string]any{"path": x.WSPath, "headers": headers}
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": x.GRPCServiceName}
	}
	config := map[string]any{
		"log":   map[string]any{"loglevel": "warning", "access": AccessLog, "error": ""},
		"api":   map[string]any{"tag": "API", "services": []string{"HandlerService", "StatsService"}},
		"stats": map[string]any{},
		"policy": map[string]any{
			"levels": map[string]any{"0": map[string]any{
				"statsUserUplink": true, "statsUserDownlink": true,
			}},
			"system": map[string]any{
				"statsInboundUplink": true, "statsInboundDownlink": true,
				"statsOutboundUplink": true, "statsOutboundDownlink": true,
			},
		},
		"inbounds": []map[string]any{
			{
				"listen": "127.0.0.1", "port": apiPort, "protocol": "dokodemo-door",
				"settings": map[string]any{"address": "127.0.0.1"}, "tag": "API_INBOUND",
			},
			{
				"listen": "0.0.0.0", "port": n.Port, "protocol": "vless", "tag": inboundTag,
				"settings":       map[string]any{"clients": clients, "decryption": "none"},
				"streamSettings": stream,
				"sniffing":       map[string]any{"enabled": true, "destOverride": []string{"http", "tls", "quic"}, "routeOnly": true},
			},
		},
		"outbounds": []map[string]any{
			{"protocol": "freedom", "tag": "direct"},
			{"protocol": "blackhole", "tag": "block"},
		},
		"routing": map[string]any{
			"domainStrategy": "IPIfNonMatch",
			"rules": []map[string]any{{
				"type": "field", "inboundTag": []string{"API_INBOUND"}, "outboundTag": "API",
			}},
		},
	}
	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256(b)
	return string(b), hex.EncodeToString(sum[:]), nil
}
