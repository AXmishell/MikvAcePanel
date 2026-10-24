package server

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/axmishell/mikvacepanel/internal/store"
)

const (
	homepageModeBuiltin = "builtin"
	homepageModeUpload  = "upload"

	homepageMaxHTML     = 2 << 20
	homepageMaxZip      = 25 << 20
	homepageMaxUnpacked = 50 << 20
	homepageMaxFile     = 20 << 20
	homepageMaxFiles    = 200
	homepageKeep        = 5
	homepagePreviewTTL  = 10 * time.Minute

	// The uploaded document runs in an opaque origin. It may load its own
	// scripts and styles, but cannot read the panel's storage or cookies.
	homepageCSP = "sandbox allow-scripts allow-forms allow-modals allow-popups allow-downloads " +
		"allow-top-navigation-by-user-activation; " +
		"default-src 'self' data: blob: https: http:; " +
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' blob: data: https: http:; " +
		"style-src 'self' 'unsafe-inline' https: http:; " +
		"img-src 'self' data: blob: https: http:; " +
		"font-src 'self' data: https:; " +
		"connect-src 'self' https: http:; " +
		"media-src 'self' data: blob: https: http:; " +
		"worker-src 'self' blob:; " +
		"object-src 'none'; base-uri 'self'; form-action 'self' https: http:; " +
		"frame-ancestors 'self'"
)

type homepagePreviewEntry struct {
	Version string
	Expires time.Time
}

// homepageCacheEntry avoids a settings query for every CSS/JS/image request.
// A short TTL keeps direct database edits discoverable; the admin handlers
// invalidate it immediately.
type homepageCacheEntry struct {
	Version string
	Active  bool
	At      time.Time
}

type homepageManifest struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	Files      int       `json:"files"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
	SHA256     string    `json:"sha256"`
}

type homepageVersionInfo struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	Files      int       `json:"files"`
	Size       int64     `json:"size"`
	UploadedAt time.Time `json:"uploadedAt"`
	SHA256     string    `json:"sha256"`
	Active     bool      `json:"active"`
}

var homepageAllowedExt = map[string]bool{
	".html": true, ".htm": true, ".css": true, ".js": true, ".mjs": true,
	".json": true, ".map": true, ".txt": true, ".xml": true,
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".gif": true,
	".svg": true, ".ico": true, ".avif": true,
	".woff": true, ".woff2": true, ".ttf": true, ".otf": true, ".eot": true,
	".mp4": true, ".webm": true, ".mp3": true, ".ogg": true, ".wav": true,
	".pdf": true, ".wasm": true,
}

func (s *Server) homepageRoot() string {
	return filepath.Join(s.cfg.DataDir, "homepage")
}

func (s *Server) homepageVersionsDir() string {
	return filepath.Join(s.homepageRoot(), "versions")
}

func (s *Server) homepageVersionDir(version string) string {
	return filepath.Join(s.homepageVersionsDir(), version)
}

func validHomepageVersion(version string) bool {
	if version == "" || len(version) > 80 {
		return false
	}
	for _, r := range version {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			continue
		}
		return false
	}
	return version != "." && version != ".."
}

// homepageActive returns the version currently served at /. A missing file or
// an invalid pointer falls back to the built-in homepage instead of erroring.
func (s *Server) homepageActive(ctx context.Context) (string, bool) {
	s.homepageCacheMu.Lock()
	cached := s.homepageCache
	s.homepageCacheMu.Unlock()
	if !cached.At.IsZero() && time.Since(cached.At) < 5*time.Second {
		if !cached.Active {
			return "", false
		}
		if _, err := os.Stat(filepath.Join(s.homepageVersionDir(cached.Version), "index.html")); err == nil {
			return cached.Version, true
		}
	}

	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return "", false
	}
	if strings.ToLower(strings.TrimSpace(settings["homepage_mode"])) != homepageModeUpload {
		s.cacheHomepageState("", false)
		return "", false
	}
	version := strings.TrimSpace(settings["homepage_version"])
	if !validHomepageVersion(version) {
		s.cacheHomepageState("", false)
		return "", false
	}
	if _, err := os.Stat(filepath.Join(s.homepageVersionDir(version), "index.html")); err != nil {
		s.cacheHomepageState("", false)
		return "", false
	}
	s.cacheHomepageState(version, true)
	return version, true
}

func (s *Server) cacheHomepageState(version string, active bool) {
	s.homepageCacheMu.Lock()
	s.homepageCache = homepageCacheEntry{Version: version, Active: active, At: time.Now()}
	s.homepageCacheMu.Unlock()
}

func (s *Server) invalidateHomepageCache() {
	s.homepageCacheMu.Lock()
	s.homepageCache = homepageCacheEntry{}
	s.homepageCacheMu.Unlock()
}

// homepageSelected returns the configured version regardless of the current
// mode. Backups use it so a temporarily disabled custom page is not lost.
func (s *Server) homepageSelected(ctx context.Context) (string, bool) {
	settings, err := s.store.GetSettings(ctx)
	if err != nil {
		return "", false
	}
	version := strings.TrimSpace(settings["homepage_version"])
	if !validHomepageVersion(version) {
		return "", false
	}
	if _, err := os.Stat(filepath.Join(s.homepageVersionDir(version), "index.html")); err != nil {
		return "", false
	}
	return version, true
}

func (s *Server) serveCustomHomepage(w http.ResponseWriter, r *http.Request) bool {
	version, ok := s.homepageActive(r.Context())
	if !ok {
		return false
	}
	s.writeHomepageWrapper(w, r, version)
	return true
}

func (s *Server) writeHomepageWrapper(w http.ResponseWriter, r *http.Request, version string) {
	title := "MikvAce Panel"
	if settings, err := s.store.GetSettings(r.Context()); err == nil {
		title = firstNonEmpty(settings["panel_name"], title)
	}
	src := "/homepage/index.html?v=" + url.QueryEscape(version)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-src 'self'; base-uri 'none'; form-action 'none'")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex, nofollow">
<title>%s</title>
<style>
html,body{height:100%%;margin:0;background:#0d0d0c}
iframe{display:block;width:100%%;height:100%%;border:0}
</style>
</head>
<body>
<iframe src="%s" title="%s" referrerpolicy="no-referrer" allow="clipboard-write; fullscreen" sandbox="allow-scripts allow-forms allow-modals allow-popups allow-downloads allow-top-navigation-by-user-activation"></iframe>
</body>
</html>`, html.EscapeString(title), html.EscapeString(src), html.EscapeString(title))
}

