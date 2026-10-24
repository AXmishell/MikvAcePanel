// Package celerity converts CELERITY-panel MongoDB backups into a MikvAce
// Panel SQLite database. CELERITY backups are tar.gz archives produced by
// mongodump; this package understands their BSON collections and maps the
// supported user, node, group, admin and subscription settings.
package celerity

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

const metaFile = "celerity-meta.json"

// Collection names used by Mongoose models in CELERITY.
var collectionFiles = map[string]string{
	"admins.bson.gz":       "admins",
	"admins.bson":          "admins",
	"hyusers.bson.gz":      "users",
	"hyusers.bson":         "users",
	"hynodes.bson.gz":      "nodes",
	"hynodes.bson":         "nodes",
	"servergroups.bson.gz": "groups",
	"servergroups.bson":    "groups",
	"settings.bson.gz":     "settings",
	"settings.bson":        "settings",
	"apikeys.bson.gz":      "apikeys",
	"apikeys.bson":         "apikeys",
}

// Options controls fallback values used when an archive does not contain them.
type Options struct {
	FallbackAdminUsername     string
	FallbackAdminPasswordHash string
}

// Summary describes what was imported.
type Summary struct {
	Users        int      `json:"users"`
	Groups       int      `json:"groups"`
	Nodes        int      `json:"nodes"`
	Admins       int      `json:"admins"`
	APIKeys      int      `json:"apiKeys"`
	SkippedUsers int      `json:"skippedUsers"`
	SkippedNodes int      `json:"skippedNodes"`
	Warnings     []string `json:"warnings,omitempty"`
}

// IsBackup reports whether the archive looks like a CELERITY mongodump.
func IsBackup(archivePath string) (bool, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return false, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, fmt.Errorf("not a gzip archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		base := archiveBase(hdr.Name)
		if base == metaFile {
			return true, nil
		}
		if _, ok := collectionFiles[base]; ok {
			return true, nil
		}
	}
}

func archiveBase(name string) string {
	return path.Base(strings.TrimPrefix(filepath.ToSlash(name), "./"))
}

// BuildDatabase reads a CELERITY backup and writes a fully migrated MikvAce
// SQLite database to outPath. The caller should validate and stage the result.
func BuildDatabase(ctx context.Context, archivePath, outPath string, opts Options) (*Summary, error) {
	dir, err := os.MkdirTemp(filepath.Dir(outPath), ".celerity-import-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)

	files, err := extractCollections(archivePath, dir)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("archive does not contain CELERITY collections")
	}
	if files["groups"] == "" && files["nodes"] == "" && files["users"] == "" {
		return nil, fmt.Errorf("archive does not contain CELERITY users, nodes or groups")
	}

	_ = os.Remove(outPath)
	_ = os.Remove(outPath + "-wal")
	_ = os.Remove(outPath + "-shm")
	st, err := store.Open(outPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = st.DB.ExecContext(context.Background(), "PRAGMA wal_checkpoint(TRUNCATE)")
		_ = st.Close()
		_ = os.Remove(outPath + "-wal")
		_ = os.Remove(outPath + "-shm")
	}()
	if err := st.EnsureDefaultSettings(ctx); err != nil {
		return nil, err
	}

	sum := &Summary{}
	groupIDs := map[primitive.ObjectID]int64{}
	nodeIDs := map[primitive.ObjectID]int64{}
	encryptedSecrets := false

	if err := importGroups(ctx, st, files["groups"], groupIDs, sum); err != nil {
		return nil, err
	}
	if err := importNodes(ctx, st, files["nodes"], groupIDs, nodeIDs, sum, &encryptedSecrets); err != nil {
		return nil, err
	}
	if err := importUsers(ctx, st, files["users"], groupIDs, nodeIDs, sum); err != nil {
		return nil, err
	}
	if err := importAdmins(ctx, st, files["admins"], opts, sum, &encryptedSecrets); err != nil {
		return nil, err
	}
	if err := importAPIKeys(ctx, st, files["apikeys"], sum); err != nil {
		return nil, err
	}
	if err := importSettings(ctx, st, files["settings"]); err != nil {
		return nil, err
	}

	if sum.Admins == 0 {
		return nil, fmt.Errorf("archive contains no usable administrator accounts")
	}
	if encryptedSecrets {
		sum.Warnings = append(sum.Warnings,
			"CELERITY 的 SSH 密码/私钥和 2FA 使用 CELERITY 加密密钥保存，未导入；请重新填写 SSH 凭据并按需重新启用 2FA。")
	}
	if sum.Nodes > 0 {
		sum.Warnings = append(sum.Warnings,
			"CELERITY 节点令牌不可复用，已为每个节点生成新的 MikvAce Agent 令牌；需要在节点上重新安装 MikvAce Agent。")
	}
	return sum, nil
}

