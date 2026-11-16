package server

import (
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"

	webui "github.com/axmishell/mikvacepanel/web"
)

func (s *Server) serveRoot(w http.ResponseWriter, r *http.Request) {
	if n, err := s.store.CountAdmins(r.Context()); err == nil && n == 0 {
		http.Redirect(w, r, "/install", http.StatusFound)
		return
	}
	s.serveNotFound(w, r)
}

// serveInstall keeps the first-run wizard available only while no
// administrator exists. Afterwards it returns the SPA shell with a 404 status
// so the client-side NotFound view renders and the endpoint is not advertised.
func (s *Server) serveInstall(w http.ResponseWriter, r *http.Request) {
	if n, err := s.store.CountAdmins(r.Context()); err == nil && n > 0 {
		s.writeIndex(w, http.StatusNotFound)
		return
	}
	s.serveSPA(w, r)
}

// serveNotFound returns the SPA shell with a 404 status so the client-side
// NotFound view renders without advertising the endpoint. It is used for the
// installed site root and retired routes such as the legacy /panel prefix.
func (s *Server) serveNotFound(w http.ResponseWriter, _ *http.Request) {
	s.writeIndex(w, http.StatusNotFound)
}

func (s *Server) serveSPA(w http.ResponseWriter, r *http.Request) {
	// Never answer unknown API paths with the SPA shell.
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	dist := webui.FS()
	upath := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if upath == "" || upath == "." || upath == "index.html" {
		upath = "index.html"
	}
	if upath != "index.html" {
		b, err := fs.ReadFile(dist, upath)
		if err == nil && !strings.HasSuffix(upath, "/") {
			ct := mime.TypeByExtension(path.Ext(upath))
			if ct == "" {
				ct = "application/octet-stream"
			}
			w.Header().Set("Content-Type", ct)
			if strings.HasPrefix(upath, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			_, _ = w.Write(b)
			return
		}
		// Missing static files should 404 instead of returning the SPA shell.
		if path.Ext(upath) != "" {
			http.NotFound(w, r)
			return
		}
	}
	s.writeIndex(w, http.StatusOK)
}

// writeIndex serves the SPA shell with the given status. The installer uses
// 404 after setup while still letting the Vue NotFound view render.
func (s *Server) writeIndex(w http.ResponseWriter, status int) {
	b, err := fs.ReadFile(webui.FS(), "index.html")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "frontend is not built")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}