func (s *Server) handleHomepagePage(w http.ResponseWriter, r *http.Request) {
	version, ok := s.homepageActive(r.Context())
	if !ok {
		http.NotFound(w, r)
		return
	}
	rel := strings.TrimPrefix(r.URL.Path, "/homepage")
	s.serveHomepageFile(w, r, filepath.Join(s.homepageVersionDir(version)), rel, false)
}

func (s *Server) handleHomepagePreviewPage(w http.ResponseWriter, r *http.Request) {
	token := routeParam(r, "token")
	version, ok := s.homepagePreviewVersion(token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	prefix := "/preview/homepage/" + token
	rel := strings.TrimPrefix(r.URL.Path, prefix)
	s.serveHomepageFile(w, r, filepath.Join(s.homepageVersionDir(version)), rel, true)
}

func cleanHomepageRel(rel string) (string, bool) {
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		rel = "index.html"
	}
	if strings.ContainsRune(rel, 0) || strings.Contains(rel, "\\") {
		return "", false
	}
	clean := path.Clean("/" + rel)
	if clean == "/" {
		clean = "/index.html"
	}
	out := strings.TrimPrefix(clean, "/")
	if out == "" || out == ".." || strings.HasPrefix(out, "../") {
		return "", false
	}
	return out, true
}

func homepageContentType(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json", ".map":
		return "application/json; charset=utf-8"
	case ".txt":
		return "text/plain; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	}
	if ct := mime.TypeByExtension(filepath.Ext(name)); ct != "" {
		return ct
	}
	return "application/octet-stream"
}