func extractCollections(archivePath, dir string) (map[string]string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("not a gzip archive")
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	files := map[string]string{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != tar.TypeRegA {
			continue
		}
		base := archiveBase(hdr.Name)
		name, ok := collectionFiles[base]
		if !ok || files[name] != "" {
			continue
		}
		dest := filepath.Join(dir, name+".bson")
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, err
		}
		var copyErr, closeErr error
		if strings.HasSuffix(base, ".gz") {
			gr, err := gzip.NewReader(tr)
			if err != nil {
				_ = out.Close()
				return nil, fmt.Errorf("open %s: %w", hdr.Name, err)
			}
			_, copyErr = io.Copy(out, gr)
			closeErr = gr.Close()
		} else {
			_, copyErr = io.Copy(out, tr)
		}
		outCloseErr := out.Close()
		if copyErr != nil {
			return nil, copyErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if outCloseErr != nil {
			return nil, outCloseErr
		}
		files[name] = dest
	}
	return files, nil
}

func decodeCollection[T any](file string, fn func(T) error) error {
	if file == "" {
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	defer f.Close()
	var sizeBuf [4]byte
	for {
		if _, err := io.ReadFull(f, sizeBuf[:]); err != nil {
			if err == io.EOF {
				return nil
			}
			return fmt.Errorf("read %s: %w", filepath.Base(file), err)
		}
		size := int(binary.LittleEndian.Uint32(sizeBuf[:]))
		if size < 5 || size > 64<<20 {
			return fmt.Errorf("invalid BSON document size %d in %s", size, filepath.Base(file))
		}
		raw := make([]byte, size)
		copy(raw, sizeBuf[:])
		if _, err := io.ReadFull(f, raw[4:]); err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(file), err)
		}
		var doc T
		if err := bson.Unmarshal(raw, &doc); err != nil {
			return fmt.Errorf("decode %s: %w", filepath.Base(file), err)
		}
		if err := fn(doc); err != nil {
			return err
		}
	}
}

type groupDoc struct {
	ID                primitive.ObjectID `bson:"_id"`
	Name              string             `bson:"name"`
	Description       string             `bson:"description"`
	Color             string             `bson:"color"`
	MaxDevices        int                `bson:"maxDevices"`
	SubscriptionTitle string             `bson:"subscriptionTitle"`
	CreatedAt         time.Time          `bson:"createdAt"`
}

func importGroups(ctx context.Context, st *store.Store, file string, ids map[primitive.ObjectID]int64, sum *Summary) error {
	return decodeCollection(file, func(doc groupDoc) error {
		name := strings.TrimSpace(doc.Name)
		if name == "" {
			return nil
		}
		created := doc.CreatedAt
		if created.IsZero() {
			created = time.Now().UTC()
		}
		g, err := st.CreateGroup(ctx, &model.Group{
			Name:              name,
			Description:       doc.Description,
			Color:             doc.Color,
			SubscriptionTitle: doc.SubscriptionTitle,
			MaxDevices:        doc.MaxDevices,
		})
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				sum.Warnings = append(sum.Warnings, "跳过同名分组："+name)
				return nil
			}
			return err
		}
		ids[doc.ID] = g.ID
		if _, err := st.DB.ExecContext(ctx, `UPDATE panel_groups SET created_at = ?, updated_at = ? WHERE id = ?`,
			created.UTC().Format(time.RFC3339Nano), created.UTC().Format(time.RFC3339Nano), g.ID); err != nil {
			return err
		}
		sum.Groups++
		return nil
	})
}

