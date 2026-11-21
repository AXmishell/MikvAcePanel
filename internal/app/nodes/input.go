package nodes

import "github.com/axmishell/mikvacepanel/internal/model"

// Input is the normalized node payload accepted by Build. It mirrors the
// administrator API payload while remaining independent of net/http.
type Input struct {
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

// GroupIDsList returns the union of groupIds and groups, preserving the
// compatibility behaviour of the HTTP API.
func (p *Input) GroupIDsList() []int64 {
	if p.GroupIDs == nil && p.Groups == nil {
		return nil
	}
	out := append([]int64{}, p.GroupIDs...)
	out = append(out, p.Groups...)
	return out
}