func (s *Server) serveHomepageFile(w http.ResponseWriter, r *http.Request, root, rel string, preview bool) {
	rel, ok := cleanHomepageRel(rel)
	if !ok || rel == "manifest.json" {
		http.NotFound(w, r)
		return
	}
	full := filepath.Join(root, filepath.FromSlash(rel))
	inside, err := filepath.Rel(root, full)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(os.PathSeparator)) {
		http.NotFound(w, r)
		return
	}
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		full = filepath.Join(full, "index.html")
		rel = path.Join(rel, "index.html")
		info, err = os.Stat(full)
	}
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(full)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()

	h := w.Header()
	h.Set("Content-Type", homepageContentType(rel))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Content-Security-Policy", homepageCSP)
	if preview || strings.HasSuffix(strings.ToLower(rel), ".html") {
		h.Set("Cache-Control", "no-store")
	} else {
		h.Set("Cache-Control", "no-cache")
	}
	http.ServeContent(w, r, rel, info.ModTime(), f)
}

// ---- admin API -----------------------------------------------------------

func (s *Server) handleHomepageInfo(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.GetSettings(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	mode := strings.ToLower(strings.TrimSpace(settings["homepage_mode"]))
	if mode != homepageModeUpload {
		mode = homepageModeBuiltin
	}
	active := strings.TrimSpace(settings["homepage_version"])
	versions, err := s.listHomepageVersions()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for i := range versions {
		versions[i].Active = mode == homepageModeUpload && versions[i].Version == active
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":          mode,
		"activeVersion": active,
		"versions":      versions,
	})
}

func (s *Server) listHomepageVersions() ([]homepageVersionInfo, error) {
	entries, err := os.ReadDir(s.homepageVersionsDir())
	if err != nil {
		if os.IsNotExist(err) {
			return []homepageVersionInfo{}, nil
		}
		return nil, err
	}
	out := make([]homepageVersionInfo, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !validHomepageVersion(entry.Name()) {
			continue
		}
		dir := filepath.Join(s.homepageVersionsDir(), entry.Name())
		info := homepageVersionInfo{Version: entry.Name()}
		if b, err := os.ReadFile(filepath.Join(dir, "manifest.json")); err == nil {
			var m homepageManifest
			if json.Unmarshal(b, &m) == nil {
				info.Source = m.Source
				info.Files = m.Files
				info.Size = m.Size
				info.UploadedAt = m.UploadedAt
				info.SHA256 = m.SHA256
			}
		}
		if info.UploadedAt.IsZero() {
			if st, err := entry.Info(); err == nil {
				info.UploadedAt = st.ModTime().UTC()
			}
		}
		out = append(out, info)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UploadedAt.Equal(out[j].UploadedAt) {
			return out[i].Version > out[j].Version
		}
		return out[i].UploadedAt.After(out[j].UploadedAt)
	})
	return out, nil
}

func (s *Server) handleHomepageUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, homepageMaxZip+1<<20)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload: "+err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "missing file field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, homepageMaxZip+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read upload")
		return
	}
	if len(data) == 0 {
		writeError(w, http.StatusBadRequest, "empty file")
		return
	}
	source := ""
	if header != nil {
		source = filepath.Base(header.Filename)
	}

	version := time.Now().UTC().Format("20060102-150405") + "-" + store.RandomToken(3)
	tmp := filepath.Join(s.homepageVersionsDir(), ".tmp-"+version)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer os.RemoveAll(tmp)

	var files int
	var size int64
	isZip := len(data) >= 4 && data[0] == 'P' && data[1] == 'K' && (data[2] == 3 || data[2] == 5 || data[2] == 7)
	if isZip {
		if len(data) > homepageMaxZip {
			writeError(w, http.StatusRequestEntityTooLarge, "ZIP is larger than 25 MB")
			return
		}
		files, size, err = extractHomepageZip(data, tmp)
	} else {
		if len(data) > homepageMaxHTML {
			writeError(w, http.StatusRequestEntityTooLarge, "HTML is larger than 2 MB; use a ZIP for assets")
			return
		}
		if err := os.WriteFile(filepath.Join(tmp, "index.html"), data, 0o644); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		files, size = 1, int64(len(data))
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := os.Stat(filepath.Join(tmp, "index.html")); err != nil {
		writeError(w, http.StatusBadRequest, "upload must contain an index.html")
		return
	}

	sum := sha256.Sum256(data)
	manifest := homepageManifest{
		Version: version, Source: source, Files: files, Size: size,
		UploadedAt: time.Now().UTC(), SHA256: hex.EncodeToString(sum[:]),
	}
	mb, _ := json.MarshalIndent(manifest, "", "  ")
	if err := os.WriteFile(filepath.Join(tmp, "manifest.json"), mb, 0o644); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	settings, _ := s.store.GetSettings(r.Context())
	selected := strings.TrimSpace(settings["homepage_version"])
	final := s.homepageVersionDir(version)
	s.homepageMu.Lock()
	err = os.Rename(tmp, final)
	if err == nil {
		s.pruneHomepageVersionsLocked(selected)
	}
	s.homepageMu.Unlock()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": version, "files": files, "size": size,
		"message": "uploaded; activate it to serve at /",
	})
}

