// Package client implements the MikvAce command line client: subscription
// fetching, endpoint diagnostics and running a local Hysteria client.
package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// HTTPClient is used for all panel requests.
type HTTPClient struct {
	Client *http.Client
	UA     string
}

// NewHTTPClient creates a client with a sane timeout.
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{
		Client: &http.Client{Timeout: 25 * time.Second},
		UA:     "MikvAce-Client/1.0",
	}
}

// Fetch downloads a subscription in a specific format.
func (c *HTTPClient) Fetch(ctx context.Context, subURL, format string) ([]byte, http.Header, error) {
	u, err := url.Parse(subURL)
	if err != nil {
		return nil, nil, err
	}
	if format != "" {
		q := u.Query()
		q.Set("format", format)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("panel returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, resp.Header, nil
}

// Info is the /api/files/info payload.
type Info struct {
	Enabled  bool   `json:"enabled"`
	Expire   string `json:"expire"`
	Servers  int    `json:"servers"`
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Traffic  struct {
		Used  int64 `json:"used"`
		Limit int64 `json:"limit"`
	} `json:"traffic"`
}

// FetchInfo loads subscription information.
func (c *HTTPClient) FetchInfo(ctx context.Context, subURL string) (*Info, error) {
	u, err := url.Parse(subURL)
	if err != nil {
		return nil, err
	}
	p := u.Path
	if strings.Contains(p, "/api/files/info/") {
		// already an info URL
	} else if strings.Contains(p, "/api/files/") {
		p = strings.Replace(p, "/api/files/", "/api/files/info/", 1)
	} else {
		return nil, fmt.Errorf("subscription URL must contain /api/files/<token>")
	}
	u.Path = p
	u.RawQuery = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UA)
	resp, err := c.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("panel returned %s", resp.Status)
	}
	var info Info
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, err
	}
	return &info, nil
}

// Endpoint is one parsed hysteria2:// link.
type Endpoint struct {
	Raw          string
	Name         string
	Host         string
	Port         int
	PortRange    string
	HopInterval  int
	User         string
	Password     string
	SNI          string
	Insecure     bool
	Obfs         string
	ObfsPassword string
}

// ParseURI parses a hysteria2 share link.
func ParseURI(raw string) (*Endpoint, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, err
	}
	if u.Scheme != "hysteria2" && u.Scheme != "hy2" {
		return nil, fmt.Errorf("unsupported scheme %q", u.Scheme)
	}
	port := 443
	if p := u.Port(); p != "" {
		port, _ = strconv.Atoi(p)
	}
	ep := &Endpoint{Raw: raw, Name: strings.TrimPrefix(u.Fragment, ""), Host: u.Hostname(), Port: port}
	if u.User != nil {
		ep.User = u.User.Username()
		ep.Password, _ = u.User.Password()
	}
	q := u.Query()
	ep.SNI = q.Get("sni")
	ep.Insecure = q.Get("insecure") == "1" || strings.EqualFold(q.Get("insecure"), "true")
	ep.PortRange = q.Get("mport")
	ep.Obfs = q.Get("obfs")
	ep.ObfsPassword = q.Get("obfs-password")
	if v := q.Get("mportHopInt"); v != "" {
		ep.HopInterval, _ = strconv.Atoi(v)
	}
	if ep.Name == "" {
		ep.Name = ep.Host
	}
	return ep, nil
}

// ParseURIList parses plain or base64 encoded URI lists.
func ParseURIList(body []byte) ([]*Endpoint, error) {
	text := strings.TrimSpace(string(body))
	if !strings.Contains(text, "://") {
		if decoded, err := base64.StdEncoding.DecodeString(text); err == nil {
			text = string(decoded)
		}
	}
	out := []*Endpoint{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "hysteria2://") && !strings.HasPrefix(line, "hy2://") {
			continue
		}
		ep, err := ParseURI(line)
		if err != nil {
			continue
		}
		out = append(out, ep)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no Hysteria 2 endpoints found")
	}
	return out, nil
}

