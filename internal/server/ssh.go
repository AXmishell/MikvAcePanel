package server

import (
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
	result := runSSHInstall(host, port, n.SSHUser, n.SSHPassword, n.SSHKey, script)
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
	return fmt.Sprintf(`#!/usr/bin/env bash
set -euo pipefail
%s%s
mkdir -p /var/lib/mikvace
docker rm -f mikvace-node >/dev/null 2>&1 || true
docker run -d --name mikvace-node --restart unless-stopped \
  --network host --cap-add NET_ADMIN \
  -e PANEL_URL=%s \
  -e NODE_TOKEN=%s \
  -e HY2_RUNTIME=process \
  -e DATA_DIR=/data \
  -v /var/lib/mikvace:/data \
  ghcr.io/axmishell/mikvacepanel-node:main
echo "MikvAce node agent started."
`, dockerCheck, installDocker, shellQuote(pub), shellQuote(n.Token))
}

func runSSHInstall(host string, port int, user, password, key, script string) sshInstallResult {
	res := sshInstallResult{}
	var auths []ssh.AuthMethod
	if password != "" {
		auths = append(auths, ssh.Password(password))
	}
	if strings.TrimSpace(key) != "" {
		signer, err := ssh.ParsePrivateKey([]byte(key))
		if err != nil {
			res.Error = "cannot parse SSH private key: " + err.Error()
			return res
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            auths,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         20 * time.Second,
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	conn, err := net.DialTimeout("tcp", addr, 20*time.Second)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, cfg)
	if err != nil {
		_ = conn.Close()
		res.Error = err.Error()
		return res
	}
	client := ssh.NewClient(sshConn, chans, reqs)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		res.Error = err.Error()
		return res
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
			return res
		}
		res.OK = true
		return res
	case <-time.After(5 * time.Minute):
		res.Error = "SSH command timed out after 5 minutes"
		_ = session.Close()
		return res
	}
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