func (s *Server) handleHomepageActivate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	version := strings.TrimSpace(req.Version)
	if !validHomepageVersion(version) {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	if _, err := os.Stat(filepath.Join(s.homepageVersionDir(version), "index.html")); err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	if err := s.store.UpdateSettings(r.Context(), map[string]string{
		"homepage_version": version,
		"homepage_mode":    homepageModeUpload,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateHomepageCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": homepageModeUpload, "activeVersion": version})
}

func (s *Server) handleHomepageMode(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	switch mode {
	case homepageModeBuiltin:
		if err := s.store.SetSetting(r.Context(), "homepage_mode", homepageModeBuiltin); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	case homepageModeUpload:
		settings, _ := s.store.GetSettings(r.Context())
		version := strings.TrimSpace(settings["homepage_version"])
		if !validHomepageVersion(version) {
			versions, _ := s.listHomepageVersions()
			if len(versions) > 0 {
				version = versions[0].Version
			}
		}
		if _, err := os.Stat(filepath.Join(s.homepageVersionDir(version), "index.html")); version == "" || err != nil {
			writeError(w, http.StatusConflict, "upload and activate a homepage first")
			return
		}
		if err := s.store.UpdateSettings(r.Context(), map[string]string{
			"homepage_mode":    homepageModeUpload,
			"homepage_version": version,
		}); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	default:
		writeError(w, http.StatusBadRequest, "mode must be builtin or upload")
		return
	}
	s.invalidateHomepageCache()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mode": mode})
}

func (s *Server) handleHomepageDeleteVersion(w http.ResponseWriter, r *http.Request) {
	version := routeParam(r, "version")
	if !validHomepageVersion(version) {
		writeError(w, http.StatusBadRequest, "invalid version")
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	if strings.EqualFold(settings["homepage_mode"], homepageModeUpload) && settings["homepage_version"] == version {
		writeError(w, http.StatusConflict, "cannot delete the active version")
		return
	}
	if err := os.RemoveAll(s.homepageVersionDir(version)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if settings["homepage_version"] == version {
		_ = s.store.SetSetting(r.Context(), "homepage_version", "")
	}
	s.invalidateHomepageCache()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleHomepageReset(w http.ResponseWriter, r *http.Request) {
	if err := s.store.SetSetting(r.Context(), "homepage_mode", homepageModeBuiltin); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.invalidateHomepageCache()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleHomepagePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Version string `json:"version"`
	}
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		settings, _ := s.store.GetSettings(r.Context())
		version = strings.TrimSpace(settings["homepage_version"])
	}
	if !validHomepageVersion(version) {
		writeError(w, http.StatusBadRequest, "no homepage version selected")
		return
	}
	if _, err := os.Stat(filepath.Join(s.homepageVersionDir(version), "index.html")); err != nil {
		writeError(w, http.StatusNotFound, "version not found")
		return
	}
	token := s.issueHomepagePreview(version)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":       "/preview/homepage/" + token + "/index.html",
		"expiresAt": time.Now().Add(homepagePreviewTTL).UTC(),
	})
}

func (s *Server) issueHomepagePreview(version string) string {
	token := store.RandomToken(24)
	s.homepageMu.Lock()
	defer s.homepageMu.Unlock()
	if s.homepagePreviews == nil {
		s.homepagePreviews = map[string]homepagePreviewEntry{}
	}
	now := time.Now()
	for k, entry := range s.homepagePreviews {
		if now.After(entry.Expires) {
			delete(s.homepagePreviews, k)
		}
	}
	if len(s.homepagePreviews) >= 128 {
		for k := range s.homepagePreviews {
			delete(s.homepagePreviews, k)
			break
		}
	}
	s.homepagePreviews[token] = homepagePreviewEntry{Version: version, Expires: now.Add(homepagePreviewTTL)}
	return token
}

func (s *Server) homepagePreviewVersion(token string) (string, bool) {
	s.homepageMu.Lock()
	defer s.homepageMu.Unlock()
	entry, ok := s.homepagePreviews[token]
	if !ok {
		return "", false
	}
	if time.Now().After(entry.Expires) {
		delete(s.homepagePreviews, token)
		return "", false
	}
	return entry.Version, true
}

func (s *Server) pruneHomepageVersionsLocked(active string) {
	entries, err := os.ReadDir(s.homepageVersionsDir())
	if err != nil {
		return
	}
	type item struct {
		name string
		mod  time.Time
	}
	items := make([]item, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !validHomepageVersion(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		items = append(items, item{name: entry.Name(), mod: info.ModTime()})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].mod.After(items[j].mod) })
	kept := 0
	for _, it := range items {
		if it.name == active {
			continue
		}
		kept++
		if kept <= homepageKeep {
			continue
		}
		_ = os.RemoveAll(filepath.Join(s.homepageVersionsDir(), it.name))
	}
}

