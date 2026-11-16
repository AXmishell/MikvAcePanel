package nodes

import (
	"regexp"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

var (
	realmNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{5,63}$`)
	listenRangePattern = regexp.MustCompile(`^[0-9]{1,5}-[0-9]{1,5}$`)
	sniffPortsPattern  = regexp.MustCompile(`^(all|[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*)$`)
	hostOnlyPattern    = regexp.MustCompile(`^([0-9A-Za-z_.-]+|\[[0-9A-Fa-f:]+\])$`)
	versionPattern     = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
)

// applyHy2SubscriptionDefaults seeds the per-node subscription switches from
// the panel-wide default when a create request omits them. The default only
// applies while creating a node; existing nodes are always driven by their own
// includeTls / includeHopping values.
func ApplyHy2SubscriptionDefaults(h *model.Hy2Settings, mode string) {
	if h == nil {
		return
	}
	off, on := false, true
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "tls":
		if h.IncludeTLS == nil {
			h.IncludeTLS = &on
		}
		if h.IncludeHopping == nil {
			h.IncludeHopping = &off
		}
	case "hopping":
		if h.IncludeTLS == nil {
			h.IncludeTLS = &off
		}
		if h.IncludeHopping == nil {
			h.IncludeHopping = &on
		}
	}
}

// normalizeHy2 trims user input, preserves server-owned ECH material and
// applies conservative defaults.
func NormalizeHy2(p *Input, existing *model.Node) (model.Hy2Settings, error) {
	h := p.Hy2
	// Older API clients do not send a hy2 object at all. Preserve the stored
	// settings instead of silently wiping them.
	if h == (model.Hy2Settings{}) && existing != nil && p.ECHPEM == "" && !p.ClearECH {
		h = existing.Hy2
	}
	if p.ClearECH {
		h.ECHPEM = ""
		h.ECHConfig = ""
	} else if strings.TrimSpace(p.ECHPEM) != "" {
		h.ECHPEM = strings.TrimSpace(p.ECHPEM)
		cfg, err := ParseECHConfigPEM(h.ECHPEM)
		if err != nil {
			return h, err
		}
		h.ECHConfig = cfg
	} else if existing != nil {
		h.ECHPEM = existing.Hy2.ECHPEM
		h.ECHConfig = existing.Hy2.ECHConfig
	}
	if h.ECHPEM != "" && h.ECHConfig == "" {
		cfg, err := ParseECHConfigPEM(h.ECHPEM)
		if err != nil {
			return h, err
		}
		h.ECHConfig = cfg
	}
	if h.QUIC != nil {
		h.QUIC.MaxIdleTimeout = strings.TrimSpace(h.QUIC.MaxIdleTimeout)
	}
	if h.Congestion != nil {
		h.Congestion.Type = strings.ToLower(strings.TrimSpace(h.Congestion.Type))
		h.Congestion.BBRProfile = strings.ToLower(strings.TrimSpace(h.Congestion.BBRProfile))
	}
	if h.Resolver != nil {
		r := h.Resolver
		r.Type = strings.ToLower(strings.TrimSpace(r.Type))
		r.Addr = strings.TrimSpace(r.Addr)
		r.Timeout = strings.TrimSpace(r.Timeout)
		r.SNI = strings.TrimSpace(r.SNI)
	}
	if h.Sniff != nil {
		s := h.Sniff
		s.Timeout = strings.TrimSpace(s.Timeout)
		s.TCPPorts = strings.TrimSpace(s.TCPPorts)
		s.UDPPorts = strings.TrimSpace(s.UDPPorts)
	}
	if h.Masquerade != nil {
		m := h.Masquerade
		m.Type = strings.ToLower(strings.TrimSpace(m.Type))
		m.ProxyURL = strings.TrimSpace(m.ProxyURL)
		m.FileDir = strings.TrimSpace(m.FileDir)
		m.ListenHTTP = strings.TrimSpace(m.ListenHTTP)
		m.ListenHTTPS = strings.TrimSpace(m.ListenHTTPS)
		if len(m.Headers) > 0 {
			headers := map[string]string{}
			for k, v := range m.Headers {
				if strings.TrimSpace(k) != "" {
					headers[strings.TrimSpace(k)] = v
				}
			}
			m.Headers = headers
		}
	}
	if h.ACME != nil {
		a := h.ACME
		a.CA = strings.ToLower(strings.TrimSpace(a.CA))
		a.Type = strings.ToLower(strings.TrimSpace(a.Type))
		a.ListenHost = strings.TrimSpace(a.ListenHost)
		a.DNSName = strings.TrimSpace(a.DNSName)
	}
	if h.TLS != nil {
		h.TLS.SNIGuard = strings.ToLower(strings.TrimSpace(h.TLS.SNIGuard))
	}
	if h.Direct != nil {
		d := h.Direct
		d.Mode = strings.TrimSpace(d.Mode)
		d.BindIPv4 = strings.TrimSpace(d.BindIPv4)
		d.BindIPv6 = strings.TrimSpace(d.BindIPv6)
		d.BindDevice = strings.TrimSpace(d.BindDevice)
	}
	if h.Realm != nil {
		r := h.Realm
		r.URI = strings.TrimSpace(r.URI)
		r.IPMode = strings.ToLower(strings.TrimSpace(r.IPMode))
		r.STUNTimeout = strings.TrimSpace(r.STUNTimeout)
		r.PunchTimeout = strings.TrimSpace(r.PunchTimeout)
		r.HeartbeatInterval = strings.TrimSpace(r.HeartbeatInterval)
		r.PortMapTimeout = strings.TrimSpace(r.PortMapTimeout)
		r.PortMapLifetime = strings.TrimSpace(r.PortMapLifetime)
		servers := []string{}
		for _, s := range r.STUNServers {
			if s = strings.TrimSpace(s); s != "" {
				servers = append(servers, s)
			}
		}
		r.STUNServers = servers
	}
	if h.Mimic != nil {
		h.Mimic.Interface = strings.TrimSpace(h.Mimic.Interface)
		h.Mimic.XDPMode = strings.ToLower(strings.TrimSpace(h.Mimic.XDPMode))
		h.Mimic.Path = strings.TrimSpace(h.Mimic.Path)
		args := []string{}
		for _, a := range h.Mimic.ExtraArgs {
			if a = strings.TrimSpace(a); a != "" {
				args = append(args, a)
			}
		}
		h.Mimic.ExtraArgs = args
	}
	h.ListenPortRange = strings.TrimSpace(h.ListenPortRange)
	h.TrafficListen = strings.TrimSpace(h.TrafficListen)
	h.ACLFile = strings.TrimSpace(h.ACLFile)
	h.GeoIP = strings.TrimSpace(h.GeoIP)
	h.GeoSite = strings.TrimSpace(h.GeoSite)
	h.GeoUpdateInterval = strings.TrimSpace(h.GeoUpdateInterval)
	h.UDPIdleTimeout = strings.TrimSpace(h.UDPIdleTimeout)
	h.ClientCA = strings.TrimSpace(h.ClientCA)
	h.ExtraYAML = strings.TrimSpace(h.ExtraYAML)
	return h, nil
}
