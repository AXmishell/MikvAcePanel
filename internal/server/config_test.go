package server

import "testing"

func TestLoadConfigSeparation(t *testing.T) {
	t.Setenv("SERVE_WEB", "false")
	t.Setenv("FRONTEND_URL", "https://panel.example.com/")
	cfg := LoadConfig()
	if cfg.ServeWeb {
		t.Fatal("ServeWeb should be false")
	}
	if cfg.FrontendURL != "https://panel.example.com" {
		t.Fatalf("FrontendURL = %q", cfg.FrontendURL)
	}

	t.Setenv("SERVE_WEB", "true")
	cfg = LoadConfig()
	if !cfg.ServeWeb {
		t.Fatal("ServeWeb should default to true")
	}
}
