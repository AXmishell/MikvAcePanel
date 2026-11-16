// Package users implements user administration use cases. It owns validation
// and orchestration, while persistence and side effects are provided through
// ports.
package users

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/idgen"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/ports"
)

var (
	userIDPattern            = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,64}$`)
	subscriptionTokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{6,128}$`)
)

// ValidationError reports invalid user input. Handlers map it to HTTP 400.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// ConflictError reports a uniqueness violation. Handlers map it to HTTP 409.
type ConflictError struct{ Message string }

func (e *ConflictError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// Input is the normalized payload used by Create and Update.
type Input struct {
	UserID            string
	XrayUUID          string
	Username          string
	Password          string
	SubscriptionToken string
	Enabled           *bool
	TrafficLimit      int64
	MaxDevices        int
	ExpireAt          *time.Time
	GroupIDs          []int64
	NodeIDs           []int64
}

// CreateResult contains the stored user and the plaintext password that was
// generated for it.
type CreateResult struct {
	User     *model.User
	Password string
}

// Service implements the user administration use cases.
type Service struct {
	repo     ports.UserRepository
	settings ports.SettingsReader
	events   ports.EventSink
	devices  ports.DeviceTracker
	syncer   ports.NodeConfigSyncer
	now      func() time.Time
}

// New builds a user service. Optional dependencies may be nil in tests.
func New(repo ports.UserRepository, settings ports.SettingsReader, events ports.EventSink, devices ports.DeviceTracker, syncer ports.NodeConfigSyncer) *Service {
	return &Service{repo: repo, settings: settings, events: events, devices: devices, syncer: syncer, now: time.Now}
}

// List returns a page of users.
func (s *Service) List(ctx context.Context, query, status string, page, size int) ([]model.User, int, error) {
	return s.repo.ListUsers(ctx, query, status, page, size)
}

// Get returns one user by primary key.
func (s *Service) Get(ctx context.Context, id int64) (*model.User, error) {
	return s.repo.GetUserByID(ctx, id)
}

// Create validates the input, generates the connection password and stores the
// user.
func (s *Service) Create(ctx context.Context, in Input) (*CreateResult, error) {
	userID := strings.TrimSpace(in.UserID)
	if userID == "" {
		userID = idgen.Token(6)
	}
	if !userIDPattern.MatchString(userID) {
		return nil, invalid("userId must be 2-64 characters (letters, digits, dot, underscore, dash)")
	}
	password := in.Password
	if password == "" {
		password = idgen.Token(10)
	}
	if len(password) < 8 {
		return nil, invalid("password must be at least 8 characters")
	}
	subToken := strings.TrimSpace(in.SubscriptionToken)
	if subToken != "" && !subscriptionTokenPattern.MatchString(subToken) {
		return nil, invalid("subscriptionToken must be 6-128 characters (letters, digits, underscore, dash)")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	uuid := strings.TrimSpace(in.XrayUUID)
	if uuid == "" {
		uuid = idgen.UUID()
	}
	u := &model.User{
		UserID: userID, Username: strings.TrimSpace(in.Username), Password: password,
		SubscriptionToken: subToken, XrayUUID: uuid,
		Enabled: enabled, TrafficLimit: in.TrafficLimit, MaxDevices: in.MaxDevices, ExpireAt: in.ExpireAt,
	}
	created, err := s.repo.CreateUser(ctx, u, in.GroupIDs, in.NodeIDs)
	if err != nil {
		return nil, err
	}
	s.event(ctx, "user.created", "用户 "+created.UserID+" 已创建")
	s.sync(ctx)
	return &CreateResult{User: created, Password: password}, nil
}

// Update applies a partial update. Empty credential fields keep the existing
// values; TrafficLimit and MaxDevices are always written, matching the panel's
// form semantics.
func (s *Service) Update(ctx context.Context, id int64, in Input) (*model.User, error) {
	existing, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(in.UserID); v != "" {
		if !userIDPattern.MatchString(v) {
			return nil, invalid("invalid userId")
		}
		existing.UserID = v
	}
	existing.Username = strings.TrimSpace(in.Username)
	if v := strings.TrimSpace(in.XrayUUID); v != "" {
		existing.XrayUUID = v
	}
	if in.Password != "" {
		if len(in.Password) < 8 {
			return nil, invalid("password must be at least 8 characters")
		}
		existing.Password = in.Password
	}
	newToken := strings.TrimSpace(in.SubscriptionToken)
	if newToken != "" && newToken != existing.SubscriptionToken {
		if !subscriptionTokenPattern.MatchString(newToken) {
			return nil, invalid("subscriptionToken must be 6-128 characters (letters, digits, underscore, dash)")
		}
		if other, err := s.repo.GetUserByToken(ctx, newToken); err == nil && other.ID != existing.ID {
			return nil, &ConflictError{Message: "subscription token already in use"}
		}
	}
	if in.Enabled != nil {
		existing.Enabled = *in.Enabled
	}
	existing.TrafficLimit = in.TrafficLimit
	existing.MaxDevices = in.MaxDevices
	existing.ExpireAt = in.ExpireAt
	updated, err := s.repo.UpdateUser(ctx, existing, in.GroupIDs, in.NodeIDs)
	if err != nil {
		return nil, err
	}
	if newToken != "" && newToken != updated.SubscriptionToken {
		if err := s.repo.SetUserSubscriptionToken(ctx, updated.ID, newToken); err != nil {
			return nil, err
		}
		updated.SubscriptionToken = newToken
	}
	s.event(ctx, "user.updated", "用户 "+updated.UserID+" 已更新")
	s.sync(ctx)
	return updated, nil
}

// Delete removes a user and clears its live device state.
func (s *Service) Delete(ctx context.Context, id int64) error {
	u, _ := s.repo.GetUserByID(ctx, id)
	if err := s.repo.DeleteUser(ctx, id); err != nil {
		return err
	}
	if u != nil {
		if s.devices != nil {
			s.devices.ResetDeviceLimit(u.UserID)
		}
		s.event(ctx, "user.deleted", "用户 "+u.UserID+" 已删除")
		s.sync(ctx)
	}
	return nil
}

// SetEnabled toggles a user and records an audit event.
func (s *Service) SetEnabled(ctx context.Context, id int64, enabled bool) error {
	if err := s.repo.SetUserEnabled(ctx, id, enabled); err != nil {
		return err
	}
	u, _ := s.repo.GetUserByID(ctx, id)
	if u != nil {
		typ := "user.disabled"
		if enabled {
			typ = "user.enabled"
		}
		state := "已禁用"
		if enabled {
			state = "已启用"
		}
		s.event(ctx, typ, "用户 "+u.UserID+" "+state)
		s.sync(ctx)
	}
	return nil
}

// ResetSubscriptionToken rotates a user's subscription token.
func (s *Service) ResetSubscriptionToken(ctx context.Context, id int64) (string, error) {
	return s.repo.ResetUserSubscriptionToken(ctx, id)
}

// ResetPassword resets the proxy connection password. It returns the generated
// plaintext password when none was given.
func (s *Service) ResetPassword(ctx context.Context, id int64, password string) (string, error) {
	if password == "" {
		password = idgen.Token(10)
	}
	if len(password) < 8 {
		return "", invalid("password must be at least 8 characters")
	}
	if err := s.repo.ResetUserPassword(ctx, id, password); err != nil {
		return "", err
	}
	s.sync(ctx)
	return password, nil
}

// ResetTraffic clears a user's traffic counters and re-admits them to Xray.
func (s *Service) ResetTraffic(ctx context.Context, id int64) error {
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.repo.ResetUserTraffic(ctx, id); err != nil {
		return err
	}
	s.event(ctx, "user.traffic_reset", "已重置用户 "+u.UserID+" 的流量统计")
	s.sync(ctx)
	return nil
}

// ResetDevices clears device-limit counters for a user.
func (s *Service) ResetDevices(ctx context.Context, id int64) error {
	u, err := s.repo.GetUserByID(ctx, id)
	if err != nil {
		return err
	}
	if s.devices != nil {
		s.devices.ResetDeviceLimit(u.UserID)
	}
	return nil
}

func (s *Service) event(ctx context.Context, typ, message string) {
	if s.events != nil {
		s.events.AddEvent(ctx, typ, 0, "info", message)
	}
}

func (s *Service) sync(ctx context.Context) {
	if s.syncer != nil {
		s.syncer.SyncXrayNodes(ctx)
	}
}
