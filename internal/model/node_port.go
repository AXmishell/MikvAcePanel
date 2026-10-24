package model

import (
	"net"
	"strconv"
	"strings"
)

// EffectiveListenPort returns the UDP port the Hysteria core actually binds for
// this node. The panel stores the node-wide port, an optional listen address
// and an optional built-in port range, so every consumer (subscriptions, agent
// port hopping, dashboards) must derive the port with the same precedence as
// hy2config.Build:
//
//	Realms URI > built-in listen port range > listen address port > node port
//
// Hysteria's built-in range binds the lowest port of the normalized range and
// redirects the remaining ports to it, so the lowest start is the usable port.
func (n *Node) EffectiveListenPort() int {
	if n == nil {
		return 443
	}
	if !n.Hy2.IsRealm() {
		if port := rangeStartPort(n.Hy2.ListenPortRange); port > 0 {
			return port
		}
		if port := listenAddressPort(n.Listen); port > 0 {
			return port
		}
	}
	if n.Port >= 1 && n.Port <= 65535 {
		return n.Port
	}
	return 443
}

// ListenHost returns the host part of the node-wide listen address. Xray's
// inbound listen field takes a host only, while the Hysteria listen address
// carries host:port, so inbounds that inherit the node listen must drop the
// port. Legacy bare hosts are returned unchanged.
func (n *Node) ListenHost() string {
	if n == nil {
		return ""
	}
	v := strings.TrimSpace(n.Listen)
	if v == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(v); err == nil {
		host = strings.TrimSpace(host)
		if host == "" || validHost(host) {
			return host
		}
		return ""
	}
	// A bare IPv6 address may be bracketed or not.
	unbracketed := strings.TrimSuffix(strings.TrimPrefix(v, "["), "]")
	if net.ParseIP(unbracketed) != nil {
		return unbracketed
	}
	// Legacy values may be a bare hostname without a port.
	if validHost(v) {
		return v
	}
	return ""
}

func validHost(v string) bool {
	if v == "" {
		return false
	}
	if net.ParseIP(v) != nil {
		return true
	}
	if len(v) > 253 {
		return false
	}
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '-', r == '_':
		default:
			return false
		}
	}
	return true
}

// listenAddressPort extracts the port from values such as ":443",
// "0.0.0.0:15443" or "[::]:15443". It also accepts a legacy bare port.
func listenAddressPort(v string) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if _, portStr, err := net.SplitHostPort(v); err == nil {
		if p := validPort(portStr); p > 0 {
			return p
		}
		// Legacy rows may contain a range directly in the listen address.
		return rangeStartPort(portStr)
	}
	return validPort(v)
}

func validPort(v string) int {
	p, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || p < 1 || p > 65535 {
		return 0
	}
	return p
}

func rangeStartPort(v string) int {
	best := 0
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		start := part
		if i := strings.IndexByte(part, '-'); i >= 0 {
			start = strings.TrimSpace(part[:i])
		}
		p := validPort(start)
		if p == 0 {
			continue
		}
		if best == 0 || p < best {
			best = p
		}
	}
	return best
}
