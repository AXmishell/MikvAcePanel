package server

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/hy2config"
	"github.com/axmishell/mikvacepanel/internal/model"
)

var (
	realmNamePattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{5,63}$`)
	listenRangePattern = regexp.MustCompile(`^[0-9]{1,5}-[0-9]{1,5}$`)
	sniffPortsPattern  = regexp.MustCompile(`^(all|[0-9]{1,5}(-[0-9]{1,5})?(,[0-9]{1,5}(-[0-9]{1,5})?)*)$`)
	hostOnlyPattern    = regexp.MustCompile(`^([0-9A-Za-z_.-]+|\[[0-9A-Fa-f:]+\])$`)
	versionPattern     = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)
)

// normalizeHy2 trims user input, preserves server-owned ECH material and
// applies conservative defaults.
func normalizeHy2(p *nodePayload, existing *model.Node) (model.Hy2Settings, error) {
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
		cfg, err := parseECHConfigPEM(h.ECHPEM)
		if err != nil {
			return h, err
		}
		h.ECHConfig = cfg
	} else if existing != nil {
		h.ECHPEM = existing.Hy2.ECHPEM
		h.ECHConfig = existing.Hy2.ECHConfig
	}
	if h.ECHPEM != "" && h.ECHConfig == "" {
		cfg, err := parseECHConfigPEM(h.ECHPEM)
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

func validateHy2(h *model.Hy2Settings, existing *model.Node, tlsMode, portRange, obfs string, cores []string, inbounds []model.XrayInbound) error {
	// QUIC windows.
	if q := h.QUIC; q != nil {
		if err := checkOptionalDuration(q.MaxIdleTimeout, "quic.maxIdleTimeout"); err != nil {
			return err
		}
		for name, v := range map[string]int{
			"quic.initStreamReceiveWindow": q.InitStreamReceiveWindow,
			"quic.maxStreamReceiveWindow":  q.MaxStreamReceiveWindow,
			"quic.initConnReceiveWindow":   q.InitConnReceiveWindow,
			"quic.maxConnReceiveWindow":    q.MaxConnReceiveWindow,
		} {
			if v < 0 || v > 1<<30 {
				return fmt.Errorf("%s must be between 0 and 1073741824", name)
			}
		}
		if q.InitStreamReceiveWindow > 0 && q.MaxStreamReceiveWindow > 0 && q.InitStreamReceiveWindow > q.MaxStreamReceiveWindow {
			return fmt.Errorf("quic.initStreamReceiveWindow cannot exceed maxStreamReceiveWindow")
		}
		if q.InitConnReceiveWindow > 0 && q.MaxConnReceiveWindow > 0 && q.InitConnReceiveWindow > q.MaxConnReceiveWindow {
			return fmt.Errorf("quic.initConnReceiveWindow cannot exceed maxConnReceiveWindow")
		}
		if q.MaxIncomingStreams < 0 || q.MaxIncomingStreams > 1<<20 {
			return fmt.Errorf("quic.maxIncomingStreams must be between 0 and 1048576")
		}
	}
	if c := h.Congestion; c != nil && c.Type != "" {
		if c.Type != "bbr" && c.Type != "reno" {
			return fmt.Errorf("congestion.type must be bbr or reno")
		}
		if c.BBRProfile != "" {
			if c.Type != "bbr" {
				return fmt.Errorf("congestion.bbrProfile only applies to bbr")
			}
			switch c.BBRProfile {
			case "standard", "conservative", "aggressive":
			default:
				return fmt.Errorf("congestion.bbrProfile must be standard, conservative or aggressive")
			}
		}
		if err := requireVersion(existing, "2.8.0", "congestion control"); err != nil {
			return err
		}
	}
	if h.DisableLossCompensation {
		if err := requireVersion(existing, "2.10.0", "bandwidth.disableLossCompensation"); err != nil {
			return err
		}
	}
	if h.Resolver != nil && h.Resolver.Type != "" {
		r := h.Resolver
		switch r.Type {
		case "udp", "tcp", "tls", "https":
		default:
			return fmt.Errorf("resolver.type must be udp, tcp, tls or https")
		}
		if r.Addr == "" {
			return fmt.Errorf("resolver.addr is required")
		}
		if err := checkOptionalDuration(r.Timeout, "resolver.timeout"); err != nil {
			return err
		}
		if r.SNI != "" && r.Type != "tls" && r.Type != "https" {
			return fmt.Errorf("resolver.sni only applies to tls or https")
		}
	}
	if s := h.Sniff; s != nil {
		if err := checkOptionalDuration(s.Timeout, "sniff.timeout"); err != nil {
			return err
		}
		for _, ports := range []struct{ name, value string }{
			{"sniff.tcpPorts", s.TCPPorts}, {"sniff.udpPorts", s.UDPPorts},
		} {
			if ports.value != "" && !sniffPortsPattern.MatchString(ports.value) {
				return fmt.Errorf("%s must be a comma separated port list or all", ports.name)
			}
		}
	}
	if err := validateMasquerade(h.Masquerade, cores, inbounds); err != nil {
		return err
	}
	if a := h.ACME; a != nil {
		switch a.Type {
		case "", "http", "tls", "dns":
		default:
			return fmt.Errorf("acme.type must be http, tls or dns")
		}
		if a.CA != "" && a.CA != "letsencrypt" && a.CA != "zerossl" {
			return fmt.Errorf("acme.ca must be letsencrypt or zerossl")
		}
		if a.HTTPPort < 0 || a.HTTPPort > 65535 || a.TLSPort < 0 || a.TLSPort > 65535 {
			return fmt.Errorf("ACME ports must be between 1 and 65535")
		}
		if a.Type == "dns" {
			if a.DNSName == "" || len(a.DNSConfig) == 0 {
				return fmt.Errorf("acme dns requires a provider name and config")
			}
		}
	}
	if h.TLS != nil && h.TLS.SNIGuard != "" {
		switch h.TLS.SNIGuard {
		case "strict", "disable", "dns-san":
		default:
			return fmt.Errorf("tls.sniGuard must be strict, disable or dns-san")
		}
	}
	if h.Direct != nil {
		d := h.Direct
		if d.Mode != "" {
			switch d.Mode {
			case "auto", "4", "6", "46", "64":
			default:
				return fmt.Errorf("direct.mode must be auto, 4, 6, 46 or 64")
			}
		}
		if d.BindDevice != "" && (d.BindIPv4 != "" || d.BindIPv6 != "") {
			return fmt.Errorf("direct.bindDevice cannot be combined with bindIPv4/bindIPv6")
		}
	}
	if h.ListenPortRange != "" {
		if !listenRangePattern.MatchString(h.ListenPortRange) {
			return fmt.Errorf("listen port range must look like 20000-50000")
		}
		if err := validatePortRange(h.ListenPortRange); err != nil {
			return err
		}
		if err := requireVersion(existing, "2.8.0", "built-in listen port range"); err != nil {
			return err
		}
		if portRange != "" {
			return fmt.Errorf("built-in listen port range and agent port hopping are mutually exclusive")
		}
	}
	// Subscription endpoint toggles. Without at least one endpoint a Hysteria
	// node would silently disappear from every subscription.
	if slices.Contains(cores, "hysteria") && !h.IsRealm() && !(h.Mimic != nil && h.Mimic.Enabled) {
		if !h.PublishesTLS() && !h.PublishesHopping() {
			return fmt.Errorf("at least one Hysteria subscription endpoint (TLS or Hopping) must be enabled")
		}
		if !h.PublishesTLS() && portRange == "" && h.ListenPortRange == "" {
			return fmt.Errorf("Hopping-only subscriptions require a port range")
		}
	}
	if h.TrafficListen != "" {
		if err := validateListenAddress(h.TrafficListen, true, "trafficStats.listen"); err != nil {
			return err
		}
	}
	if h.GeoUpdateInterval != "" {
		if err := checkOptionalDuration(h.GeoUpdateInterval, "acl.geoUpdateInterval"); err != nil {
			return err
		}
	}
	if h.UDPIdleTimeout != "" {
		if err := checkOptionalDuration(h.UDPIdleTimeout, "udpIdleTimeout"); err != nil {
			return err
		}
	}
	if obfs == "gecko" {
		min, max := h.GeckoMinPacketSize, h.GeckoMaxPacketSize
		if min != 0 && (min < 512 || min > 2048) {
			return fmt.Errorf("gecko minPacketSize must be between 512 and 2048")
		}
		if max != 0 && (max < 512 || max > 2048) {
			return fmt.Errorf("gecko maxPacketSize must be between 512 and 2048")
		}
		if min != 0 && max != 0 && min > max {
			return fmt.Errorf("gecko minPacketSize cannot exceed maxPacketSize")
		}
		if min != 0 || max != 0 {
			if err := requireVersion(existing, "2.9.2", "gecko packet sizing"); err != nil {
				return err
			}
		}
	}
	if h.IsRealm() {
		if err := validateRealm(h.Realm); err != nil {
			return err
		}
		if err := requireVersion(existing, "2.9.0", "Hysteria Realms"); err != nil {
			return err
		}
		if portRange != "" || h.ListenPortRange != "" {
			return fmt.Errorf("Realms mode cannot be combined with port hopping")
		}
	}
	if h.Mimic != nil && h.Mimic.Enabled {
		if h.Mimic.XDPMode != "" && h.Mimic.XDPMode != "native" && h.Mimic.XDPMode != "skb" {
			return fmt.Errorf("mimic.xdpMode must be native or skb")
		}
		if portRange != "" || h.ListenPortRange != "" {
			return fmt.Errorf("mimic cannot be combined with port hopping")
		}
		if err := requireVersion(existing, "2.12.0", "mimic"); err != nil {
			return err
		}
	}
	if h.ECHConfig != "" {
		if err := requireVersion(existing, "2.10.0", "ECH"); err != nil {
			return err
		}
	}
	if h.ClientCA != "" {
		if strings.EqualFold(tlsMode, "acme") {
			return fmt.Errorf("client CA (mTLS) cannot be combined with node-side ACME")
		}
		if !strings.Contains(h.ClientCA, "BEGIN CERTIFICATE") {
			return fmt.Errorf("client CA must be a PEM certificate bundle")
		}
	}
	if err := hy2config.ValidateExtraYAML(h.ExtraYAML); err != nil {
		return err
	}
	return nil
}

func validateMasquerade(m *model.Hy2Masquerade, cores []string, inbounds []model.XrayInbound) error {
	if m == nil || m.Type == "" {
		if m != nil && (m.ListenHTTP != "" || m.ListenHTTPS != "") {
			return fmt.Errorf("masquerade.listenHTTP/listenHTTPS require a masquerade type")
		}
		return nil
	}
	switch m.Type {
	case "proxy":
		if m.ProxyURL == "" {
			return fmt.Errorf("masquerade proxy URL is required")
		}
	case "file":
		if m.FileDir == "" {
			return fmt.Errorf("masquerade file directory is required")
		}
	case "string":
		if m.Content == "" {
			return fmt.Errorf("masquerade string content is required")
		}
		if m.StatusCode != 0 && (m.StatusCode < 100 || m.StatusCode > 599) {
			return fmt.Errorf("masquerade statusCode must be a valid HTTP status")
		}
	default:
		return fmt.Errorf("masquerade.type must be proxy, file or string")
	}
	used := map[int]string{}
	xrayEnabled := false
	for _, core := range cores {
		if core == "xray" {
			xrayEnabled = true
			break
		}
	}
	if !xrayEnabled {
		// Masquerade only exists on the Hysteria core. Still validate the
		// field values for pure-Xray nodes, but their inbound ports cannot
		// conflict with a Hysteria listener.
		inbounds = nil
	}
	for _, ib := range inbounds {
		if ib.IsEnabled() && ib.Port > 0 {
			used[ib.Port] = fmt.Sprintf("Xray inbound %s", ib.Tag)
		}
	}
	for _, item := range []struct{ name, value string }{
		{"masquerade.listenHTTP", m.ListenHTTP},
		{"masquerade.listenHTTPS", m.ListenHTTPS},
	} {
		if item.value == "" {
			continue
		}
		port, err := listenAddressPort(item.value)
		if err != nil {
			return fmt.Errorf("%s: %w", item.name, err)
		}
		if other, ok := used[port]; ok {
			return fmt.Errorf("%s port %d conflicts with %s", item.name, port, other)
		}
		used[port] = item.name
	}
	return nil
}

func validateRealm(r *model.Hy2Realm) error {
	u, err := url.Parse(r.URI)
	if err != nil {
		return fmt.Errorf("realm URI is invalid: %w", err)
	}
	if u.Scheme != "realm" && u.Scheme != "realm+http" {
		return fmt.Errorf("realm URI must start with realm:// or realm+http://")
	}
	if u.Host == "" {
		return fmt.Errorf("realm URI must include a rendezvous host")
	}
	name := strings.TrimPrefix(u.Path, "/")
	if !realmNamePattern.MatchString(name) {
		return fmt.Errorf("realm name must be 6-64 characters of letters, digits, - or _")
	}
	if r.IPMode != "" && r.IPMode != "dual" && r.IPMode != "v4" && r.IPMode != "v6" {
		return fmt.Errorf("realm.ipMode must be dual, v4 or v6")
	}
	for _, d := range []struct{ name, value string }{
		{"realm.stunTimeout", r.STUNTimeout},
		{"realm.punchTimeout", r.PunchTimeout},
		{"realm.heartbeatInterval", r.HeartbeatInterval},
		{"realm.portMapping.timeout", r.PortMapTimeout},
		{"realm.portMapping.lifetime", r.PortMapLifetime},
	} {
		if err := checkOptionalDuration(d.value, d.name); err != nil {
			return err
		}
	}
	return nil
}

func checkOptionalDuration(v, field string) error {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	if _, err := time.ParseDuration(v); err != nil {
		return fmt.Errorf("%s must be a duration such as 30s or 1m", field)
	}
	return nil
}

func validateListenAddress(v string, allowBareHost bool, field string) error {
	if strings.Contains(v, ":") {
		if _, err := listenAddressPort(v); err != nil {
			return fmt.Errorf("%s: %w", field, err)
		}
		return nil
	}
	if allowBareHost && hostOnlyPattern.MatchString(v) {
		return nil
	}
	return fmt.Errorf("%s must be a host or host:port", field)
}

func listenAddressPort(v string) (int, error) {
	host, portStr, err := net.SplitHostPort(v)
	if err != nil {
		if strings.HasPrefix(v, ":") {
			host, portStr = "", strings.TrimPrefix(v, ":")
		} else {
			return 0, fmt.Errorf("invalid listen address %q", v)
		}
	}
	if host != "" && net.ParseIP(host) == nil && !hostOnlyPattern.MatchString(host) {
		return 0, fmt.Errorf("invalid listen host %q", host)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid listen port %q", portStr)
	}
	return port, nil
}

func requireVersion(existing *model.Node, want, feature string) error {
	if existing == nil || existing.Hy2Version == "" || strings.EqualFold(existing.Hy2Version, "unknown") {
		return nil
	}
	have := existing.Hy2Version
	hm := versionPattern.FindStringSubmatch(have)
	if len(hm) != 4 {
		return nil
	}
	wm := versionPattern.FindStringSubmatch(want)
	if len(wm) != 4 {
		return nil
	}
	for i := 1; i <= 3; i++ {
		a, _ := strconv.Atoi(hm[i])
		b, _ := strconv.Atoi(wm[i])
		if a > b {
			return nil
		}
		if a < b {
			return fmt.Errorf("%s requires Hysteria %s or newer (node reports %s)", feature, want, have)
		}
	}
	return nil
}

// parseECHConfigPEM validates an ech.pem produced by `hysteria ech` and
// returns the base64 ECHConfigList clients must use.
func parseECHConfigPEM(v string) (string, error) {
	rest := []byte(v)
	var list []byte
	haveKeys := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "ECH KEYS":
			haveKeys = len(block.Bytes) > 0
		case "ECH CONFIGS":
			if err := validateECHConfigList(block.Bytes); err != nil {
				return "", err
			}
			list = block.Bytes
		}
	}
	if !haveKeys || len(list) == 0 {
		return "", fmt.Errorf("ECH PEM must contain both ECH KEYS and ECH CONFIGS blocks")
	}
	return base64.StdEncoding.EncodeToString(list), nil
}

func validateECHConfigList(list []byte) error {
	if len(list) < 2 {
		return fmt.Errorf("malformed ECH config list")
	}
	n := int(binary.BigEndian.Uint16(list[:2]))
	body := list[2:]
	if n != len(body) {
		return fmt.Errorf("malformed ECH config list length")
	}
	count := 0
	for len(body) > 0 {
		if len(body) < 4 {
			return fmt.Errorf("malformed ECH config list")
		}
		l := int(binary.BigEndian.Uint16(body[2:4]))
		if len(body) < 4+l {
			return fmt.Errorf("malformed ECH config list")
		}
		body = body[4+l:]
		count++
	}
	if count == 0 {
		return fmt.Errorf("malformed ECH config list")
	}
	return nil
}
