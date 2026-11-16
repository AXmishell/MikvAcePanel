package server

import (
	"time"

	usersvc "github.com/axmishell/mikvacepanel/internal/app/users"
)

type userPayload struct {
	UserID            string     `json:"userId"`
	XrayUUID          string     `json:"xrayUuid"`
	Username          string     `json:"username"`
	Password          string     `json:"password"`
	SubscriptionToken string     `json:"subscriptionToken"`
	Enabled           *bool      `json:"enabled"`
	TrafficLimit      int64      `json:"trafficLimit"`
	TrafficGB         float64    `json:"trafficLimitGb"`
	MaxDevices        int        `json:"maxDevices"`
	ExpireAt          *time.Time `json:"expireAt"`
	GroupIDs          []int64    `json:"groupIds"`
	Groups            []int64    `json:"groups"`
	NodeIDs           []int64    `json:"nodeIds"`
	Nodes             []int64    `json:"nodes"`
}

func (p *userPayload) groups() []int64 {
	if p.GroupIDs == nil && p.Groups == nil {
		return nil
	}
	out := append([]int64{}, p.GroupIDs...)
	out = append(out, p.Groups...)
	return out
}

func (p *userPayload) nodes() []int64 {
	if p.NodeIDs == nil && p.Nodes == nil {
		return nil
	}
	out := append([]int64{}, p.NodeIDs...)
	out = append(out, p.Nodes...)
	return out
}

func (p *userPayload) limitBytes() int64 {
	if p.TrafficLimit > 0 {
		return p.TrafficLimit
	}
	if p.TrafficGB > 0 {
		return int64(p.TrafficGB * 1024 * 1024 * 1024)
	}
	return 0
}

func (p *userPayload) input() usersvc.Input {
	return usersvc.Input{
		UserID: p.UserID, XrayUUID: p.XrayUUID, Username: p.Username,
		Password:          p.Password,
		SubscriptionToken: p.SubscriptionToken, Enabled: p.Enabled,
		TrafficLimit: p.limitBytes(), MaxDevices: p.MaxDevices, ExpireAt: p.ExpireAt,
		GroupIDs: p.groups(), NodeIDs: p.nodes(),
	}
}
