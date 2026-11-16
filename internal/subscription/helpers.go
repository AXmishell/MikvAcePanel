package subscription

import (
	"strings"

	"github.com/axmishell/mikvacepanel/internal/model"
)

func hasManagedCert(n model.Node) bool {
	return strings.EqualFold(n.TLSMode, "managed") && n.Domain != ""
}

func endpointLabel(n model.Node) string {
	return strings.TrimSpace(strings.Join([]string{n.Flag, n.Name}, " "))
}

func firstOf(vals []string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
