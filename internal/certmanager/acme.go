package certmanager

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme"

	"github.com/axmishell/mikvacepanel/internal/certcrypto"
	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// Settings is the ACME configuration exposed by the panel.
type Settings struct {
	CA         string            `json:"ca"`
	Email      string            `json:"email"`
	Provider   string            `json:"provider"`
	RenewDays  int               `json:"renewDays"`
	Configured bool              `json:"configured"`
	DNSConfig  map[string]string `json:"-"`
}

// LoadSettings returns the current ACME settings, decrypting DNS credentials.
func (m *Manager) LoadSettings(ctx context.Context) (Settings, error) {
	raw, err := m.st.GetSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	s := Settings{
		CA:        strings.TrimSpace(raw[SettingCA]),
		Email:     strings.TrimSpace(raw[SettingEmail]),
		Provider:  strings.TrimSpace(raw[SettingProvider]),
		RenewDays: DefaultRenewDays,
	}
	if s.CA == "" {
		s.CA = DefaultCADir
	}
	if s.Provider == "" {
		s.Provider = "manual"
	}
	if n, err := strconv.Atoi(raw[SettingRenewDays]); err == nil && n > 0 {
		s.RenewDays = n
	}
	if token := strings.TrimSpace(raw[SettingDNSConfig]); token != "" {
		plain, err := certcrypto.DecryptString(m.key, token)
		if err != nil {
			return s, fmt.Errorf("decrypt DNS credentials: %w", err)
		}
		var cfg map[string]string
		if err := json.Unmarshal([]byte(plain), &cfg); err != nil {
			return s, fmt.Errorf("parse DNS credentials: %w", err)
		}
		s.DNSConfig = cfg
		s.Configured = len(cfg) > 0
	}
	return s, nil
}

// SaveSettings writes ACME settings. A nil dnsConfig keeps the stored
// credentials untouched; an empty map clears them.
func (m *Manager) SaveSettings(ctx context.Context, s Settings, dnsConfig map[string]string) error {
	values := map[string]string{
		SettingCA:        strings.TrimSpace(s.CA),
		SettingEmail:     strings.TrimSpace(s.Email),
		SettingProvider:  strings.TrimSpace(s.Provider),
		SettingRenewDays: strconv.Itoa(s.RenewDays),
	}
	if values[SettingCA] == "" {
		values[SettingCA] = DefaultCADir
	}
	if values[SettingProvider] == "" {
		values[SettingProvider] = "manual"
	}
	if values[SettingRenewDays] == "" || values[SettingRenewDays] == "0" {
		values[SettingRenewDays] = strconv.Itoa(DefaultRenewDays)
	}
	if dnsConfig != nil {
		if len(dnsConfig) == 0 {
			values[SettingDNSConfig] = ""
		} else {
			// Merge with the stored credentials so a partially filled form
			// does not silently drop the other fields.
			if current, err := m.LoadSettings(ctx); err == nil {
				for k, v := range current.DNSConfig {
					if _, ok := dnsConfig[k]; !ok {
						dnsConfig[k] = v
					}
				}
			}
			b, _ := json.Marshal(dnsConfig)
			token, err := certcrypto.EncryptString(m.key, string(b))
			if err != nil {
				return err
			}
			values[SettingDNSConfig] = token
		}
	}
	return m.st.UpdateSettings(ctx, values)
}

