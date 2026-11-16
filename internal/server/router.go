package server

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// routes assembles the HTTP surface from focused registration functions. Each
// area owns its middleware boundary so the top-level router stays readable.
func (s *Server) routes() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(s.recoverer)
	r.Use(s.requestLogger)
	r.Use(s.securityHeaders)
	r.Use(s.cors)
	r.Use(s.requireAdminUIEntrance)

	s.registerPublicRoutes(r)
	s.registerAdminRoutes(r)

	if s.cfg.ServeWeb {
		s.registerStaticRoutes(r)
	} else {
		s.registerAPIOnlyRoutes(r)
	}
	return r
}

// registerPublicRoutes contains endpoints that are reachable without an
// administrator session: health, install, login, subscriptions, Hy2 auth and
// the agent WebSocket.
func (s *Server) registerPublicRoutes(r chi.Router) {
	r.Get("/api/health", s.handleHealth)
	r.Get("/api/readyz", s.handleReadyz)
	r.With(noStore).Get("/api/admin/config", s.handleAdminConfig)
	r.With(noStore).Get("/api/install/status", s.handleInstallStatus)
	r.With(noStore).Post("/api/install/database/test", s.handleInstallDatabaseTest)
	r.With(noStore).Post("/api/install/database", s.handleInstallDatabase)
	r.With(noStore).Post("/api/install/import", s.handleInstallImport)
	r.With(noStore).Post("/api/install", s.handleInstall)
	r.With(s.requireAdminEntry).Post("/api/login", s.handleLogin)
	r.With(s.requireAdminEntry).Post("/api/login/2fa", s.handleLogin2FA)
	r.With(noStore, s.requireAdminEntry).Get("/api/login/captcha", s.handleLoginCaptcha)
	r.Post("/api/auth", s.handleHy2Auth)
	r.Get("/api/files/info/{token}", s.handleSubInfo)
	r.Get("/api/files/{token}", s.handleSubscription)
	r.Get("/api/agent/install.sh", s.handleInstallScript)
	r.Get("/api/agent/version", s.handleAgentVersion)
	r.Get("/api/node/ws", s.handleAgentWS)
}

// registerStaticRoutes serves the embedded SPA. The site root intentionally
// returns 404 once installed; the administrator console lives under /admin.
func (s *Server) registerStaticRoutes(r chi.Router) {
	r.Get("/", s.serveRoot)
	// Retired public homepage paths return 404 instead of the SPA shell.
	r.Get("/homepage", s.serveNotFound)
	r.Get("/homepage/*", s.serveNotFound)
	r.Get("/preview/homepage", s.serveNotFound)
	r.Get("/preview/homepage/*", s.serveNotFound)
	r.Get("/install", s.serveInstall)
	// The legacy /panel prefix is intentionally hidden instead of redirecting
	// to /admin, so it behaves like any other unknown route.
	r.Get("/panel", s.serveNotFound)
	r.Get("/panel/*", s.serveNotFound)
	// The self-service user portal has been removed; keep its old paths hidden
	// instead of serving the SPA shell.
	r.Get("/user", s.serveNotFound)
	r.Get("/user/*", s.serveNotFound)
	r.Get("/portal", s.serveNotFound)
	r.Get("/portal/*", s.serveNotFound)
	r.Get("/admin", s.serveAdminSPA)
	r.Get("/admin/*", s.serveAdminSPA)
	r.Get("/assets/*", s.serveSPA)
	r.Get("/favicon.ico", s.serveSPA)
	r.NotFound(s.serveSPA)
}

// registerAPIOnlyRoutes keeps the server usable as a pure API when the
// frontend is deployed separately (for example behind a CDN).
func (s *Server) registerAPIOnlyRoutes(r chi.Router) {
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.FrontendURL != "" {
			http.Redirect(w, r, s.cfg.FrontendURL, http.StatusFound)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "service": "MikvAce Panel API", "frontend": "separate",
		})
	})
	r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
}
