package server

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// normalizeNodeListen validates the node-wide Hysteria listen address and
// reconciles it with the node port. Both values are stored on the node, and
// the Hysteria config prefers the listen address while subscriptions use the
// port, so silently accepting different ports would publish a dead link.
//
// Accepted forms:
//
//	""                 -> use the node port
//	"15443"            -> ":15443" (common data-entry mistake)
//	":15443"           -> ":15443"
//	"0.0.0.0:15443"    -> unchanged
//	"[::]:15443"       -> unchanged
//	"0.0.0.0"          -> "0.0.0.0:15443" (bare host gets the node port)
//
// A port supplied in the listen address must match the node port. When the
// node port is omitted (0), the listen port becomes the node port.
func normalizeNodeListen(raw string, port int) (string, int, error) {
	raw = strings.TrimSpace(raw)
	if port != 0 && (port < 1 || port > 65535) {
		return "", 0, fmt.Errorf("port must be between 1 and 65535")
	}
	if raw == "" {
		if port == 0 {
			port = 443
		}
		return "", port, nil
	}

	// Full listen address: :443, 0.0.0.0:443, [::]:443.
	if host, portStr, err := net.SplitHostPort(raw); err == nil {
		p, perr := strconv.Atoi(portStr)
		if perr != nil || p < 1 || p > 65535 {
			return "", 0, fmt.Errorf("listen address %q has an invalid port", raw)
		}
		if host != "" && net.ParseIP(host) == nil && !hostOnlyPattern.MatchString(host) {
			return "", 0, fmt.Errorf("listen address %q has an invalid host", raw)
		}
		if port != 0 && port != p {
			return "", 0, listenPortConflictError(p, port)
		}
		return net.JoinHostPort(host, strconv.Itoa(p)), p, nil
	}

	// A bare port is a common data-entry mistake: "15443" -> ":15443".
	if isAllDigits(raw) {
		p, err := strconv.Atoi(raw)
		if err != nil || p < 1 || p > 65535 {
			return "", 0, fmt.Errorf("listen address %q has an invalid port", raw)
		}
		if port != 0 && port != p {
			return "", 0, listenPortConflictError(p, port)
		}
		return ":" + strconv.Itoa(p), p, nil
	}

	// A bare host: append the node port so Hysteria can bind it.
	host := raw
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	if net.ParseIP(host) == nil && !hostOnlyPattern.MatchString(host) {
		return "", 0, fmt.Errorf("listen address %q must be a host, host:port or :port", raw)
	}
	if port == 0 {
		port = 443
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), port, nil
}

func listenPortConflictError(listenPort, nodePort int) error {
	return fmt.Errorf("listen address port %d conflicts with node port %d; clear the listen address or use the same port for both", listenPort, nodePort)
}

func isAllDigits(v string) bool {
	if v == "" {
		return false
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
