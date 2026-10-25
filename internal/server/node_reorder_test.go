package server

import (
	"context"
	"net/http"
	"testing"
)

func TestReorderNodes(t *testing.T) {
	env := newTestEnv(t)

	ids := make([]int64, 0, 3)
	for _, name := range []string{"Alpha", "Bravo", "Charlie"} {
		resp := decode[map[string]any](t, env.do(t, "POST", "/api/nodes", env.token, map[string]any{
			"name": name, "type": "hysteria", "cores": []string{"hysteria"},
			"ip": "203.0.113.10", "port": 443, "tlsMode": "selfsigned", "active": true,
			"ranking": 1,
		}))
		node, _ := resp["node"].(map[string]any)
		ids = append(ids, int64(node["id"].(float64)))
	}

	order := []int64{ids[2], ids[0], ids[1]}
	resp := env.do(t, "POST", "/api/nodes/reorder", env.token, map[string]any{"ids": order})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("reorder status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	nodes, err := env.store.ListNodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[int64]float64, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n.Ranking
	}
	for i, id := range order {
		want := float64(len(order) - i)
		if byID[id] != want {
			t.Fatalf("node %d ranking = %v, want %v", id, byID[id], want)
		}
	}

	dup := env.do(t, "POST", "/api/nodes/reorder", env.token, map[string]any{"ids": []int64{ids[0], ids[0]}})
	if dup.StatusCode != http.StatusBadRequest {
		t.Fatalf("duplicate reorder status = %d", dup.StatusCode)
	}
	dup.Body.Close()
}
