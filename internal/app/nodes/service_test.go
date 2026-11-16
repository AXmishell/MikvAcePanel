package nodes

import (
	"context"
	"errors"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type fakeRepo struct {
	nodes  map[int64]*model.Node
	nextID int64
}

func newFakeRepo() *fakeRepo { return &fakeRepo{nodes: map[int64]*model.Node{}, nextID: 1} }

func (r *fakeRepo) ListNodes(context.Context) ([]model.Node, error) {
	out := make([]model.Node, 0, len(r.nodes))
	for _, n := range r.nodes {
		out = append(out, *n)
	}
	return out, nil
}

func (r *fakeRepo) GetNode(_ context.Context, id int64) (*model.Node, error) {
	n, ok := r.nodes[id]
	if !ok {
		return nil, errors.New("not found")
	}
	c := *n
	return &c, nil
}

func (r *fakeRepo) CreateNode(_ context.Context, n *model.Node, _, _ string, _ []int64) (*model.Node, error) {
	n.ID = r.nextID
	r.nextID++
	c := *n
	r.nodes[n.ID] = &c
	return &c, nil
}

func (r *fakeRepo) UpdateNode(_ context.Context, n *model.Node, _ []int64) (*model.Node, error) {
	c := *n
	r.nodes[n.ID] = &c
	return &c, nil
}

func (r *fakeRepo) DeleteNode(_ context.Context, id int64) error {
	delete(r.nodes, id)
	return nil
}

func (r *fakeRepo) ReorderNodes(_ context.Context, ids []int64) error {
	for i, id := range ids {
		if n, ok := r.nodes[id]; ok {
			n.Ranking = float64(i + 1)
		}
	}
	return nil
}

func (r *fakeRepo) ResetNodeToken(_ context.Context, id int64, token, _, _ string) error {
	r.nodes[id].Token = token
	return nil
}

type fakePusher struct {
	synced bool
	pushed bool
}

func (p *fakePusher) SyncNodeConfig(context.Context, *model.Node, bool) (*model.NodeConfig, bool) {
	p.synced = true
	return &model.NodeConfig{Revision: 3}, p.pushed
}

func (p *fakePusher) SendUserSnapshot(context.Context, int64) {}

type fakeHub struct{ disconnected []int64 }

func (h *fakeHub) Disconnect(id int64) { h.disconnected = append(h.disconnected, id) }

type fakeEvents struct{ events []string }

func (e *fakeEvents) AddEvent(_ context.Context, typ string, _ int64, _, _ string) {
	e.events = append(e.events, typ)
}

func TestCreateGeneratesTokenAndPushesConfig(t *testing.T) {
	repo := newFakeRepo()
	pusher := &fakePusher{pushed: true}
	svc := New(repo, pusher, &fakeHub{}, &fakeEvents{})
	res, err := svc.Create(context.Background(), &Input{Name: "node-1", Port: 443}, nil)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if res.Node.ID == 0 || res.Token == "" || res.TokenHash == "" {
		t.Fatalf("incomplete result: %+v", res)
	}
	if res.Node.Token != res.Token {
		t.Fatalf("stored token = %q, want %q", res.Node.Token, res.Token)
	}
	if !pusher.synced || !res.Pushed || res.Config.Revision != 3 {
		t.Fatalf("config was not pushed: synced=%v pushed=%v config=%+v", pusher.synced, res.Pushed, res.Config)
	}
}

func TestCreateRejectsInvalidNode(t *testing.T) {
	_, err := New(newFakeRepo(), nil, nil, nil).Create(context.Background(), &Input{}, nil)
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestReorderRejectsDuplicates(t *testing.T) {
	err := New(newFakeRepo(), nil, nil, nil).Reorder(context.Background(), []int64{1, 1})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
}

func TestDeleteDisconnectsAgent(t *testing.T) {
	repo := newFakeRepo()
	hub := &fakeHub{}
	events := &fakeEvents{}
	svc := New(repo, nil, hub, events)
	res, _ := svc.Create(context.Background(), &Input{Name: "node-1", Port: 443}, nil)
	if err := svc.Delete(context.Background(), res.Node.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(hub.disconnected) != 1 || hub.disconnected[0] != res.Node.ID {
		t.Fatalf("disconnected = %v", hub.disconnected)
	}
	if len(events.events) != 1 || events.events[0] != "node.deleted" {
		t.Fatalf("events = %v", events.events)
	}
}
