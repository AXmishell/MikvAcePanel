package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func scanGroup(row interface{ Scan(...any) error }) (*model.Group, error) {
	var g model.Group
	var created, updated string
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.Color, &g.SubscriptionTitle, &g.MaxDevices, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.CreatedAt = parseTime(created)
	g.UpdatedAt = parseTime(updated)
	return &g, nil
}

// ListGroups returns all groups with usage counters.
func (s *Store) ListGroups(ctx context.Context) ([]model.Group, error) {
	rows, err := s.DB.QueryContext(ctx,
		`SELECT g.id, g.name, g.description, g.color, g.subscription_title, g.max_devices, g.created_at, g.updated_at,
		        (SELECT COUNT(*) FROM user_groups ug WHERE ug.group_id = g.id),
		        (SELECT COUNT(*) FROM node_groups ng WHERE ng.group_id = g.id)
		 FROM groups g ORDER BY g.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.Group{}
	for rows.Next() {
		var g model.Group
		var created, updated string
		if err := rows.Scan(&g.ID, &g.Name, &g.Description, &g.Color, &g.SubscriptionTitle, &g.MaxDevices,
			&created, &updated, &g.UserCount, &g.NodeCount); err != nil {
			return nil, err
		}
		g.CreatedAt = parseTime(created)
		g.UpdatedAt = parseTime(updated)
		out = append(out, g)
	}
	return out, rows.Err()
}

// GetGroup loads a group by id.
func (s *Store) GetGroup(ctx context.Context, id int64) (*model.Group, error) {
	row := s.DB.QueryRowContext(ctx,
		`SELECT g.id, g.name, g.description, g.color, g.subscription_title, g.max_devices, g.created_at, g.updated_at,
		        (SELECT COUNT(*) FROM user_groups ug WHERE ug.group_id = g.id),
		        (SELECT COUNT(*) FROM node_groups ng WHERE ng.group_id = g.id)
		 FROM groups g WHERE g.id = ?`, id)
	var g model.Group
	var created, updated string
	err := row.Scan(&g.ID, &g.Name, &g.Description, &g.Color, &g.SubscriptionTitle, &g.MaxDevices,
		&created, &updated, &g.UserCount, &g.NodeCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	g.CreatedAt = parseTime(created)
	g.UpdatedAt = parseTime(updated)
	return &g, nil
}

// CreateGroup inserts a group.
func (s *Store) CreateGroup(ctx context.Context, g *model.Group) (*model.Group, error) {
	nowT := now()
	g.CreatedAt, g.UpdatedAt = nowT, nowT
	res, err := s.DB.ExecContext(ctx,
		`INSERT INTO groups (name, description, color, subscription_title, max_devices, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		g.Name, g.Description, g.Color, g.SubscriptionTitle, g.MaxDevices, ts(nowT), ts(nowT))
	if err != nil {
		return nil, attr(err, "create group")
	}
	id, _ := res.LastInsertId()
	return s.GetGroup(ctx, id)
}

// UpdateGroup updates a group.
func (s *Store) UpdateGroup(ctx context.Context, g *model.Group) (*model.Group, error) {
	res, err := s.DB.ExecContext(ctx,
		`UPDATE groups SET name = ?, description = ?, color = ?, subscription_title = ?, max_devices = ?, updated_at = ?
		 WHERE id = ?`,
		g.Name, g.Description, g.Color, g.SubscriptionTitle, g.MaxDevices, ts(now()), g.ID)
	if err != nil {
		return nil, attr(err, "update group")
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return nil, ErrNotFound
	}
	return s.GetGroup(ctx, g.ID)
}

// DeleteGroup removes a group.
func (s *Store) DeleteGroup(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM groups WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetGroupsByIDs loads groups by their ids.
func (s *Store) GetGroupsByIDs(ctx context.Context, ids []int64) ([]model.Group, error) {
	out := []model.Group{}
	for _, id := range uniqueIDs(ids) {
		g, err := s.GetGroup(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *g)
	}
	return out, nil
}