// HysteriaClientConfig is the subset of the Hysteria client config we generate.
type HysteriaClientConfig struct {
	Server    string           `yaml:"server"`
	Auth      string           `yaml:"auth"`
	TLS       ClientTLS        `yaml:"tls"`
	Obfs      *ClientObfs      `yaml:"obfs,omitempty"`
	Transport *ClientTransport `yaml:"transport,omitempty"`
	SOCKS5    *ClientListen    `yaml:"socks5,omitempty"`
	HTTP      *ClientListen    `yaml:"http,omitempty"`
}

type ClientTLS struct {
	SNI      string   `yaml:"sni,omitempty"`
	Insecure bool     `yaml:"insecure,omitempty"`
	ALPN     []string `yaml:"alpn,omitempty"`
}

type ClientObfs struct {
	Type       string                `yaml:"type"`
	Salamander *ClientObfsSalamander `yaml:"salamander,omitempty"`
	Gecko      *ClientObfsGecko      `yaml:"gecko,omitempty"`
}

type ClientObfsSalamander struct {
	Password string `yaml:"password"`
}

type ClientObfsGecko struct {
	Password string `yaml:"password"`
}

type ClientTransport struct {
	UDP ClientUDP `yaml:"udp"`
}

type ClientUDP struct {
	HopInterval string `yaml:"hopInterval,omitempty"`
}

type ClientListen struct {
	Listen string `yaml:"listen"`
}

// BuildClientConfig generates a Hysteria client config for one endpoint.
func BuildClientConfig(ep *Endpoint, socks5, httpAddr string) (string, error) {
	server := net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port))
	if ep.PortRange != "" {
		server = net.JoinHostPort(ep.Host, ep.PortRange)
	}
	cfg := HysteriaClientConfig{
		Server: server,
		Auth:   ep.User + ":" + ep.Password,
		TLS: ClientTLS{
			SNI: ep.SNI, Insecure: ep.Insecure, ALPN: []string{"h3"},
		},
	}
	if ep.Obfs != "" && ep.ObfsPassword != "" {
		cfg.Obfs = &ClientObfs{Type: ep.Obfs}
		switch ep.Obfs {
		case "gecko":
			cfg.Obfs.Gecko = &ClientObfsGecko{Password: ep.ObfsPassword}
		default:
			cfg.Obfs.Salamander = &ClientObfsSalamander{Password: ep.ObfsPassword}
		}
	}
	if ep.HopInterval > 0 {
		cfg.Transport = &ClientTransport{UDP: ClientUDP{HopInterval: strconv.Itoa(ep.HopInterval) + "s"}}
	}
	if socks5 != "" {
		cfg.SOCKS5 = &ClientListen{Listen: socks5}
	}
	if httpAddr != "" {
		cfg.HTTP = &ClientListen{Listen: httpAddr}
	}
	if cfg.SOCKS5 == nil && cfg.HTTP == nil {
		cfg.SOCKS5 = &ClientListen{Listen: "127.0.0.1:1080"}
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RunHysteria writes a temporary client config and executes the Hysteria
// binary in client mode.
func RunHysteria(ctx context.Context, bin string, ep *Endpoint, socks5, httpAddr string) error {
	content, err := BuildClientConfig(ep, socks5, httpAddr)
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "mikvace-client-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, bin, "client", "-c", path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// CheckResult is a diagnostic result for one endpoint.
type CheckResult struct {
	Name    string
	Host    string
	Port    int
	Address string
	Error   string
	Millis  int64
}

// CheckEndpoints resolves endpoint hostnames and optionally probes TCP.
func CheckEndpoints(ctx context.Context, endpoints []*Endpoint, tcpProbe bool) []CheckResult {
	out := []CheckResult{}
	for _, ep := range endpoints {
		res := CheckResult{Name: ep.Name, Host: ep.Host, Port: ep.Port}
		start := time.Now()
		ips, err := net.DefaultResolver.LookupHost(ctx, ep.Host)
		if err != nil {
			res.Error = err.Error()
			out = append(out, res)
			continue
		}
		res.Address = strings.Join(ips, ",")
		res.Millis = time.Since(start).Milliseconds()
		if tcpProbe {
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(ep.Host, strconv.Itoa(ep.Port)))
			if err != nil {
				res.Error = "tcp: " + err.Error()
			} else {
				_ = conn.Close()
			}
		}
		out = append(out, res)
	}
	return out
}

// FormatBytes renders a byte count for CLI output.
func FormatBytes(n int64) string {
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
	return fmt.Sprintf("%.2f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