// ---- backup integration --------------------------------------------------

// addHomepageToTar includes the active uploaded page in a panel backup.
func addHomepageToTar(tw *tar.Writer, dir, prefix string, mod time.Time) error {
	return filepath.WalkDir(dir, func(name string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !entry.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		f, err := os.Open(name)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr := &tar.Header{
			Name: path.Join(prefix, filepath.ToSlash(rel)), Mode: 0o644,
			Size: info.Size(), ModTime: mod, Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.CopyN(tw, f, info.Size())
		return err
	})
}

// extractHomepageFromArchive stages homepage files from a backup. They are
// moved into place after the restored database has been validated on startup.
func extractHomepageFromArchive(tarPath, dataDir string) error {
	staging := filepath.Join(dataDir, "homepage", ".restore")
	_ = os.RemoveAll(staging)
	f, err := os.Open(tarPath)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	found := false
	var total int64
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if !strings.HasPrefix(name, "homepage/versions/") {
			continue
		}
		rel := strings.TrimPrefix(name, "homepage/versions/")
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) != 2 || !validHomepageVersion(parts[0]) {
			continue
		}
		fileRel, ok := cleanZipRel(parts[1])
		if !ok {
			continue
		}
		ext := strings.ToLower(path.Ext(fileRel))
		if ext != ".json" && !homepageAllowedExt[ext] {
			continue
		}
		if hdr.Size > homepageMaxFile {
			return fmt.Errorf("homepage file %q is too large", fileRel)
		}
		total += hdr.Size
		if total > 2*homepageMaxUnpacked {
			return fmt.Errorf("homepage files in archive are too large")
		}
		target := filepath.Join(staging, "versions", parts[0], filepath.FromSlash(fileRel))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, tr, hdr.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		found = true
	}
	if !found {
		_ = os.RemoveAll(staging)
	}
	return nil
}

// ApplyPendingHomepageRestore swaps staged homepage versions into place. It is
// called after the database restore has been applied and before HTTP serving.
func ApplyPendingHomepageRestore(dataDir string) error {
	staging := filepath.Join(dataDir, "homepage", ".restore")
	entries, err := os.ReadDir(filepath.Join(staging, "versions"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dest := filepath.Join(dataDir, "homepage", "versions")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !validHomepageVersion(entry.Name()) {
			continue
		}
		target := filepath.Join(dest, entry.Name())
		_ = os.RemoveAll(target)
		if err := os.Rename(filepath.Join(staging, "versions", entry.Name()), target); err != nil {
			return err
		}
	}
	return os.RemoveAll(staging)
}

// ---- ZIP handling --------------------------------------------------------

func cleanZipRel(name string) (string, bool) {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "./")
	if name == "" || strings.HasPrefix(name, "/") || strings.ContainsRune(name, 0) {
		return "", false
	}
	if len(name) >= 2 && name[1] == ':' {
		return "", false // Windows drive prefix
	}
	clean := path.Clean(name)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", false
	}
	return clean, true
}

func commonZipRoot(names []string) string {
	root := ""
	for _, name := range names {
		parts := strings.SplitN(name, "/", 2)
		if len(parts) < 2 {
			return ""
		}
		if root == "" {
			root = parts[0]
		} else if root != parts[0] {
			return ""
		}
	}
	return root
}

