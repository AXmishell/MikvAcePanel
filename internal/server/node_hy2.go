package server

import (
	"github.com/axmishell/mikvacepanel/internal/app/nodes"
	"github.com/axmishell/mikvacepanel/internal/model"
)

// Compatibility wrappers keep the existing server tests and handlers stable
// while the validation implementation lives in the nodes application package.

func applyHy2SubscriptionDefaults(h *model.Hy2Settings, mode string) {
	nodes.ApplyHy2SubscriptionDefaults(h, mode)
}

func normalizeHy2(p *nodePayload, existing *model.Node) (model.Hy2Settings, error) {
	return nodes.NormalizeHy2(p.input(), existing)
}

func validateHy2(h *model.Hy2Settings, existing *model.Node, tlsMode, portRange, obfs string, cores []string, inbounds []model.XrayInbound) error {
	return nodes.ValidateHy2(h, existing, tlsMode, portRange, obfs, cores, inbounds)
}

func validateMasquerade(m *model.Hy2Masquerade, cores []string, inbounds []model.XrayInbound) error {
	return nodes.ValidateMasquerade(m, cores, inbounds)
}
