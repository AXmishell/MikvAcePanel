package server

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type sshInstallResult struct {
	OK      bool   `json:"ok"`
	Output  string `json:"output"`
	Command string `json:"command,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *Server) handleNodeSSHInstall(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid node id")
		return
	}
	n, err := s.store.GetNode(r.Context(), id)
	if err != nil {
		writeStoreError(w, err, "node")
		return
	}
	if strings.TrimSpace(n.SSHUser) == "" || (n.SSHPassword == "" && n.SSHKey == "") {
		writeError(w, http.StatusBadRequest, "SSH credentials are not configured for this node")
		return
	}
	host := strings.TrimSpace(n.Domain)
	if host == "" {
		host = strings.TrimSpace(n.IP)
	}
	if host == "" {
		writeError(w, http.StatusBadRequest, "node has no IP or domain for SSH")
		return
	}
	port := n.SSHPort
	if port == 0 {
		port = 22
	}
	script := s.sshInstallScript(n)
	s.hub.Disconnect(id)
	s.store.AddEvent(r.Context(), "node.ssh_setup", id, "info", "已开始对节点 "+n.Name+" 执行 SSH 自动安装")
	result, hostKey := runSSHInstall(host, port, n.SSHUser, n.SSHPassword, n.SSHKey, n.SSHHostKey, script)
	if hostKey != "" && strings.TrimSpace(n.SSHHostKey) == "" {
		// Trust-on-first-use: pin the key so subsequent installs cannot be
		// redirected to a different host by a MITM. Use a detached context
		// because the install may outlive the HTTP request.
		pinCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := s.store.SetNodeSSHHostKey(pinCtx, id, hostKey); err == nil {
			s.store.AddEvent(pinCtx, "node.ssh_host_key_pinned", id, "info",
				"已固定节点 SSH 主机密钥："+sshHostKeyFingerprint(hostKey))
		}
		cancel()
	}
	if !result.OK {
		s.store.AddEvent(r.Context(), "node.ssh_setup_failed", id, "warning", "SSH 自动安装失败："+result.Error)
		writeJSON(w, http.StatusBadGateway, result)
		return
	}
	s.store.AddEvent(r.Context(), "node.ssh_setup_ok", id, "info", "节点 "+n.Name+" 的 SSH 自动安装完成")
	result.Command = script
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) sshInstallScript(n *model.Node) string {
	pub, _ := s.store.GetSetting(contextBackground(), "public_url")
	pub = strings.TrimRight(strings.TrimSpace(pub), "/")
	if pub == "" {
		pub = s.cfg.PublicURL
	}
	installDocker := ""
	if n.SSHInstallDocker {
		installDocker = `
if ! command -v docker >/dev/null 2>&1; then
  echo "Installing Docker..."
  curl -fsSL https://get.docker.com | sh
fi
`
	}
	dockerCheck := ""
	if !n.SSHInstallDocker {
		dockerCheck = `
if ! command -v docker >/dev/null 2>&1; then
  echo "Docker is not installed. Enable 'install Docker' or install it manually." >&2
  exit 1
fi
`
	}
	runtime := nodeRuntime(n)
	sockCheck := ""
	extra := ""
	if runtime == "docker" {
		sockCheck = `
if [[ ! -S /var/run/docker.sock ]]; then
  echo "Docker runtime requires /var/run/docker.sock on the host." >&2
  exit 1
fi
`
		extra = "  -e HOST_DATA_DIR=/var/lib/mikvace \\\n  -v /var/run/docker.sock:/var/run/docker.sock \\\n"
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
%s%s%s
mkdir -p /var/lib/mikvace
docker rm -f mikvace-node >/dev/null 2>&1 || true
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL=%s \
  -e NODE_TOKEN=%s \
  -e HY2_RUNTIME=%s \
%s  -e DATA_DIR=/data \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest
echo "MikvAce node agent started."
`, dockerCheck, installDocker, sockCheck, shellQuote(pub), shellQuote(n.Token), runtime, extra)
}

func runSSHInstall(host string, port int, user, password, key, expectedHostKey, script string) (sshInstallResult, string) {
	res := sshInstallResult{}
	var auths []ssh.AuthMethod
	if password != "" {
		auths = append(auths, ssh.Password(password))
	}
	if strings.TrimSpace(key) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(key))
		if err != nil {
			res.Error = "cannot parse SSH private key: " + err.Error()
			return res, ""
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	var observedKey ssh.PublicKey
	cfg := &ssh.ClientConfig{
		User: user,
		Auth: auths,
		HostKeyCallback: func(hostname string, remote net.Addr, hostKey ssh.PublicKey) error {
			if err := verifySSHHostKey(expectedHostKey, hostKey); err != nil {
				return err
			}
			observedKey = hostKey
			return nil
		},
		Timeout: 20 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		res.Error = err.Error()
		return res, ""
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		res.Error = err.Error()
		return res, ""
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	pinned := ""
	if observedKey != nil {
		pinned = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(observedKey)))
	}
	session, err := client.NewSession()
	if err != nil {
		res.Error = err.Error()
		return res, pinned
	}
	defer session.Close()
	cmd := "bash -lc " + shellQuote(script)
	type runResult struct {
		out []byte
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		out, err := session.CombinedOutput(cmd)
		done <- runResult{out: out, err: err}
	}()
	select {
	case rr := <-done:
		res.Output = truncateOutput(string(rr.out), 128*1024)
		if rr.err != nil {
			res.Error = rr.err.Error()
			return res, pinned
		}
		res.OK = true
		return res, pinned
	case <-time.After(5 * time.Minute):
		res.Error = "SSH command timed out after 5 minutes"
		_ = session.Close()
		return res, pinned
	}
}

// verifySSHHostKey accepts the key when no pin exists, otherwise requires an
// exact match. Pins use the authorized_keys format.
func verifySSHHostKey(expected string, key ssh.PublicKey) error {
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return nil
	}
	want, _, _, _, err := ssh.ParseAuthorizedKey([]byte(expected))
	if err != nil {
		return fmt.Errorf("invalid pinned SSH host key: %w", err)
	}
	if !bytes.Equal(want.Marshal(), key.Marshal()) {
		return fmt.Errorf("SSH host key mismatch (got %s)", ssh.FingerprintSHA256(key))
	}
	return nil
}

func sshHostKeyFingerprint(value string) string {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(strings.TrimSpace(value)))
	if err != nil {
		return ""
	}
	return ssh.FingerprintSHA256(key)
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func truncateOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… output truncated"
}

func contextBackground() context.Context { return context.Background() }
