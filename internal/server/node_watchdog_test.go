package server

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/crypto/ssh"

	"github.com/axmishell/mikvacepanel/internal/store"
)

func createTestNode(t *testing.T, env *testEnv, payload map[string]any) (int64, string) {
	t.Helper()
	if payload == nil {
		payload = map[string]any{}
	}
	if _, ok := payload["name"]; !ok {
		payload["name"] = "Watchdog Node"
	}
	if _, ok := payload["ip"]; !ok {
		payload["ip"] = "127.0.0.1"
	}
	if _, ok := payload["port"]; !ok {
		payload["port"] = 443
	}
	if _, ok := payload["tlsMode"]; !ok {
		payload["tlsMode"] = "selfsigned"
	}
	if _, ok := payload["active"]; !ok {
		payload["active"] = true
	}
	resp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, payload))
	node := resp["node"].(map[string]any)
	id := int64(node["id"].(float64))
	n, err := env.store.GetNode(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return id, n.Token
}

func connectTestAgent(t *testing.T, env *testEnv, token string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(env.srv.URL, "http") + "/api/node/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{
		"Authorization": []string{"Bearer " + token},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if err := conn.WriteJSON(map[string]any{"type": "hello", "payload": map[string]any{
		"version": "test", "os": "linux", "arch": "amd64", "runtime": "process",
		"capabilities": []string{CapabilityMultiCore},
	}}); err != nil {
		t.Fatal(err)
	}
	return conn
}

func waitConnected(t *testing.T, env *testEnv, nodeID int64) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if env.app.hub.Connected(nodeID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("agent did not connect")
}

func readAgentMessage(t *testing.T, conn *websocket.Conn, want string) map[string]any {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read %s: %v", want, err)
		}
		if msg["type"] == want {
			return msg
		}
	}
}

func TestNodeAutoRestartDefaultsAndUpdate(t *testing.T) {
	env := newTestEnv(t)
	defaultID, _ := createTestNode(t, env, map[string]any{"name": "default restart"})
	n, err := env.store.GetNode(context.Background(), defaultID)
	if err != nil {
		t.Fatal(err)
	}
	if !n.AutoRestart {
		t.Fatal("new nodes must enable auto restart by default")
	}

	offID, _ := createTestNode(t, env, map[string]any{"name": "manual only", "autoRestart": false})
	n, err = env.store.GetNode(context.Background(), offID)
	if err != nil {
		t.Fatal(err)
	}
	if n.AutoRestart {
		t.Fatal("autoRestart=false was not persisted")
	}

	resp := decode[map[string]any](t, env.do(t, "PUT", "/api/nodes/"+itoa(offID), env.token, map[string]any{
		"name": "manual only", "ip": "127.0.0.1", "port": 443,
		"tlsMode": "selfsigned", "active": true, "autoRestart": true,
	}))
	if resp["node"].(map[string]any)["autoRestart"] != true {
		t.Fatalf("update response = %+v", resp)
	}
	n, _ = env.store.GetNode(context.Background(), offID)
	if !n.AutoRestart {
		t.Fatal("autoRestart update was not persisted")
	}
}

func TestConfigPushCarriesAutoRestartPolicy(t *testing.T) {
	env := newTestEnv(t)
	nodeID, token := createTestNode(t, env, nil)
	conn := connectTestAgent(t, env, token)
	first := readAgentMessage(t, conn, "config")
	if first["payload"].(map[string]any)["autoRestart"] != true {
		t.Fatalf("initial autoRestart = %v", first["payload"].(map[string]any)["autoRestart"])
	}

	// Toggling only the node flag does not change the rendered proxy config,
	// so the handler must force a push for the agent to see it.
	env.do(t, "PUT", "/api/nodes/"+itoa(nodeID), env.token, map[string]any{
		"name": "Watchdog Node", "ip": "127.0.0.1", "port": 443,
		"tlsMode": "selfsigned", "active": true, "autoRestart": false,
	}).Body.Close()
	second := readAgentMessage(t, conn, "config")
	if second["payload"].(map[string]any)["autoRestart"] != false {
		t.Fatalf("updated autoRestart = %v", second["payload"].(map[string]any)["autoRestart"])
	}
}

func TestNodeWatchdogRestartsFailedCore(t *testing.T) {
	env := newTestEnv(t)
	nodeID, token := createTestNode(t, env, nil)
	conn := connectTestAgent(t, env, token)
	waitConnected(t, env, nodeID)

	if err := conn.WriteJSON(map[string]any{"type": "heartbeat", "payload": map[string]any{
		"coresRunning": map[string]bool{"hysteria": false},
		"coresDesired": map[string]bool{"hysteria": true},
	}}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		env.app.watchdog.mu.Lock()
		_, ok := env.app.watchdog.health[nodeID]
		env.app.watchdog.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat was not observed by the watchdog")
		}
		time.Sleep(10 * time.Millisecond)
	}

	// Pretend the failure has persisted longer than the grace period.
	env.app.watchdog.mu.Lock()
	env.app.watchdog.health[nodeID].stoppedSince["hysteria"] = time.Now().Add(-time.Minute)
	env.app.watchdog.mu.Unlock()
	env.app.runNodeWatchdogOnce(context.Background())

	msg := readAgentMessage(t, conn, "command")
	payload := msg["payload"].(map[string]any)
	if payload["action"] != "restart" {
		t.Fatalf("action = %v", payload["action"])
	}
}

