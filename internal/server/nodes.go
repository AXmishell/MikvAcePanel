package server

import (
	"github.com/axmishell/mikvacepanel/internal/app/nodes"
	"regexp"

	"github.com/axmishell/mikvacepanel/internal/model"
)

var portRangePattern = regexp.MustCompile(`^[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*$`)

// CapabilityMultiCore is advertised by agents that can apply several core
// configs in one push.
const CapabilityMultiCore = "multi-core.v1"

type nodePayload struct {
	Name             string             `json:"name"`
	Type             string             `json:"type"`
	Flag             string             `json:"flag"`
	IP               string             `json:"ip"`
	Domain           string             `json:"domain"`
	SNI              string             `json:"sni"`
	Listen           string             `json:"listen"`
	Port             int                `json:"port"`
	PortRange        string             `json:"portRange"`
	HopInterval      string             `json:"hopInterval"`
	ObfsType         string             `json:"obfsType"`
	ObfsPassword     string             `json:"obfsPassword"`
	StatsPort        int                `json:"statsPort"`
	SSHPort          int                `json:"sshPort"`
	SSHUser          string             `json:"sshUser"`
	SSHPassword      string             `json:"sshPassword"`
	SSHKey           string             `json:"sshKey"`
	SSHHostKey       string             `json:"sshHostKey"`
	SSHInstallDocker bool               `json:"sshInstallDocker"`
	TLSMode          string             `json:"tlsMode"`
	ACMEEmail        string             `json:"acmeEmail"`
	BandwidthUp      string             `json:"bandwidthUp"`
	BandwidthDown    string             `json:"bandwidthDown"`
	MasqueradeURL    string             `json:"masqueradeUrl"`
	ACL              []string           `json:"acl"`
	Outbounds        []model.Outbound   `json:"outbounds"`
	Xray             model.XraySettings `json:"xray"`
	Hy2              model.Hy2Settings  `json:"hy2"`
	ECHPEM           string             `json:"echPem"`
	ClearECH         bool               `json:"clearEch"`
	Cores            []string           `json:"cores"`
	RuntimeMode      string             `json:"runtimeMode"`
	MaxOnline        int                `json:"maxOnline"`
	Ranking          float64            `json:"ranking"`
	Active           *bool              `json:"active"`
	AutoRestart      *bool              `json:"autoRestart"`
	GroupIDs         []int64            `json:"groupIds"`
	Groups           []int64            `json:"groups"`
}

func (p *nodePayload) groups() []int64 {
	if p.GroupIDs == nil && p.Groups == nil {
		return nil
	}
	out := append([]int64{}, p.GroupIDs...)
	out = append(out, p.Groups...)
	return out
}

func (p *nodePayload) input() *nodes.Input {
	return &nodes.Input{
		Name: p.Name, Type: p.Type, Flag: p.Flag, IP: p.IP, Domain: p.Domain, SNI: p.SNI,
		Listen: p.Listen, Port: p.Port, PortRange: p.PortRange, HopInterval: p.HopInterval,
		ObfsType: p.ObfsType, ObfsPassword: p.ObfsPassword, StatsPort: p.StatsPort,
		SSHPort: p.SSHPort, SSHUser: p.SSHUser, SSHPassword: p.SSHPassword, SSHKey: p.SSHKey,
		SSHHostKey: p.SSHHostKey, SSHInstallDocker: p.SSHInstallDocker,
		TLSMode: p.TLSMode, ACMEEmail: p.ACMEEmail, BandwidthUp: p.BandwidthUp,
		BandwidthDown: p.BandwidthDown, MasqueradeURL: p.MasqueradeURL, ACL: p.ACL,
		Outbounds: p.Outbounds, Xray: p.Xray, Hy2: p.Hy2, ECHPEM: p.ECHPEM,
		ClearECH: p.ClearECH, Cores: p.Cores, RuntimeMode: p.RuntimeMode,
		MaxOnline: p.MaxOnline, Ranking: p.Ranking, Active: p.Active,
		AutoRestart: p.AutoRestart,
		GroupIDs:    p.GroupIDs, Groups: p.Groups,
	}
}

// publicNode returns a copy safe to send to the browser: REALITY private keys
// stay on the panel and are preserved on update by matching inbound tags.
func publicNode(n *model.Node) *model.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Cores = n.EffectiveCores()
	c.Xray = n.Xray
	c.Xray.RealityPrivateKey = ""
	c.Hy2 = n.Hy2
	// ECH private keys must never leave the panel; only the client config list
	// (Hy2.ECHConfig) is public.
	c.Hy2.ECHPEM = ""
	c.Xray.Inbounds = make([]model.XrayInbound, len(n.Xray.Inbounds))
	for i := range n.Xray.Inbounds {
		c.Xray.Inbounds[i] = n.Xray.Inbounds[i]
		c.Xray.Inbounds[i].RealityPrivateKey = ""
	}
	return &c
}