// obtain runs a DNS-01 order using the supplied CSR. It returns the PEM
// encoded certificate chain.
func (m *Manager) obtain(ctx context.Context, j *job, domains []string, csr *x509.CertificateRequest) (string, error) {
	settings, err := m.LoadSettings(ctx)
	if err != nil {
		return "", err
	}
	accountKey, err := m.accountKey(ctx, settings.CA, settings.Email)
	if err != nil {
		return "", err
	}
	client := &acme.Client{
		Key: accountKey, DirectoryURL: settings.CA, UserAgent: "MikvAcePanel",
		HTTPClient: &http.Client{Timeout: 45 * time.Second},
	}
	acct := &acme.Account{}
	if settings.Email != "" {
		acct.Contact = []string{"mailto:" + settings.Email}
	}
	if _, err := client.Register(ctx, acct, acme.AcceptTOS); err != nil {
		return "", fmt.Errorf("ACME account: %w", err)
	}
	order, err := client.AuthorizeOrder(ctx, acme.DomainIDs(domains...))
	if err != nil {
		return "", fmt.Errorf("ACME order: %w", err)
	}

	type pendingAuth struct {
		auth    *acme.Authorization
		chal    *acme.Challenge
		keyAuth string
		fqdn    string
		value   string
	}
	var pending []pendingAuth
	for _, authURL := range order.AuthzURLs {
		auth, err := client.GetAuthorization(ctx, authURL)
		if err != nil {
			return "", fmt.Errorf("ACME authorization: %w", err)
		}
		if auth.Status == acme.StatusValid {
			continue
		}
		var chal *acme.Challenge
		for _, c := range auth.Challenges {
			if c.Type == "dns-01" {
				chal = c
				break
			}
		}
		if chal == nil {
			return "", fmt.Errorf("CA did not offer a DNS-01 challenge for %s", auth.Identifier.Value)
		}
		value, err := client.DNS01ChallengeRecord(chal.Token)
		if err != nil {
			return "", err
		}
		keyAuth, err := keyAuthorization(accountKey, chal.Token)
		if err != nil {
			return "", err
		}
		pending = append(pending, pendingAuth{
			auth: auth, chal: chal, keyAuth: keyAuth,
			fqdn:  "_acme-challenge." + strings.TrimPrefix(auth.Identifier.Value, "*."),
			value: value,
		})
	}

	if len(pending) > 0 {
		var records []ManualRecord
		for _, p := range pending {
			records = append(records, ManualRecord{Name: p.fqdn, Value: p.value})
		}
		if settings.Provider == "" || settings.Provider == "manual" {
			man := &ManualChallenge{
				NodeID: j.nodeID, Domains: domains, Records: records,
				CreatedAt: time.Now().UTC(), confirm: make(chan struct{}),
			}
			m.mu.Lock()
			j.manual = man
			m.mu.Unlock()
			_ = m.st.SetNodeCertStatus(ctx, j.nodeID, "awaiting_txt", "")
			m.st.AddEvent(ctx, "cert.challenge", j.nodeID, "info",
				"等待手动添加 DNS TXT 记录："+records[0].Name)
			select {
			case <-man.confirm:
			case <-ctx.Done():
				_ = m.st.SetNodeCertStatus(context.Background(), j.nodeID, "error", "manual challenge cancelled")
				return "", ctx.Err()
			}
			_ = m.st.SetNodeCertStatus(ctx, j.nodeID, "validating", "")
			if m.txtWait > 0 {
				if err := waitForTXT(ctx, records, m.txtWait); err != nil {
					return "", err
				}
			}
		} else {
			provider, err := newDNSProvider(settings.Provider, settings.DNSConfig)
			if err != nil {
				return "", err
			}
			defer func() {
				cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				for _, p := range pending {
					_ = provider.CleanUp(cleanupCtx, p.auth.Identifier.Value, p.chal.Token, p.keyAuth)
				}
			}()
			for _, p := range pending {
				if err := provider.Present(ctx, p.auth.Identifier.Value, p.chal.Token, p.keyAuth); err != nil {
					return "", fmt.Errorf("DNS provider: %w", err)
				}
			}
			if m.txtWait > 0 {
				if err := waitForTXT(ctx, records, m.txtWait); err != nil {
					return "", err
				}
			}
		}
		for _, p := range pending {
			if _, err := client.Accept(ctx, p.chal); err != nil {
				return "", fmt.Errorf("ACME challenge: %w", err)
			}
			if _, err := client.WaitAuthorization(ctx, p.auth.URI); err != nil {
				return "", fmt.Errorf("ACME authorization: %w", err)
			}
		}
	}

	der, _, err := client.CreateOrderCert(ctx, order.FinalizeURL, csr.Raw, true)
	if err != nil {
		return "", fmt.Errorf("ACME finalize: %w", err)
	}
	var b strings.Builder
	for _, part := range der {
		_ = pem.Encode(&b, &pem.Block{Type: "CERTIFICATE", Bytes: part})
	}
	return b.String(), nil
}

// accountKey loads or creates the persisted ACME account key.
func (m *Manager) accountKey(ctx context.Context, ca, email string) (*ecdsa.PrivateKey, error) {
	acc, err := m.st.GetAcmeAccount(ctx, ca, email)
	if err == nil {
		plain, err := certcrypto.Decrypt(m.key, acc.KeyCipher, acc.KeyNonce)
		if err != nil {
			return nil, fmt.Errorf("decrypt ACME account key: %w", err)
		}
		key, err := parseECKey(plain)
		if err != nil {
			return nil, err
		}
		return key, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	ct, nonce, err := certcrypto.Encrypt(m.key, pemKey)
	if err != nil {
		return nil, err
	}
	acc = &model.ACMEAccount{CAURL: ca, Email: email, KeyCipher: ct, KeyNonce: nonce}
	if err := m.st.SaveAcmeAccount(ctx, acc); err != nil {
		// A concurrent issuance may have created the account first.
		existing, getErr := m.st.GetAcmeAccount(ctx, ca, email)
		if getErr != nil {
			return nil, err
		}
		plain, decErr := certcrypto.Decrypt(m.key, existing.KeyCipher, existing.KeyNonce)
		if decErr != nil {
			return nil, decErr
		}
		return parseECKey(plain)
	}
	return key, nil
}

func parseECKey(pemBytes []byte) (*ecdsa.PrivateKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("invalid ACME account key")
	}
	if k, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if ec, ok := k.(*ecdsa.PrivateKey); ok {
			return ec, nil
		}
		return nil, errors.New("ACME account key is not ECDSA")
	}
	return x509.ParseECPrivateKey(block.Bytes)
}

// keyAuthorization builds token.thumbprint for DNS providers that need it.
func keyAuthorization(key *ecdsa.PrivateKey, token string) (string, error) {
	pub := key.Public().(*ecdsa.PublicKey)
	if pub.Curve != elliptic.P256() {
		return "", errors.New("only P-256 ACME account keys are supported")
	}
	size := (pub.Curve.Params().BitSize + 7) / 8
	x := base64.RawURLEncoding.EncodeToString(pub.X.FillBytes(make([]byte, size)))
	y := base64.RawURLEncoding.EncodeToString(pub.Y.FillBytes(make([]byte, size)))
	jwk := fmt.Sprintf(`{"crv":"P-256","kty":"EC","x":"%s","y":"%s"}`, x, y)
	sum := sha256.Sum256([]byte(jwk))
	thumb := base64.RawURLEncoding.EncodeToString(sum[:])
	return token + "." + thumb, nil
}
