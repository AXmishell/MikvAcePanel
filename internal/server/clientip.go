package server

import (
	"net"
	"net/http"
	"strings"
)

// parseTrustedProxies converts IP/CIDR strings from the configuration into
// networks. Invalid entries are ignored so a typo cannot make every address
// trusted.
func parseTrustedProxies(values []string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(values))
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if strings.Contains(raw, "/") {
			_, network, err := net.ParseCIDR(raw)
			if err == nil {
				out = append(out, network)
			}
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			continue
		}
		bits := 128
		if ip.To4() != nil {
			ip = ip.To4()
			bits = 32
		}
		out = append(out, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	return out
}

func (s *Server) isTrustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIP returns the address used for rate limiting and audit logs.
//
// Forwarded headers are only honoured when the direct peer is a configured
// trusted proxy. Without that check any client could rotate
// X-Forwarded-For and bypass the login limiter.
func (s *Server) clientIP(r *http.Request) string {
	remote := remoteHost(r.RemoteAddr)
	remoteIP := net.ParseIP(remote)
	if remoteIP == nil || !s.isTrustedProxy(remoteIP) {
		return remote
	}

	var hops []string
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		for _, part := range strings.Split(xff, ",") {
			if hop := strings.TrimSpace(part); hop != "" {
				hops = append(hops, hop)
			}
		}
	}
	if len(hops) == 0 {
		if xri := strings.TrimSpace(r.Header.Get("X-Real-IP")); xri != "" {
			hops = []string{xri}
		}
	}
	if len(hops) == 0 {
		return remote
	}
	// Walk from the nearest hop to the farthest and return the first address
	// that is not itself one of our proxies. This is the standard behaviour
	// recommended by reverse proxies.
	for i := len(hops) - 1; i >= 0; i-- {
		ip := net.ParseIP(hops[i])
		if ip == nil {
			// Malformed chain: fall back to the peer so all traffic from this
			// proxy shares one bucket instead of trusting attacker input.
			return remote
		}
		if !s.isTrustedProxy(ip) {
			return ip.String()
		}
	}
	return hops[0]
}

func remoteHost(addr string) string {
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		return host
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip.String()
	}
	return addr
}