type outboundDoc struct {
	Name     string `bson:"name"`
	Type     string `bson:"type"`
	Addr     string `bson:"addr"`
	Username string `bson:"username"`
	Password string `bson:"password"`
	Insecure bool   `bson:"insecure"`
}

type xrayDoc struct {
	Listen            string   `bson:"listen"`
	Transport         string   `bson:"transport"`
	Security          string   `bson:"security"`
	Flow              string   `bson:"flow"`
	Fingerprint       string   `bson:"fingerprint"`
	ALPN              []string `bson:"alpn"`
	RealityDest       string   `bson:"realityDest"`
	RealitySNI        []string `bson:"realitySni"`
	RealityPrivateKey string   `bson:"realityPrivateKey"`
	RealityPublicKey  string   `bson:"realityPublicKey"`
	RealityShortIDs   []string `bson:"realityShortIds"`
	RealitySpiderX    string   `bson:"realitySpiderX"`
	WSPath            string   `bson:"wsPath"`
	WSHost            string   `bson:"wsHost"`
	GRPCServiceName   string   `bson:"grpcServiceName"`
	APIPort           int      `bson:"apiPort"`
	InboundTag        string   `bson:"inboundTag"`
	TLSSource         string   `bson:"tlsSource"`
}

type nodeDoc struct {
	ID                   primitive.ObjectID `bson:"_id"`
	Type                 string             `bson:"type"`
	Name                 string             `bson:"name"`
	Flag                 string             `bson:"flag"`
	IP                   string             `bson:"ip"`
	Domain               string             `bson:"domain"`
	SNI                  string             `bson:"sni"`
	Port                 int                `bson:"port"`
	PortRange            string             `bson:"portRange"`
	SubscriptionVariants string             `bson:"subscriptionVariants"`
	HopInterval          string             `bson:"hopInterval"`
	Obfs                 struct {
		Type     string `bson:"type"`
		Password string `bson:"password"`
	} `bson:"obfs"`
	ACME struct {
		Email string `bson:"email"`
	} `bson:"acme"`
	Masquerade struct {
		Type  string `bson:"type"`
		Proxy struct {
			URL string `bson:"url"`
		} `bson:"proxy"`
	} `bson:"masquerade"`
	Bandwidth struct {
		Up   string `bson:"up"`
		Down string `bson:"down"`
	} `bson:"bandwidth"`
	StatsPort   int                  `bson:"statsPort"`
	StatsSecret string               `bson:"statsSecret"`
	Groups      []primitive.ObjectID `bson:"groups"`
	SSH         struct {
		Port       int    `bson:"port"`
		Username   string `bson:"username"`
		PrivateKey string `bson:"privateKey"`
		Password   string `bson:"password"`
	} `bson:"ssh"`
	Outbounds []outboundDoc `bson:"outbounds"`
	ACLRules  []string      `bson:"aclRules"`
	Active    bool          `bson:"active"`
	Ranking   float64       `bson:"rankingCoefficient"`
	MaxOnline int           `bson:"maxOnlineUsers"`
	Traffic   struct {
		Tx int64 `bson:"tx"`
		Rx int64 `bson:"rx"`
	} `bson:"traffic"`
	Xray      xrayDoc   `bson:"xray"`
	CreatedAt time.Time `bson:"createdAt"`
}

