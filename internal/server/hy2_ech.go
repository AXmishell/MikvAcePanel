package server

import "github.com/axmishell/mikvacepanel/internal/app/nodes"

func parseECHConfigPEM(v string) (string, error) { return nodes.ParseECHConfigPEM(v) }
