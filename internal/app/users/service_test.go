package users

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/axmishell/mikvacepanel/internal/model"
)

type fakeRepo struct {
	users  map[int64]*model.User
	nextID int64
	events []string
	tokens map[string]int64
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{users: map[int64]*model.User{}, nextID: 1, tokens: map[string]int64{}}
}

func (r *fakeRepo) clone(u *model.User) *model.User { c := *u; return &c }

func (r *fakeRepo) ListUsers(context.Context, string, string, int, int) ([]model.User, int, error) {
	out := make([]model.User, 0, len(r.users))
	for _, u := range r.users {
		out = append(out, *u)
	}
	return out, len(out), nil
}

func (r *fakeRepo) GetUserByID(_ context.Context, id int64) (*model.User, error) {
	u, ok := r.users[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return r.clone(u), nil
}

func (r *fakeRepo) GetUserByToken(_ context.Context, token string) (*model.User, error) {
	id, ok := r.tokens[token]
	if !ok {
		return nil, errors.New("not found")
	}
	return r.GetUserByID(context.Background(), id)
}

func (r *fakeRepo) CreateUser(_ context.Context, u *model.User, _, _ []int64) (*model.User, error) {
	u.ID = r.nextID
	r.nextID++
	r.users[u.ID] = r.clone(u)
	if u.SubscriptionToken != "" {
		r.tokens[u.SubscriptionToken] = u.ID
	}
	return r.clone(u), nil
}

func (r *fakeRepo) UpdateUser(_ context.Context, u *model.User, _, _ []int64) (*model.User, error) {
	r.users[u.ID] = r.clone(u)
	return r.clone(u), nil
}

func (r *fakeRepo) DeleteUser(_ context.Context, id int64) error {
	delete(r.users, id)
	return nil
}

func (r *fakeRepo) SetUserEnabled(_ context.Context, id int64, enabled bool) error {
	r.users[id].Enabled = enabled
	return nil
}

func (r *fakeRepo) ResetUserSubscriptionToken(_ context.Context, id int64) (string, error) {
	return "reset-token", nil
}

func (r *fakeRepo) SetUserSubscriptionToken(_ context.Context, id int64, token string) error {
	r.users[id].SubscriptionToken = token
	r.tokens[token] = id
	return nil
}

func (r *fakeRepo) ResetUserPassword(_ context.Context, id int64, password string) error {
	r.users[id].Password = password
	return nil
}

func (r *fakeRepo) ResetUserTraffic(_ context.Context, id int64) error {
	r.users[id].TrafficTx, r.users[id].TrafficRx = 0, 0
	return nil
}

type fakeEvents struct{ events []string }

func (e *fakeEvents) AddEvent(_ context.Context, typ string, _ int64, _, _ string) {
	e.events = append(e.events, typ)
}

type fakeDevices struct{ reset []string }

func (d *fakeDevices) ResetDeviceLimit(userID string) { d.reset = append(d.reset, userID) }

type fakeSyncer struct{ calls int }

func (s *fakeSyncer) SyncXrayNodes(context.Context) { s.calls++ }

func TestCreateGeneratesCredentialsAndRecordsEvent(t *testing.T) {
	repo := newFakeRepo()
	events := &fakeEvents{}
	syncer := &fakeSyncer{}
	svc := New(repo, nil, events, &fakeDevices{}, syncer)
	res, err := svc.Create(context.Background(), Input{UserID: "alice"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if res.User.ID == 0 || res.Password == "" {
		t.Fatalf("Create() returned incomplete result: %+v", res)
	}
	if res.User.Password != res.Password {
		t.Fatalf("stored password = %q, want %q", res.User.Password, res.Password)
	}
	if len(events.events) != 1 || events.events[0] != "user.created" {
		t.Fatalf("events = %v", events.events)
	}
	if syncer.calls != 1 {
		t.Fatalf("sync calls = %d, want 1", syncer.calls)
	}
}

func TestCreateRejectsInvalidPassword(t *testing.T) {
	_, err := New(newFakeRepo(), nil, nil, nil, nil).Create(context.Background(), Input{UserID: "alice", Password: "short"})
	var validation *ValidationError
	if !errors.As(err, &validation) {
		t.Fatalf("error = %v, want ValidationError", err)
	}
	if !strings.Contains(validation.Message, "at least 8") {
		t.Fatalf("message = %q", validation.Message)
	}
}

func TestUpdateRejectsDuplicateSubscriptionToken(t *testing.T) {
	repo := newFakeRepo()
	svc := New(repo, nil, nil, nil, nil)
	first, _ := svc.Create(context.Background(), Input{UserID: "alice", SubscriptionToken: "token-one"})
	second, _ := svc.Create(context.Background(), Input{UserID: "bob", SubscriptionToken: "token-two"})
	_, err := svc.Update(context.Background(), second.User.ID, Input{UserID: "bob", SubscriptionToken: "token-one"})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v, want ConflictError", err)
	}
	_ = first
}

func TestSetEnabledAndDeleteManageDeviceState(t *testing.T) {
	repo := newFakeRepo()
	devices := &fakeDevices{}
	events := &fakeEvents{}
	svc := New(repo, nil, events, devices, nil)
	created, _ := svc.Create(context.Background(), Input{UserID: "alice"})
	if err := svc.SetEnabled(context.Background(), created.User.ID, false); err != nil {
		t.Fatalf("SetEnabled() error = %v", err)
	}
	if repo.users[created.User.ID].Enabled {
		t.Fatal("user is still enabled")
	}
	if err := svc.Delete(context.Background(), created.User.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if len(devices.reset) != 1 || devices.reset[0] != "alice" {
		t.Fatalf("device resets = %v", devices.reset)
	}
}
