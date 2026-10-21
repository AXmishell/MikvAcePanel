package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/subscription"
)

var qrCache sync.Map

// handleSubscription implements GET /api/files/:token with the CELERITY
// compatible format detection and response headers.
func (s *Server) handleSubscription(w http.ResponseWriter, r *http.Request) {
	token := chiURLParam(r, "token")
	user, err := s.store.GetUserByToken(r.Context(), token)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("# User not found"))
		return
	}
	nowT := time.Now().UTC()
	reason := ""
	switch {
	case user.Expired(nowT):
		reason = "Expired"
	case !user.Enabled:
		reason = "Inactive"
	case user.TrafficExceeded():
		reason = "Traffic exceeded"
	}
	browser := isBrowserRequest(r)
	format := subscription.NormalizeFormat(r.URL.Query().Get("format"))
	if reason != "" {
		if browser && format == "" {
			s.renderSubPageError(w, r, user, reason)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("# " + reason))
		return
	}
	if browser && format == "" {
		s.renderSubPage(w, r, user)
		return
	}
	if format == "" {
		format = subscription.DetectFormat(r.UserAgent())
	}
	nodes, err := s.store.NodesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load nodes")
		return
	}
	if len(nodes) == 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("# No servers available"))
		return
	}
	subscription.SortNodes(nodes)
	body, err := subscription.Render(format, user, nodes)
	if err != nil {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("# " + err.Error()))
		return
	}
	settings, _ := s.store.GetSettings(r.Context())
	title := subscription.Title(user, settings["subscription_title"])
	updateInterval := parseIntSetting(settings["subscription_update_interval"], 12)
	if updateInterval <= 0 {
		updateInterval = 12
	}
	headers := w.Header()
	headers.Set("Content-Type", subscription.ContentType(format))
	headers.Set("Content-Disposition", contentDisposition(user.Username))
	headers.Set("Profile-Title", subscription.EncodeTitle(title))
	headers.Set("Profile-Update-Interval", strconv.Itoa(updateInterval))
	headers.Set("Subscription-Userinfo", userInfoHeader(user))
	headers.Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	headers.Set("Pragma", "no-cache")
	headers.Set("Expires", "0")
	if v := headerSafe(settings["support_url"]); v != "" {
		headers.Set("support-url", v)
	}
	if v := headerSafe(settings["web_page_url"]); v != "" {
		headers.Set("profile-web-page-url", v)
	}
	if v := headerSafe(settings["happ_provider_id"]); v != "" {
		headers.Set("providerid", v)
	}
	ua := strings.ToLower(r.UserAgent())
	isHapp := strings.Contains(ua, "happ")
	isIncy := strings.Contains(ua, "incy")
	if isHapp || isIncy {
		routing := ""
		if strings.EqualFold(settings["happ_routing_mode"], "custom") {
			profile := strings.TrimSpace(settings["happ_routing_profile"])
			if profile != "" && json.Valid([]byte(profile)) {
				scheme := ""
				if isHapp {
					scheme = "happ"
				}
				routing = scheme + "://routing/onadd/" + base64.StdEncoding.EncodeToString([]byte(profile))
			}
		}
		if routing == "" && isHapp {
			routing = "happ://routing/off"
		}
		if routing != "" {
			headers.Set("routing", routing)
			if format == "uri" {
				body = routing + "\n" + body
			}
		}
		if isHapp {
			if announce := strings.TrimSpace(settings["happ_announce"]); announce != "" {
				ascii := true
				for _, r := range announce {
					if r < 0x20 || r > 0x7e {
						ascii = false
						break
					}
				}
				if ascii {
					headers.Set("announce", announce)
				} else {
					headers.Set("announce", "base64:"+base64.StdEncoding.EncodeToString([]byte(announce)))
				}
			}
		}
	}
	headers.Set("X-Subscription-Format", format)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// handleSubInfo implements GET /api/files/info/:token.
