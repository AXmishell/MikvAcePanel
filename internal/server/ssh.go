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
	s.pinNodeSSHHostKey(id, n.SSHHostKey, hostKey)
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
	envArgs := fmt.Sprintf("  -e PANEL_URL=%s \\\n  -e NODE_TOKEN=%s \\\n  -e HY2_RUNTIME=%s \\\n",
		shellQuote(s.panelBaseURL(context.Background())), shellQuote(n.Token), shellQuote(runtime))
	extra := ""
	if runtime == "docker" {
		sockCheck = `
if [[ ! -S /var/run/docker.sock ]]; then
  echo "Docker runtime requires /var/run/docker.sock on the host." >&2
  exit 1
fi
`
		envArgs += fmt.Sprintf("  -e HOST_DATA_DIR=%s \\\n", shellQuote(nodeDataDir))
		extra = "  -v /var/run/docker.sock:/var/run/docker.sock \\\n"
	}
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
%s%s%s
mkdir -p %s
docker rm -f mikvace-node >/dev/null 2>&1 || true
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
%s%s  -v %s:/data \
  ghcr.io/axmishell/mikvacepanel-node:latest
echo "MikvAce node agent started."
`, dockerCheck, installDocker, sockCheck, nodeDataDir, envArgs, extra, nodeDataDir)
}

// nodeRestartScript restarts the standard Docker container, falling back to a
// systemd unit used by binary deployments. It never reinstalls or rewrites the
// node; that remains an explicit SSH install action.
const nodeRestartScript = `#!/usr/bin/env bash
set -euo pipefail
if command -v docker >/dev/null 2>&1; then
  if docker ps -a --format '{{.Names}}' | grep -qx 'mikvace-node'; then
    docker restart mikvace-node
    exit 0
  fi
fi
if command -v systemctl >/dev/null 2>&1; then
  if systemctl cat mikvace-node.service >/dev/null 2>&1; then
    systemctl restart mikvace-node
    exit 0
  fi
fi
echo "mikvace-node container or service was not found" >&2
exit 1
`

func nodeSSHAddress(n *model.Node) string {
	if n == nil {
		return ""
	}
	host := strings.TrimSpace(n.Domain)
	if host == "" {
		host = strings.TrimSpace(n.IP)
	}
	return host
}

func nodeSSHReady(n *model.Node) bool {
	if n == nil || nodeSSHAddress(n) == "" || strings.TrimSpace(n.SSHUser) == "" {
		return false
	}
	return n.SSHPassword != "" || strings.TrimSpace(n.SSHKey) != ""
}

// pinNodeSSHHostKey stores a trust-on-first-use key. It is shared by install
// and restart so the first successful SSH connection pins the host for later
// automatic recovery attempts.
func (s *Server) pinNodeSSHHostKey(nodeID int64, configured, hostKey string) {
	if hostKey == "" || strings.TrimSpace(configured) != "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.SetNodeSSHHostKey(ctx, nodeID, hostKey); err == nil {
		s.store.AddEvent(ctx, "node.ssh_host_key_pinned", nodeID, "info",
			"已固定节点 SSH 主机密钥："+sshHostKeyFingerprint(hostKey))
	}
}

func (s *Server) runNodeSSHRestart(n *model.Node) sshInstallResult {
	host := nodeSSHAddress(n)
	port := n.SSHPort
	if port == 0 {
		port = 22
	}
	result, hostKey := runSSHInstall(host, port, n.SSHUser, n.SSHPassword, n.SSHKey, n.SSHHostKey, nodeRestartScript)
	s.pinNodeSSHHostKey(n.ID, n.SSHHostKey, hostKey)
	return result
}

// performNodeRestart restarts the whole node (agent and cores). A connected
// agent does this through the control channel; otherwise the panel uses the
// saved SSH credentials. The returned method is empty when neither is usable.
func (s *Server) performNodeRestart(n *model.Node) (string, sshInstallResult) {
	if n == nil {
		return "", sshInstallResult{Error: "node not found"}
	}
	if s.hub.Connected(n.ID) {
		if _, ok := s.hub.SendCommand(n.ID, "restart-node"); ok {
			if s.watchdog != nil {
				s.watchdog.markCommand(n.ID, "restart-node")
			}
			return "agent", sshInstallResult{OK: true, Output: "agent restart command sent"}
		}
	}
	if !nodeSSHReady(n) {
		return "", sshInstallResult{Error: "node agent is offline and SSH credentials are not configured"}
	}
	return "ssh", s.runNodeSSHRestart(n)
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
