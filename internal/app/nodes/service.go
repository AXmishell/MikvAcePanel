package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"

	"github.com/axmishell/mikvacepanel/internal/idgen"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/ports"
)

// Service implements node administration use cases.
type Service struct {
	repo    ports.NodeRepository
	configs ports.NodeConfigPusher
	hub     ports.NodeConnectionManager
	events  ports.EventSink
}

// New builds a node service. Optional dependencies may be nil in tests.
func New(repo ports.NodeRepository, configs ports.NodeConfigPusher, hub ports.NodeConnectionManager, events ports.EventSink) *Service {
	return &Service{repo: repo, configs: configs, hub: hub, events: events}
}

// List returns all nodes.
func (s *Service) List(ctx context.Context) ([]model.Node, error) { return s.repo.ListNodes(ctx) }

// Get returns one node.
func (s *Service) Get(ctx context.Context, id int64) (*model.Node, error) {
	return s.repo.GetNode(ctx, id)
}

// CreateResult describes a newly created node and its first config revision.
type CreateResult struct {
	Node       *model.Node
	Config     *model.NodeConfig
	Pushed     bool
	Token      string
	TokenHash  string
	TokenShort string
}

// Create validates and persists a node, then renders and pushes its config.
func (s *Service) Create(ctx context.Context, in *Input, groupIDs []int64) (*CreateResult, error) {
	n, err := Build(in, nil)
	if err != nil {
		return nil, &ValidationError{Message: err.Error()}
	}
	token := "mtn_" + idgen.Token(24)
	hash := HashToken(token)
	n.Token = token
	created, err := s.repo.CreateNode(ctx, n, hash, token[:12], groupIDs)
	if err != nil {
		return nil, err
	}
	result := &CreateResult{Node: created, Token: token, TokenHash: hash, TokenShort: token[:12]}
	if s.configs != nil {
		result.Config, result.Pushed = s.configs.SyncNodeConfig(ctx, created, false)
	}
	if fresh, err := s.repo.GetNode(ctx, created.ID); err == nil {
		result.Node = fresh
	}
	return result, nil
}

// Update validates and persists a node, then renders and pushes its config.
func (s *Service) Update(ctx context.Context, id int64, in *Input, groupIDs []int64) (*model.Node, *model.NodeConfig, bool, error) {
	existing, err := s.repo.GetNode(ctx, id)
	if err != nil {
		return nil, nil, false, err
	}
	updated, err := Build(in, existing)
	if err != nil {
		return nil, nil, false, &ValidationError{Message: err.Error()}
	}
	saved, err := s.repo.UpdateNode(ctx, updated, groupIDs)
	if err != nil {
		return nil, nil, false, err
	}
	var cfg *model.NodeConfig
	var pushed bool
	if s.configs != nil {
		cfg, pushed = s.configs.SyncNodeConfig(ctx, saved, false)
		if saved.HasCore("hysteria") {
			s.configs.SendUserSnapshot(ctx, saved.ID)
		}
	}
	if fresh, err := s.repo.GetNode(ctx, saved.ID); err == nil {
		saved = fresh
	}
	return saved, cfg, pushed, nil
}

// Delete disconnects the agent and removes the node.
func (s *Service) Delete(ctx context.Context, id int64) error {
	n, _ := s.repo.GetNode(ctx, id)
	if s.hub != nil {
		s.hub.Disconnect(id)
	}
	if err := s.repo.DeleteNode(ctx, id); err != nil {
		return err
	}
	if n != nil && s.events != nil {
		s.events.AddEvent(ctx, "node.deleted", 0, "info", "节点 "+n.Name+" 已删除")
	}
	return nil
}

// Reorder validates duplicate/non-positive IDs and stores the new ranking.
func (s *Service) Reorder(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return &ValidationError{Message: "ids is required"}
	}
	seen := make(map[int64]bool, len(ids))
	clean := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return &ValidationError{Message: "invalid node id"}
		}
		if seen[id] {
			return &ValidationError{Message: "duplicate node id"}
		}
		seen[id] = true
		clean = append(clean, id)
	}
	return s.repo.ReorderNodes(ctx, clean)
}

// ResetToken rotates a node token and disconnects the current agent so it
// reconnects with the new credential.
func (s *Service) ResetToken(ctx context.Context, id int64) (*model.Node, string, error) {
	n, err := s.repo.GetNode(ctx, id)
	if err != nil {
		return nil, "", err
	}
	token := "mtn_" + idgen.Token(24)
	if err := s.repo.ResetNodeToken(ctx, id, token, HashToken(token), token[:12]); err != nil {
		return nil, "", err
	}
	if s.hub != nil {
		s.hub.Disconnect(id)
	}
	n.Token = token
	return n, token, nil
}

// ValidationError reports invalid node input.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

// HashToken returns the stable SHA-256 hex digest used for node tokens.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CheckPortRange is exposed for handlers that accept a standalone port range.
func CheckPortRange(value string) error { return ValidatePortRange(value) }
