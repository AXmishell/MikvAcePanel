package server

import (
	"context"
	"testing"
	"time"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func TestUserAuthSnapshot(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()

	group, err := env.store.CreateGroup(ctx, &model.Group{Name: "EU", MaxDevices: 3})
	if err != nil {
		t.Fatal(err)
	}
	node, err := env.store.CreateNode(ctx, &model.Node{Name: "node", Active: true}, "hash", "prefix", nil)
	if err != nil {
		t.Fatal(err)
	}
	alice, err := env.store.CreateUser(ctx, &model.User{
		UserID: "alice", Password: "alice-pw", Enabled: true, TrafficLimit: 1000,
	}, []int64{group.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.store.CreateUser(ctx, &model.User{
		UserID: "bob", Password: "bob-pw", Enabled: true, MaxDevices: 2,
	}, []int64{group.ID}, nil); err != nil {
		t.Fatal(err)
	}
	expired, err := env.store.CreateUser(ctx, &model.User{
		UserID: "expired", Password: "pw", Enabled: true,
		ExpireAt: timePtr(time.Now().Add(-time.Hour)),
	}, []int64{group.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = expired
	over, err := env.store.CreateUser(ctx, &model.User{
		UserID: "over", Password: "pw", Enabled: true, TrafficLimit: 10,
	}, []int64{group.ID}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.AddUserTraffic(ctx, over.UserID, 10, 0); err != nil {
		t.Fatal(err)
	}
	if err := env.store.SetUserEnabled(ctx, alice.ID, true); err != nil {
		t.Fatal(err)
	}

	snap, err := env.app.userAuthSnapshot(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]model.UserAuth{}
	for _, u := range snap.Users {
		byID[u.UserID] = u
	}
	got, ok := byID["alice"]
	if !ok {
		t.Fatalf("alice missing from snapshot: %+v", snap.Users)
	}
	if got.Password != "alice-pw" || got.MaxDevices != 3 {
		t.Fatalf("alice = %+v", got)
	}
	if got, ok := byID["bob"]; !ok || got.MaxDevices != 2 {
		t.Fatalf("bob should keep the user-specific limit: %+v", got)
	}
	if _, ok := byID["expired"]; ok {
		t.Fatal("expired user must not be in the snapshot")
	}
	if _, ok := byID["over"]; ok {
		t.Fatal("over-quota user must not be in the snapshot")
	}
	if snap.NodeID != node.ID || snap.GraceMinutes <= 0 {
		t.Fatalf("snapshot metadata = %+v", snap)
	}
}

func timePtr(t time.Time) *time.Time { return &t }