func TestNodeWatchdogIgnoresManualStop(t *testing.T) {
	env := newTestEnv(t)
	nodeID, token := createTestNode(t, env, nil)
	conn := connectTestAgent(t, env, token)
	waitConnected(t, env, nodeID)

	if err := conn.WriteJSON(map[string]any{"type": "heartbeat", "payload": map[string]any{
		"coresRunning": map[string]bool{"hysteria": false},
		"coresDesired": map[string]bool{"hysteria": false},
	}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		env.app.watchdog.mu.Lock()
		state := env.app.watchdog.health[nodeID]
		env.app.watchdog.mu.Unlock()
		if state != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("heartbeat was not observed by the watchdog")
		}
		time.Sleep(10 * time.Millisecond)
	}
	env.app.watchdog.mu.Lock()
	env.app.watchdog.health[nodeID].stoppedSince["hysteria"] = time.Now().Add(-time.Minute)
	env.app.watchdog.mu.Unlock()
	env.app.runNodeWatchdogOnce(context.Background())

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	for {
		var msg map[string]any
		if err := conn.ReadJSON(&msg); err != nil {
			break // expected: no command arrives
		}
		if msg["type"] == "command" {
			t.Fatalf("manual stop was overridden: %+v", msg)
		}
	}
}

func TestRestartNodeEndpointUsesAgent(t *testing.T) {
	env := newTestEnv(t)
	nodeID, token := createTestNode(t, env, nil)
	conn := connectTestAgent(t, env, token)
	waitConnected(t, env, nodeID)

	resp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes/"+itoa(nodeID)+"/restart", env.token, nil))
	if resp["ok"] != true || resp["method"] != "agent" {
		t.Fatalf("restart response = %+v", resp)
	}
	msg := readAgentMessage(t, conn, "command")
	if msg["payload"].(map[string]any)["action"] != "restart-node" {
		t.Fatalf("command = %+v", msg)
	}
}

func TestRestartNodeEndpointOfflineWithoutSSH(t *testing.T) {
	env := newTestEnv(t)
	nodeID, _ := createTestNode(t, env, nil)
	resp := env.do(t, "POST", "/api/nodes/"+itoa(nodeID)+"/restart", env.token, nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestNodeWatchdogRestartsOfflineAgentOverSSH(t *testing.T) {
	env := newTestEnv(t)
	commands := make(chan string, 1)
	port := startTestSSHServer(t, commands)

	nodeID, _ := createTestNode(t, env, map[string]any{
		"name": "offline node", "ip": "127.0.0.1",
		"sshPort": port, "sshUser": "root", "sshPassword": "pw",
	})
	if err := env.store.MarkNodeOnline(context.Background(), nodeID, store.NodeHello{Version: "test"}); err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdateSettings(context.Background(), map[string]string{
		"node_auto_restart_enabled":          "true",
		"node_auto_restart_grace_seconds":    "10",
		"node_auto_restart_cooldown_seconds": "30",
	}); err != nil {
		t.Fatal(err)
	}

	// The first pass records when the node was observed offline.
	env.app.runNodeWatchdogOnce(context.Background())
	env.app.watchdog.mu.Lock()
	env.app.watchdog.offlineSince[nodeID] = time.Now().Add(-time.Minute)
	env.app.watchdog.mu.Unlock()
	env.app.runNodeWatchdogOnce(context.Background())

	select {
	case cmd := <-commands:
		if !strings.Contains(cmd, "mikvace-node") {
			t.Fatalf("restart command = %q", cmd)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SSH restart was not attempted")
	}
}

func startTestSSHServer(t *testing.T, commands chan<- string) int {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &ssh.ServerConfig{
		PasswordCallback: func(c ssh.ConnMetadata, pass []byte) (*ssh.Permissions, error) {
			if c.User() == "root" && string(pass) == "pw" {
				return nil, nil
			}
			return nil, fmt.Errorf("denied")
		},
	}
	cfg.AddHostKey(signer)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				_, chans, reqs, err := ssh.NewServerConn(conn, cfg)
				if err != nil {
					return
				}
				go ssh.DiscardRequests(reqs)
				for nc := range chans {
					if nc.ChannelType() != "session" {
						_ = nc.Reject(ssh.UnknownChannelType, "session only")
						continue
					}
					ch, requests, err := nc.Accept()
					if err != nil {
						return
					}
					go func() {
						defer ch.Close()
						for req := range requests {
							if req.Type != "exec" {
								_ = req.Reply(false, nil)
								continue
							}
							_ = req.Reply(true, nil)
							select {
							case commands <- string(req.Payload[4:]):
							default:
							}
							_, _ = ch.Write([]byte("restarted\n"))
							_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
							return
						}
					}()
				}
			}()
		}
	}()
	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	return port
}