func (s *Server) handleSubInfo(w http.ResponseWriter, r *http.Request) {
	token := chiURLParam(r, "token")
	user, err := s.store.GetUserByToken(r.Context(), token)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	nodes, err := s.store.NodesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load nodes")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":  user.Enabled,
		"groups":   user.Groups,
		"traffic":  map[string]int64{"used": user.TrafficUsed(), "limit": user.TrafficLimit},
		"expire":   user.ExpireAt,
		"servers":  len(nodes),
		"userId":   user.UserID,
		"username": user.Username,
	})
}

func (s *Server) renderSubPage(w http.ResponseWriter, r *http.Request, user *model.User) {
	settings, _ := s.store.GetSettings(r.Context())
	nodes, err := s.store.NodesForUser(r.Context(), user.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not load nodes")
		return
	}
	subscription.SortNodes(nodes)
	base := publicBaseURL(r, settings["public_url"])
	subURL := base + "/api/files/" + user.SubscriptionToken
	data := subPageData{
		Title:      firstNonEmpty(settings["page_title"], "Connection"),
		PageNote:   firstNonEmpty(settings["page_note"], "Your personal subscription"),
		LogoURL:    settings["logo_url"],
		SupportURL: settings["support_url"],
		SubURL:     subURL,
		Username:   firstNonEmpty(user.Username, user.UserID),
		PanelName:  firstNonEmpty(settings["panel_name"], "MikvAce Panel"),
		UsedLabel:  formatBytes(user.TrafficUsed()),
		LimitLabel: formatBytes(user.TrafficLimit),
		Unlimited:  user.TrafficLimit <= 0,
		ClashURL:   subURL + "?format=clash",
		SingboxURL: subURL + "?format=singbox",
		URIURL:     subURL + "?format=uri",
		QRData:     qrDataURI(subURL),
		Year:       time.Now().Year(),
	}
	if user.ExpireAt != nil {
		data.ExpireLabel = user.ExpireAt.Local().Format("2006-01-02")
	} else {
		data.ExpireLabel = "不限制"
	}
	type loc struct {
		Name    string
		Flag    string
		Configs []subConfig
	}
	locMap := map[string]*loc{}
	order := []string{}
	for _, n := range nodes {
		key := n.Name
		l, ok := locMap[key]
		if !ok {
			l = &loc{Name: n.Name, Flag: n.Flag}
			locMap[key] = l
			order = append(order, key)
		}
		if subscription.IsXray(n) {
			if uri := subscription.VLESSURI(user, n); uri != "" {
				l.Configs = append(l.Configs, subConfig{Name: "VLESS", URI: uri})
			}
			continue
		}
		for _, e := range subscription.Endpoints(n) {
			l.Configs = append(l.Configs, subConfig{Name: e.Label, URI: subscription.URI(user, n, e)})
		}
	}
	for _, key := range order {
		data.Locations = append(data.Locations, subLocation{
			Name: locMap[key].Name, Flag: locMap[key].Flag, Configs: locMap[key].Configs,
		})
	}
	// Optional custom buttons: [{"label":"...","url":"...{url}"}]
	if raw := strings.TrimSpace(settings["subscription_buttons"]); raw != "" {
		data.Buttons = parseButtons(raw, subURL)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	if err := subPageTemplate.Execute(w, data); err != nil {
		s.log.Warn("render subscription page", "error", err)
	}
}

func (s *Server) renderSubPageError(w http.ResponseWriter, r *http.Request, user *model.User, reason string) {
	data := subPageData{
		Title: "订阅不可用", PageNote: reason, Username: user.UserID,
		Error: reason, Year: time.Now().Year(),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = subPageErrorTemplate.Execute(w, data)
}

func isBrowserRequest(r *http.Request) bool {
	if !strings.Contains(r.Header.Get("Accept"), "text/html") {
		return false
	}
	ua := strings.ToLower(r.UserAgent())
	return strings.Contains(ua, "mozilla") || strings.Contains(ua, "chrome") ||
		strings.Contains(ua, "safari") || strings.Contains(ua, "edge") || strings.Contains(ua, "opera")
}

func userInfoHeader(u *model.User) string {
	parts := []string{
		fmt.Sprintf("upload=%d", u.TrafficTx),
		fmt.Sprintf("download=%d", u.TrafficRx),
	}
	if u.TrafficLimit > 0 {
		parts = append(parts, fmt.Sprintf("total=%d", u.TrafficLimit))
	}
	expire := int64(0)
	if u.ExpireAt != nil {
		expire = u.ExpireAt.Unix()
	}
	parts = append(parts, fmt.Sprintf("expire=%d", expire))
	return strings.Join(parts, "; ")
}

func contentDisposition(name string) string {
	raw := strings.TrimSpace(name)
	if raw == "" {
		raw = "subscription"
	}
	ascii := strings.Map(func(r rune) rune {
		if r >= 0x20 && r <= 0x7e && r != '"' && r != '\\' {
			return r
		}
		return '_'
	}, raw)
	encoded := url.PathEscape(raw)
	return fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, ascii, encoded)
}

func headerSafe(v string) string {
	for _, r := range v {
		if r < 0x20 || r > 0x7e {
			return ""
		}
	}
	return strings.TrimSpace(v)
}

func publicBaseURL(r *http.Request, configured string) string {
	if c := strings.TrimRight(strings.TrimSpace(configured), "/"); c != "" {
		return c
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
		scheme = strings.TrimSpace(strings.Split(p, ",")[0])
	}
	host := r.Host
	if h := r.Header.Get("X-Forwarded-Host"); h != "" {
		host = strings.TrimSpace(strings.Split(h, ",")[0])
	}
	return scheme + "://" + host
}

func qrDataURI(content string) string {
	if v, ok := qrCache.Load(content); ok {
		return v.(string)
	}
	png, err := qrcode.Encode(content, qrcode.Medium, 260)
	if err != nil {
		return ""
	}
	uri := "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	qrCache.Store(content, uri)
	return uri
}

func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func chiURLParam(r *http.Request, key string) string {
	// Kept local to avoid importing chi in every handler file.
	return routeParam(r, key)
}

type subButton struct {
	Label string
	URL   string
}

type subConfig struct {
	Name string
	URI  string
}

type subLocation struct {
	Name    string
	Flag    string
	Configs []subConfig
}

type subPageData struct {
	Title       string
	PageNote    string
	LogoURL     string
	SupportURL  string
	SubURL      string
	Username    string
	PanelName   string
	UsedLabel   string
	LimitLabel  string
	Unlimited   bool
	ExpireLabel string
	ClashURL    string
	SingboxURL  string
	URIURL      string
	QRData      string
	Locations   []subLocation
	Buttons     []subButton
	Error       string
	Year        int
}

func parseButtons(raw, subURL string) []subButton {
	// Tiny ad-hoc parser keeps the settings format compatible with CELERITY's
	// [{label,url}] array without pulling in another JSON decoder.
	var items []struct {
		Label string `json:"label"`
		URL   string `json:"url"`
	}
	if err := jsonUnmarshal(raw, &items); err != nil {
		return nil
	}
	out := []subButton{}
	b64 := base64.StdEncoding.EncodeToString([]byte(subURL))
	for _, it := range items {
		if strings.TrimSpace(it.Label) == "" || strings.TrimSpace(it.URL) == "" {
			continue
		}
		u := strings.ReplaceAll(it.URL, "{url}", subURL)
		u = strings.ReplaceAll(u, "{url_encoded}", url.QueryEscape(subURL))
		u = strings.ReplaceAll(u, "{url_b64}", b64)
		if strings.HasPrefix(strings.ToLower(u), "javascript:") {
			continue
		}
		out = append(out, subButton{Label: it.Label, URL: u})
	}
	return out
}

var subPageTemplate = template.Must(template.New("sub").Parse(subPageHTML))
var subPageErrorTemplate = template.Must(template.New("suberr").Parse(subPageErrorHTML))