func importNodes(ctx context.Context, st *store.Store, file string, groupIDs, nodeIDs map[primitive.ObjectID]int64, sum *Summary, encryptedSecrets *bool) error {
	return decodeCollection(file, func(doc nodeDoc) error {
		name := strings.TrimSpace(doc.Name)
		if name == "" || strings.TrimSpace(doc.IP) == "" {
			sum.SkippedNodes++
			return nil
		}
		if doc.SSH.Password != "" || doc.SSH.PrivateKey != "" {
			*encryptedSecrets = true
		}
		nodeType := strings.ToLower(strings.TrimSpace(doc.Type))
		if nodeType == "" {
			nodeType = "hysteria"
		}
		if nodeType != "hysteria" && nodeType != "xray" {
			sum.SkippedNodes++
			sum.Warnings = append(sum.Warnings, "跳过不支持的节点类型 "+doc.Type+"："+name)
			return nil
		}
		x := doc.Xray
		if nodeType == "xray" {
			transport := strings.ToLower(strings.TrimSpace(x.Transport))
			if transport == "" {
				transport = "tcp"
			}
			if transport != "tcp" && transport != "ws" && transport != "grpc" {
				sum.SkippedNodes++
				sum.Warnings = append(sum.Warnings, "跳过不支持的 Xray 传输 "+x.Transport+"："+name)
				return nil
			}
			x.Transport = transport
			lower := strings.ToLower(strings.TrimSpace(x.Security))
			switch lower {
			case "tls", "none", "reality":
				x.Security = lower
			default:
				x.Security = "reality"
			}
			if x.Transport != "tcp" && x.Security == "none" {
				x.Security = "tls"
			}
		}

		portRange := strings.TrimSpace(doc.PortRange)
		if strings.EqualFold(doc.SubscriptionVariants, "tls") {
			portRange = ""
		}
		port := doc.Port
		if port == 0 {
			port = 443
		}
		tlsMode := "selfsigned"
		if strings.TrimSpace(doc.Domain) != "" &&
			(strings.TrimSpace(doc.ACME.Email) != "" || strings.EqualFold(x.TLSSource, "acme")) {
			tlsMode = "acme"
		}
		var groups []int64
		for _, id := range doc.Groups {
			if mapped, ok := groupIDs[id]; ok {
				groups = append(groups, mapped)
			}
		}
		outbounds := make([]model.Outbound, 0, len(doc.Outbounds))
		for _, o := range doc.Outbounds {
			if o.Name == "" || (o.Type != "direct" && o.Type != "socks5" && o.Type != "http") {
				continue
			}
			outbounds = append(outbounds, model.Outbound{
				Name: o.Name, Type: o.Type, Addr: o.Addr,
				Username: o.Username, Password: o.Password, Insecure: o.Insecure,
			})
		}
		masqueradeURL := ""
		if strings.EqualFold(doc.Masquerade.Type, "proxy") {
			masqueradeURL = doc.Masquerade.Proxy.URL
		}
		ranking := doc.Ranking
		if ranking == 0 {
			ranking = 1
		}
		sshPort := doc.SSH.Port
		if sshPort == 0 {
			sshPort = 22
		}
		statsPort := doc.StatsPort
		if statsPort == 0 {
			statsPort = 9999
		}
		token := "mtn_" + store.RandomToken(24)
		n := &model.Node{
			Name: name, Type: nodeType, Flag: doc.Flag,
			IP: strings.TrimSpace(doc.IP), Domain: doc.Domain, SNI: doc.SNI,
			Listen: x.Listen, Port: port, PortRange: portRange, HopInterval: doc.HopInterval,
			ObfsType: doc.Obfs.Type, ObfsPassword: doc.Obfs.Password,
			StatsPort: statsPort, StatsSecret: doc.StatsSecret,
			SSHPort: sshPort, SSHUser: doc.SSH.Username,
			TLSMode: tlsMode, ACMEEmail: doc.ACME.Email,
			BandwidthUp: doc.Bandwidth.Up, BandwidthDown: doc.Bandwidth.Down,
			MasqueradeURL: masqueradeURL, ACL: doc.ACLRules, Outbounds: outbounds,
			Ranking: ranking, Active: doc.Active,
			TrafficTx: doc.Traffic.Tx, TrafficRx: doc.Traffic.Rx,
			Xray: model.XraySettings{
				Transport: x.Transport, Security: x.Security, Flow: x.Flow,
				Fingerprint: x.Fingerprint, ALPN: x.ALPN,
				RealityDest: x.RealityDest, RealitySNI: x.RealitySNI,
				RealityPrivateKey: x.RealityPrivateKey, RealityPublicKey: x.RealityPublicKey,
				RealityShortIDs: x.RealityShortIDs, RealitySpiderX: x.RealitySpiderX,
				WSPath: x.WSPath, WSHost: x.WSHost, GRPCServiceName: x.GRPCServiceName,
				APIPort: x.APIPort, InboundTag: x.InboundTag,
			},
		}
		created, err := st.CreateNode(ctx, n, hashToken(token), token[:12], groups)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				sum.SkippedNodes++
				sum.Warnings = append(sum.Warnings, "跳过冲突节点："+name)
				return nil
			}
			return err
		}
		nodeIDs[doc.ID] = created.ID
		if doc.Traffic.Tx != 0 || doc.Traffic.Rx != 0 {
			if _, err := st.DB.ExecContext(ctx, `UPDATE nodes SET traffic_tx = ?, traffic_rx = ? WHERE id = ?`,
				doc.Traffic.Tx, doc.Traffic.Rx, created.ID); err != nil {
				return err
			}
		}
		if !doc.CreatedAt.IsZero() {
			if _, err := st.DB.ExecContext(ctx, `UPDATE nodes SET created_at = ?, updated_at = ? WHERE id = ?`,
				doc.CreatedAt.UTC().Format(time.RFC3339Nano), doc.CreatedAt.UTC().Format(time.RFC3339Nano), created.ID); err != nil {
				return err
			}
		}
		sum.Nodes++
		return nil
	})
}

