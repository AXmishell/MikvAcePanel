package subscription

import (
	"fmt"

	"github.com/axmishell/mikvacepanel/internal/model"
)

// Renderer turns a user and node set into one subscription format.
type Renderer interface {
	Format() string
	ContentType() string
	Render(u *model.User, nodes []model.Node) (string, error)
}

type renderer struct {
	format      string
	contentType string
	render      func(*model.User, []model.Node) (string, error)
}

func (r renderer) Format() string      { return r.format }
func (r renderer) ContentType() string { return r.contentType }
func (r renderer) Render(u *model.User, nodes []model.Node) (string, error) {
	return r.render(u, nodes)
}

var renderers = map[string]renderer{
	"uri": {
		format: "uri", contentType: "text/plain; charset=utf-8",
		render: func(u *model.User, nodes []model.Node) (string, error) { return URIList(u, nodes), nil },
	},
	"": {
		format: "uri", contentType: "text/plain; charset=utf-8",
		render: func(u *model.User, nodes []model.Node) (string, error) { return URIList(u, nodes), nil },
	},
	"clash": {
		format: "clash", contentType: "text/yaml; charset=utf-8",
		render: Clash,
	},
	"singbox": {
		format: "singbox", contentType: "application/json; charset=utf-8",
		render: Singbox,
	},
	"shadowrocket": {
		format: "shadowrocket", contentType: "text/plain; charset=utf-8",
		render: func(u *model.User, nodes []model.Node) (string, error) { return Shadowrocket(u, nodes), nil },
	},
	"xray-json": {
		format: "xray-json", contentType: "application/json; charset=utf-8",
		render: Xray,
	},
}

// RendererFor resolves a subscription format.
func RendererFor(format string) (Renderer, error) {
	r, ok := renderers[format]
	if !ok {
		return nil, fmt.Errorf("unsupported format: %s", format)
	}
	return r, nil
}

// DefaultRenderer is used for empty or unknown aliases normalised earlier.
func DefaultRenderer() Renderer { return renderers["uri"] }
