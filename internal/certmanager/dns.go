package certmanager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/providers/dns/alidns"
	"github.com/go-acme/lego/v4/providers/dns/cloudflare"
	"github.com/go-acme/lego/v4/providers/dns/dnspod"
)

// dnsProvider mirrors lego's challenge provider with a context for cleanup.
type dnsProvider interface {
	Present(ctx context.Context, domain, token, keyAuth string) error
	CleanUp(ctx context.Context, domain, token, keyAuth string) error
}

type legoProvider struct{ p challenge.Provider }

func (l legoProvider) Present(_ context.Context, domain, token, keyAuth string) error {
	return l.p.Present(domain, token, keyAuth)
}

func (l legoProvider) CleanUp(_ context.Context, domain, token, keyAuth string) error {
	return l.p.CleanUp(domain, token, keyAuth)
}

func newDNSProvider(name string, cfg map[string]string) (dnsProvider, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "cloudflare":
		c := cloudflare.NewDefaultConfig()
		c.AuthToken = strings.TrimSpace(cfg["apiToken"])
		if c.AuthToken == "" {
			c.AuthKey = strings.TrimSpace(cfg["apiKey"])
			c.AuthEmail = strings.TrimSpace(cfg["apiEmail"])
		}
		p, err := cloudflare.NewDNSProviderConfig(c)
		if err != nil {
			return nil, err
		}
		return legoProvider{p: p}, nil
	case "alidns":
		c := alidns.NewDefaultConfig()
		c.APIKey = strings.TrimSpace(cfg["accessKeyId"])
		c.SecretKey = strings.TrimSpace(cfg["accessKeySecret"])
		if c.APIKey == "" || c.SecretKey == "" {
			return nil, errors.New("阿里云 DNS 需要 AccessKey ID 与 AccessKey Secret")
		}
		p, err := alidns.NewDNSProviderConfig(c)
		if err != nil {
			return nil, err
		}
		return legoProvider{p: p}, nil
	case "dnspod":
		c := dnspod.NewDefaultConfig()
		c.LoginToken = strings.TrimSpace(cfg["loginToken"])
		if c.LoginToken == "" {
			return nil, errors.New("DNSPod 需要 Login Token")
		}
		p, err := dnspod.NewDNSProviderConfig(c)
		if err != nil {
			return nil, err
		}
		return legoProvider{p: p}, nil
	default:
		return nil, fmt.Errorf("unsupported DNS provider %q", name)
	}
}

var txtResolvers = []string{"1.1.1.1:53", "8.8.8.8:53", "223.5.5.5:53", "119.29.29.29:53"}

// waitForTXT waits until every expected TXT value is visible through at least
// one public resolver.
func waitForTXT(ctx context.Context, records []ManualRecord, timeout time.Duration) error {
	expected := map[string]map[string]bool{}
	for _, r := range records {
		name := strings.TrimSuffix(strings.ToLower(r.Name), ".")
		if expected[name] == nil {
			expected[name] = map[string]bool{}
		}
		expected[name][r.Value] = true
	}
	deadline := time.Now().Add(timeout)
	var last []string
	for {
		missing := map[string][]string{}
		for name, values := range expected {
			got, err := lookupTXT(ctx, name)
			if err != nil {
				missing[name] = []string{"lookup: " + err.Error()}
				continue
			}
			var need []string
			for v := range values {
				if !got[v] {
					need = append(need, v)
				}
			}
			if len(need) > 0 {
				missing[name] = need
			}
		}
		if len(missing) == 0 {
			return nil
		}
		last = last[:0]
		for name, values := range missing {
			last = append(last, name+"="+strings.Join(values, ","))
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("DNS TXT 记录尚未生效: %s", strings.Join(last, "; "))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func lookupTXT(ctx context.Context, name string) (map[string]bool, error) {
	out := map[string]bool{}
	var errs []string
	ok := false
	for _, server := range txtResolvers {
		r := &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 4 * time.Second}
				return d.DialContext(ctx, "udp", server)
			},
		}
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		records, err := r.LookupTXT(qctx, name)
		cancel()
		if err != nil {
			errs = append(errs, server+": "+err.Error())
			continue
		}
		ok = true
		for _, rec := range records {
			out[strings.TrimSpace(rec)] = true
		}
	}
	if !ok {
		// Some networks block public DNS; fall back to the system resolver.
		qctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		records, err := net.DefaultResolver.LookupTXT(qctx, name)
		cancel()
		if err != nil {
			return nil, errors.New(strings.Join(errs, "; "))
		}
		for _, rec := range records {
			out[strings.TrimSpace(rec)] = true
		}
	}
	return out, nil
}