type userDoc struct {
	ID                primitive.ObjectID   `bson:"_id"`
	UserID            string               `bson:"userId"`
	SubscriptionToken string               `bson:"subscriptionToken"`
	Username          string               `bson:"username"`
	Password          string               `bson:"password"`
	XrayUUID          string               `bson:"xrayUuid"`
	Enabled           bool                 `bson:"enabled"`
	Groups            []primitive.ObjectID `bson:"groups"`
	Nodes             []primitive.ObjectID `bson:"nodes"`
	Traffic           struct {
		Tx int64 `bson:"tx"`
		Rx int64 `bson:"rx"`
	} `bson:"traffic"`
	TrafficLimit int64      `bson:"trafficLimit"`
	MaxDevices   int        `bson:"maxDevices"`
	ExpireAt     *time.Time `bson:"expireAt"`
	IsProbe      bool       `bson:"isProbe"`
	CreatedAt    time.Time  `bson:"createdAt"`
}

func importUsers(ctx context.Context, st *store.Store, file string, groupIDs, nodeIDs map[primitive.ObjectID]int64, sum *Summary) error {
	return decodeCollection(file, func(doc userDoc) error {
		if doc.IsProbe {
			sum.SkippedUsers++
			sum.Warnings = append(sum.Warnings, "跳过 CELERITY 探针用户："+doc.UserID)
			return nil
		}
		userID := strings.TrimSpace(doc.UserID)
		if userID == "" {
			sum.SkippedUsers++
			return nil
		}
		password := doc.Password
		if password == "" {
			password = store.RandomToken(12)
			sum.Warnings = append(sum.Warnings, "用户 "+userID+" 缺少密码，已生成随机密码。")
		}
		username := strings.TrimSpace(doc.Username)
		if username == "" {
			username = userID
		}
		var groups []int64
		for _, id := range doc.Groups {
			if mapped, ok := groupIDs[id]; ok {
				groups = append(groups, mapped)
			}
		}
		var nodes []int64
		for _, id := range doc.Nodes {
			if mapped, ok := nodeIDs[id]; ok {
				nodes = append(nodes, mapped)
			}
		}
		u := &model.User{
			UserID: userID, Username: username, Password: password,
			SubscriptionToken: doc.SubscriptionToken, XrayUUID: doc.XrayUUID,
			Enabled: doc.Enabled, TrafficTx: doc.Traffic.Tx, TrafficRx: doc.Traffic.Rx,
			TrafficLimit: doc.TrafficLimit, MaxDevices: doc.MaxDevices, ExpireAt: doc.ExpireAt,
		}
		created, err := st.CreateUser(ctx, u, groups, nodes)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				sum.SkippedUsers++
				sum.Warnings = append(sum.Warnings, "跳过冲突用户："+userID)
				return nil
			}
			return err
		}
		if !doc.CreatedAt.IsZero() {
			if _, err := st.DB.ExecContext(ctx, `UPDATE users SET created_at = ?, updated_at = ? WHERE id = ?`,
				doc.CreatedAt.UTC().Format(time.RFC3339Nano), doc.CreatedAt.UTC().Format(time.RFC3339Nano), created.ID); err != nil {
				return err
			}
		}
		sum.Users++
		return nil
	})
}