func extractHomepageZip(data []byte, dest string) (int, int64, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid ZIP archive")
	}
	type entry struct {
		name string
		out  string
		file *zip.File
	}
	entries := make([]entry, 0, len(zr.File))
	names := make([]string, 0, len(zr.File))
	var declared uint64
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if f.Flags&0x1 != 0 {
			return 0, 0, fmt.Errorf("encrypted ZIP entries are not supported")
		}
		if !f.Mode().IsRegular() || f.Mode()&os.ModeSymlink != 0 {
			return 0, 0, fmt.Errorf("only regular files are allowed in the archive")
		}
		name, ok := cleanZipRel(f.Name)
		if !ok {
			return 0, 0, fmt.Errorf("unsafe path in archive: %q", f.Name)
		}
		if strings.HasPrefix(name, "__MACOSX/") {
			continue
		}
		base := path.Base(name)
		if base == ".DS_Store" || base == "Thumbs.db" {
			continue
		}
		if !homepageAllowedExt[strings.ToLower(path.Ext(name))] {
			return 0, 0, fmt.Errorf("unsupported file type: %q", name)
		}
		if f.UncompressedSize64 > homepageMaxFile {
			return 0, 0, fmt.Errorf("file %q is too large", name)
		}
		declared += f.UncompressedSize64
		if declared > homepageMaxUnpacked {
			return 0, 0, fmt.Errorf("uncompressed archive is larger than 50 MB")
		}
		entries = append(entries, entry{name: name, out: name, file: f})
		names = append(names, name)
	}
	if len(entries) == 0 {
		return 0, 0, fmt.Errorf("archive is empty")
	}
	if len(entries) > homepageMaxFiles {
		return 0, 0, fmt.Errorf("archive contains more than %d files", homepageMaxFiles)
	}

	rootIndex := func(list []string) bool {
		for _, name := range list {
			if strings.EqualFold(name, "index.html") || strings.EqualFold(name, "index.htm") {
				return true
			}
		}
		return false
	}
	if !rootIndex(names) {
		root := commonZipRoot(names)
		if root == "" {
			return 0, 0, fmt.Errorf("archive must contain index.html")
		}
		for i := range entries {
			entries[i].out = strings.TrimPrefix(entries[i].out, root+"/")
		}
		stripped := make([]string, 0, len(entries))
		for _, e := range entries {
			stripped = append(stripped, e.out)
		}
		if !rootIndex(stripped) {
			return 0, 0, fmt.Errorf("archive must contain index.html")
		}
	}

	seen := map[string]bool{}
	var total int64
	for _, e := range entries {
		out := e.out
		base := path.Base(out)
		if strings.EqualFold(base, "index.html") || strings.EqualFold(base, "index.htm") {
			out = path.Join(path.Dir(out), "index.html")
			if path.Dir(out) == "." {
				out = "index.html"
			}
		}
		if seen[out] {
			return 0, 0, fmt.Errorf("duplicate file after normalization: %q", out)
		}
		seen[out] = true
		target := filepath.Join(dest, filepath.FromSlash(out))
		root := filepath.Clean(dest) + string(os.PathSeparator)
		if !strings.HasPrefix(filepath.Clean(target)+string(os.PathSeparator), root) && filepath.Clean(target) != filepath.Clean(dest) {
			return 0, 0, fmt.Errorf("unsafe path in archive: %q", out)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return 0, 0, err
		}
		rc, err := e.file.Open()
		if err != nil {
			return 0, 0, err
		}
		fw, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			_ = rc.Close()
			return 0, 0, err
		}
		n, copyErr := io.Copy(fw, io.LimitReader(rc, homepageMaxFile+1))
		closeErr := fw.Close()
		_ = rc.Close()
		if copyErr != nil || closeErr != nil {
			return 0, 0, fmt.Errorf("could not extract %q", out)
		}
		if n > homepageMaxFile {
			return 0, 0, fmt.Errorf("file %q is too large", out)
		}
		total += n
		if total > homepageMaxUnpacked {
			return 0, 0, fmt.Errorf("uncompressed archive is larger than 50 MB")
		}
	}
	return len(entries), total, nil
}
