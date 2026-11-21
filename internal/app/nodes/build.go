package nodes

import (
	"fmt"
	"strings"

	"github.com/axmishell/mikvacepanel/internal/idgen"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/xrayconfig"
)

func Build(p *Input, existing *model.Node) (*model.Node, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("node name is required")
	}
	nodeType := strings.ToLower(strings.TrimSpace(p.Type))
	if nodeType == "" {
		if len(p.Cores) > 0 {
			nodeType = strings.ToLower(strings.TrimSpace(p.Cores[0]))
		} else if existing != nil && existing.Type != "" {
			nodeType = existing.Type
		} else {
			nodeType = "hysteria"
		}
	}
	if nodeType != "hysteria" && nodeType != "xray" {
		return nil, fmt.Errorf("node type must be hysteria or xray")
	}
	cores := []string{}
	seenCores := map[string]bool{}
	for _, core := range p.Cores {
		core = strings.ToLower(strings.TrimSpace(core))
		if core == "" || seenCores[core] {
			continue
		}
		if !model.ValidCore(core) {
			return nil, fmt.Errorf("unsupported core %q", core)
		}
		seenCores[core] = true
		cores = append(cores, core)
	}
	if len(cores) == 0 {
		cores = []string{nodeType}
	}
	// The first core is the node's primary type; it keeps legacy endpoints and
	// the Type column meaningful while Cores drives multi-core rendering.
	nodeType = cores[0]
	listen, port, err := NormalizeNodeListen(p.Listen, p.Port)
	if err != nil {
		return nil, err
	}
	tlsMode := strings.ToLower(strings.TrimSpace(p.TLSMode))
	if tlsMode == "" {
		tlsMode = "selfsigned"
	}
	if tlsMode != "selfsigned" && tlsMode != "acme" && tlsMode != "managed" {
		return nil, fmt.Errorf("tlsMode must be selfsigned, acme or managed")
	}
	domain := strings.TrimSpace(p.Domain)
	if (tlsMode == "acme" || tlsMode == "managed") && domain == "" {
		return nil, fmt.Errorf("ACME or panel-issued certificates require a domain")
	}
	portRange := strings.TrimSpace(p.PortRange)
	if portRange != "" {
		if !portRangePattern.MatchString(portRange) {
			return nil, fmt.Errorf("port range must look like 20000-50000")
		}
		if err := ValidatePortRange(portRange); err != nil {
			return nil, err
		}
	}
	obfs := strings.ToLower(strings.TrimSpace(p.ObfsType))
	if obfs != "" && obfs != "salamander" && obfs != "gecko" {
		return nil, fmt.Errorf("obfs type must be salamander or gecko")
	}
	if obfs != "" && strings.TrimSpace(p.ObfsPassword) == "" {
		return nil, fmt.Errorf("obfs password is required when obfuscation is enabled")
	}
	statsPort := p.StatsPort
	if statsPort == 0 {
		statsPort = 9999
	}
	if statsPort < 1 || statsPort > 65535 {
		return nil, fmt.Errorf("stats port must be between 1 and 65535")
	}
	ranking := p.Ranking
	if ranking == 0 {
		ranking = 1
	}
	active := true
	if p.Active != nil {
		active = *p.Active
	}
	autoRestart := true
	if existing != nil {
		autoRestart = existing.AutoRestart
	}
	if p.AutoRestart != nil {
		autoRestart = *p.AutoRestart
	}
	acl := []string{}
	for _, line := range p.ACL {
		line = strings.TrimSpace(line)
		if line != "" {
			acl = append(acl, line)
		}
	}
	if len(acl) > 500 {
		return nil, fmt.Errorf("too many ACL rules (max 500)")
	}
	outbounds := []model.Outbound{}
	for _, o := range p.Outbounds {
		o.Name = strings.TrimSpace(o.Name)
		o.Type = strings.ToLower(strings.TrimSpace(o.Type))
		if o.Name == "" {
			continue
		}
		if o.Type != "direct" && o.Type != "socks5" && o.Type != "http" {
			return nil, fmt.Errorf("outbound %q has unsupported type", o.Name)
		}
		outbounds = append(outbounds, o)
	}
	hy2, err := NormalizeHy2(p, existing)
	if err != nil {
		return nil, err
	}
	if err := ValidateHy2(&hy2, existing, tlsMode, portRange, obfs, cores, p.Xray.Inbounds); err != nil {
		return nil, err
	}
	sshPort := p.SSHPort
	if sshPort == 0 {
		sshPort = 22
	}
	if sshPort < 1 || sshPort > 65535 {
		return nil, fmt.Errorf("SSH port must be between 1 and 65535")
	}
	runtimeMode := strings.ToLower(strings.TrimSpace(p.RuntimeMode))
	if runtimeMode == "" {
		if existing != nil && existing.RuntimeMode != "" {
			runtimeMode = existing.RuntimeMode
		} else {
			runtimeMode = "process"
		}
	}
	if runtimeMode != "process" && runtimeMode != "docker" {
		return nil, fmt.Errorf("runtimeMode must be process or docker")
	}
	n := &model.Node{
		Name: name, Type: nodeType, Flag: strings.TrimSpace(p.Flag),
		IP: strings.TrimSpace(p.IP), Domain: domain, SNI: strings.TrimSpace(p.SNI),
		Listen: listen, Port: port, PortRange: portRange,
		HopInterval: strings.TrimSpace(p.HopInterval), ObfsType: obfs, ObfsPassword: p.ObfsPassword,
		StatsPort: statsPort, SSHPort: sshPort, SSHUser: strings.TrimSpace(p.SSHUser),
		SSHPassword: p.SSHPassword, SSHKey: p.SSHKey, SSHHostKey: strings.TrimSpace(p.SSHHostKey),
		SSHInstallDocker: p.SSHInstallDocker,
		TLSMode:          tlsMode, ACMEEmail: strings.TrimSpace(p.ACMEEmail),
		BandwidthUp: strings.TrimSpace(p.BandwidthUp), BandwidthDown: strings.TrimSpace(p.BandwidthDown),
		MasqueradeURL: strings.TrimSpace(p.MasqueradeURL), ACL: acl, Outbounds: outbounds,
		Xray: p.Xray, Hy2: hy2, Cores: cores, MaxOnline: p.MaxOnline, Ranking: ranking, Active: active,
		AutoRestart: autoRestart, RuntimeMode: runtimeMode,
	}
	if n.HasCore("xray") {
		if existing != nil && existing.HasCore("xray") {
			if n.Xray.RealityPrivateKey == "" && existing.Xray.RealityPrivateKey != "" {
				n.Xray.RealityPrivateKey = existing.Xray.RealityPrivateKey
				n.Xray.RealityPublicKey = existing.Xray.RealityPublicKey
			}
			if len(n.Xray.RealityShortIDs) == 0 {
				n.Xray.RealityShortIDs = existing.Xray.RealityShortIDs
			}
			// Preserve REALITY material of inbounds that were left unchanged.
			for i := range n.Xray.Inbounds {
				for _, old := range existing.Xray.EffectiveInbounds() {
					if old.Tag == n.Xray.Inbounds[i].Tag && n.Xray.Inbounds[i].RealityPrivateKey == "" {
						n.Xray.Inbounds[i].RealityPrivateKey = old.RealityPrivateKey
						n.Xray.Inbounds[i].RealityPublicKey = old.RealityPublicKey
						n.Xray.Inbounds[i].RealityShortIDs = old.RealityShortIDs
					}
				}
			}
		}
		if err := xrayconfig.Normalize(n); err != nil {
			return nil, err
		}
		if n.Xray.Security == "tls" && n.TLSMode == "acme" && len(cores) == 1 {
			// A pure-Xray node has no built-in ACME in this panel; panel-issued
			// managed certificates are the supported option.
			n.TLSMode = "selfsigned"
		}
		if len(cores) == 1 {
			n.Port = n.Xray.Inbounds[0].Port
		}
	}
	if existing != nil {
		n.ID = existing.ID
		n.StatsSecret = existing.StatsSecret
		n.Token = existing.Token
		if n.SSHPassword == "" {
			n.SSHPassword = existing.SSHPassword
		}
		if n.SSHKey == "" {
			n.SSHKey = existing.SSHKey
		}
		if n.SSHHostKey == "" {
			n.SSHHostKey = existing.SSHHostKey
		}
	}
	if n.StatsSecret == "" {
		n.StatsSecret = idgen.Token(16)
	}
	return n, nil
}