type adminDoc struct {
	Username     string `bson:"username"`
	PasswordHash string `bson:"passwordHash"`
	TwoFactor    struct {
		Enabled bool `bson:"enabled"`
	} `bson:"twoFactor"`
	CreatedAt time.Time `bson:"createdAt"`
}

func importAdmins(ctx context.Context, st *store.Store, file string, opts Options, sum *Summary, encryptedSecrets *bool) error {
	insert := func(username, hash string, created time.Time) error {
		username = strings.TrimSpace(username)
		hash = strings.TrimSpace(hash)
		if username == "" || hash == "" {
			return nil
		}
		if created.IsZero() {
			created = time.Now().UTC()
		}
		res, err := st.DB.ExecContext(ctx,
			`INSERT OR IGNORE INTO admins (username, password_hash, created_at) VALUES (?, ?, ?)`,
			username, hash, created.UTC().Format(time.RFC3339Nano))
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			sum.Admins++
		} else if username == strings.TrimSpace(opts.FallbackAdminUsername) {
			sum.Warnings = append(sum.Warnings,
				"管理员用户名 "+username+" 已存在，已保留当前面板密码；CELERITY 中的同账号密码未覆盖。")
		}
		return nil
	}
	// Keep the existing panel administrator available even when the archive's
	// admin password is unknown or its username conflicts.
	if err := insert(opts.FallbackAdminUsername, opts.FallbackAdminPasswordHash, time.Now().UTC()); err != nil {
		return err
	}
	return decodeCollection(file, func(doc adminDoc) error {
		if doc.TwoFactor.Enabled {
			*encryptedSecrets = true
		}
		return insert(doc.Username, doc.PasswordHash, doc.CreatedAt)
	})
}

type apiKeyDoc struct {
	Name       string     `bson:"name"`
	KeyHash    string     `bson:"keyHash"`
	KeyPrefix  string     `bson:"keyPrefix"`
	Scopes     []string   `bson:"scopes"`
	RateLimit  int        `bson:"rateLimit"`
	ExpiresAt  *time.Time `bson:"expiresAt"`
	Active     bool       `bson:"active"`
	LastUsedAt *time.Time `bson:"lastUsedAt"`
	CreatedAt  time.Time  `bson:"createdAt"`
}

// CELERITY and MikvAce share the ck_ key format and SHA-256 hashing, so API
// keys can keep working after a migration.
func importAPIKeys(ctx context.Context, st *store.Store, file string, sum *Summary) error {
	supported := map[string]bool{
		"users:read": true, "users:write": true,
		"nodes:read": true, "nodes:write": true,
		"stats:read": true, "sync:write": true,
	}
	return decodeCollection(file, func(doc apiKeyDoc) error {
		name := strings.TrimSpace(doc.Name)
		hash := strings.TrimSpace(doc.KeyHash)
		prefix := strings.TrimSpace(doc.KeyPrefix)
		if name == "" || hash == "" || prefix == "" {
			return nil
		}
		var scopes []string
		for _, sc := range doc.Scopes {
			if supported[sc] {
				scopes = append(scopes, sc)
			}
		}
		if len(scopes) == 0 {
			sum.Warnings = append(sum.Warnings, "跳过没有 MikvAce 可用作用域的 API 密钥："+name)
			return nil
		}
		key, err := st.CreateAPIKey(ctx, name, prefix, hash, scopes, doc.RateLimit, doc.ExpiresAt)
		if err != nil {
			if errors.Is(err, store.ErrConflict) {
				sum.Warnings = append(sum.Warnings, "跳过冲突的 API 密钥："+name)
				return nil
			}
			return err
		}
		if !doc.Active || doc.LastUsedAt != nil || !doc.CreatedAt.IsZero() {
			enabled := 1
			if !doc.Active {
				enabled = 0
			}
			if _, err := st.DB.ExecContext(ctx,
				`UPDATE api_keys SET enabled = ?, last_used_at = ?, created_at = ? WHERE id = ?`,
				enabled, nullTimeValue(doc.LastUsedAt), timeValue(doc.CreatedAt), key.ID); err != nil {
				return err
			}
		}
		sum.APIKeys++
		return nil
	})
}

func timeValue(t time.Time) any {
	if t.IsZero() {
		return time.Now().UTC().Format(time.RFC3339Nano)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func nullTimeValue(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return t.UTC().Format(time.RFC3339Nano)
}

type settingsDoc struct {
	DeviceGracePeriod int `bson:"deviceGracePeriod"`
	Backup            struct {
		Enabled       bool `bson:"enabled"`
		IntervalHours int  `bson:"intervalHours"`
		KeepLast      int  `bson:"keepLast"`
	} `bson:"backup"`
	Subscription struct {
		SupportURL     string `bson:"supportUrl"`
		WebPageURL     string `bson:"webPageUrl"`
		HappProviderID string `bson:"happProviderId"`
		LogoURL        string `bson:"logoUrl"`
		PageTitle      string `bson:"pageTitle"`
		PageNote       string `bson:"pageNote"`
		UpdateInterval int    `bson:"updateInterval"`
		Buttons        []struct {
			Label string `bson:"label"`
			URL   string `bson:"url"`
		} `bson:"buttons"`
		HAPP struct {
			Announce string `bson:"announce"`
		} `bson:"happ"`
	} `bson:"subscription"`
}

func importSettings(ctx context.Context, st *store.Store, file string) error {
	return decodeCollection(file, func(doc settingsDoc) error {
		values := map[string]string{}
		s := doc.Subscription
		if v := strings.TrimSpace(s.SupportURL); v != "" {
			values["support_url"] = v
		}
		if v := strings.TrimSpace(s.WebPageURL); v != "" {
			values["web_page_url"] = v
		}
		if v := strings.TrimSpace(s.HappProviderID); v != "" {
			values["happ_provider_id"] = v
		}
		if v := strings.TrimSpace(s.LogoURL); v != "" {
			values["logo_url"] = v
		}
		if v := strings.TrimSpace(s.PageTitle); v != "" {
			values["page_title"] = v
		}
		if v := strings.TrimSpace(s.PageNote); v != "" {
			values["page_note"] = v
		}
		if v := strings.TrimSpace(s.HAPP.Announce); v != "" {
			values["happ_announce"] = v
		}
		if s.UpdateInterval > 0 {
			values["subscription_update_interval"] = fmt.Sprintf("%d", s.UpdateInterval)
		}
		if len(s.Buttons) > 0 {
			buttons := make([]map[string]string, 0, len(s.Buttons))
			for _, b := range s.Buttons {
				if strings.TrimSpace(b.Label) == "" || strings.TrimSpace(b.URL) == "" {
					continue
				}
				buttons = append(buttons, map[string]string{"label": b.Label, "url": b.URL})
			}
			if len(buttons) > 0 {
				if b, err := json.Marshal(buttons); err == nil {
					values["subscription_buttons"] = string(b)
				}
			}
		}
		if doc.DeviceGracePeriod > 0 {
			values["device_grace_minutes"] = fmt.Sprintf("%d", doc.DeviceGracePeriod)
		}
		values["backup_enabled"] = fmt.Sprintf("%t", doc.Backup.Enabled)
		if doc.Backup.IntervalHours > 0 {
			values["backup_interval_hours"] = fmt.Sprintf("%d", doc.Backup.IntervalHours)
		}
		if doc.Backup.KeepLast > 0 {
			values["backup_keep"] = fmt.Sprintf("%d", doc.Backup.KeepLast)
		}
		if len(values) > 0 {
			if err := st.UpdateSettings(ctx, values); err != nil {
				return err
			}
		}
		return nil
	})
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
