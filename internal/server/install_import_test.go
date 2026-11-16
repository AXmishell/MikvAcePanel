package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"golang.org/x/crypto/bcrypt"

	"github.com/axmishell/mikvacepanel/internal/model"
	"github.com/axmishell/mikvacepanel/internal/store"
)

// buildMikvAceImportArchive writes a minimal panel backup: a consistent SQLite
// snapshot with one administrator, one user and one group, plus the encryption
// key. It mirrors data/backups/mikvace-*.tar.gz.
func buildMikvAceImportArchive(t *testing.T) ([]byte, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "panel.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	hash, err := bcrypt.GenerateFromPassword([]byte("backup-password-123"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.EnsureAdmin(ctx, "backup-admin", string(hash)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(ctx, "admin_security_path", "backup-entry"); err != nil {
		t.Fatal(err)
	}
	group, err := st.CreateGroup(ctx, &model.Group{Name: "Imported Group"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateUser(ctx, &model.User{
		UserID: "imported-user", Username: "imported-user", Password: "secret123",
		SubscriptionToken: "imported-token", Enabled: true,
	}, []int64{group.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	key := bytes.Repeat([]byte{0x2a}, 32)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	now := time.Now().UTC()
	write := func(name string, data []byte) {
		if err := tw.WriteHeader(&tar.Header{
			Name: name, Mode: 0o600, Size: int64(len(data)), ModTime: now, Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write("panel.db", dbBytes)
	write("meta.json", []byte(`{"app":"MikvAce Panel","version":"test"}`))
	write("cert.key", key)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), "backup-entry"
}

// buildCelerityImportArchive includes an administrator so the archive can seed
// a brand-new panel without a fallback account.
func buildCelerityImportArchive(t *testing.T) []byte {
	t.Helper()
	gid := primitive.NewObjectID()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name string, docs []bson.M) {
		var raw bytes.Buffer
		for _, doc := range docs {
			b, err := bson.Marshal(doc)
			if err != nil {
				t.Fatal(err)
			}
			raw.Write(b)
		}
		var z bytes.Buffer
		zw := gzip.NewWriter(&z)
		if _, err := zw.Write(raw.Bytes()); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name: "celerity-backup-test/hysteria/" + name + ".bson.gz",
			Mode: 0o600, Size: int64(z.Len()), ModTime: time.Now().UTC(), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(z.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
	write("servergroups", []bson.M{{"_id": gid, "name": "Migrated"}})
	write("hynodes", []bson.M{{
		"_id": primitive.NewObjectID(), "type": "hysteria", "name": "migrated-node",
		"ip": "203.0.113.20", "port": 8443, "groups": []primitive.ObjectID{gid}, "active": true,
	}})
	write("hyusers", []bson.M{{
		"_id": primitive.NewObjectID(), "userId": "migrated-user", "password": "secret123",
		"enabled": true, "groups": []primitive.ObjectID{gid},
	}})
	write("admins", []bson.M{{
		"_id": primitive.NewObjectID(), "username": "celerity-admin",
		"passwordHash": "$2a$10$abcdefghijklmnopqrstuv",
	}})
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func postInstallImport(t *testing.T, env *testEnv, target string, archive []byte, name string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(archive); err != nil {
		t.Fatal(err)
	}
	if target != "" {
		if err := mw.WriteField("path", target); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest("POST", env.srv.URL+"/api/install/import", &body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestInstallImportMikvAceBackup(t *testing.T) {
	env, dir := newSetupModeEnv(t)
	restarted := make(chan struct{}, 1)
	env.app.cfg.Restart = func() { restarted <- struct{}{} }

	archive, wantEntry := buildMikvAceImportArchive(t)
	target := filepath.Join(dir, "restored.db")
	resp := postInstallImport(t, env, target, archive, "mikvace-test.tar.gz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("import = %d: %s", resp.StatusCode, b)
	}
	out := decode[map[string]any](t, resp)
	if out["ok"] != true || out["restartRequired"] != true || out["source"] != "mikvace" || out["hasAdmin"] != true {
		t.Fatalf("import response = %+v", out)
	}
	if out["adminEntry"] != wantEntry {
		t.Fatalf("adminEntry = %v, want %q", out["adminEntry"], wantEntry)
	}
	summary, _ := out["summary"].(map[string]any)
	if summary["admins"] != float64(1) || summary["users"] != float64(1) || summary["groups"] != float64(1) {
		t.Fatalf("summary = %+v", summary)
	}

	staged := target + ".restore"
	if err := store.ValidateDatabase(staged); err != nil {
		t.Fatalf("staged database: %v", err)
	}
	contents, err := store.InspectDatabase(staged)
	if err != nil {
		t.Fatal(err)
	}
	if contents.Admins != 1 || contents.Users != 1 || contents.Groups != 1 || contents.AdminSecurityPath != wantEntry {
		t.Fatalf("staged contents = %+v", contents)
	}
	key, err := os.ReadFile(filepath.Join(dir, "cert.key.restore"))
	if err != nil || len(key) != 32 {
		t.Fatalf("staged cert.key = %d bytes, err=%v", len(key), err)
	}

	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "driver: sqlite") || !strings.Contains(string(raw), target) {
		t.Fatalf("panel.yaml = \n%s", raw)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("import did not trigger a restart")
	}
}

func TestInstallImportCelerityBackup(t *testing.T) {
	env, dir := newSetupModeEnv(t)
	archive := buildCelerityImportArchive(t)
	target := filepath.Join(dir, "celerity.db")
	resp := postInstallImport(t, env, target, archive, "celerity-backup-20260101-000000.tar.gz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("import = %d: %s", resp.StatusCode, b)
	}
	out := decode[map[string]any](t, resp)
	if out["source"] != "celerity" || out["hasAdmin"] != true {
		t.Fatalf("import response = %+v", out)
	}
	entry, _ := out["adminEntry"].(string)
	if !validAdminSecurityPath(entry) {
		t.Fatalf("generated adminEntry = %q", entry)
	}
	summary, _ := out["summary"].(map[string]any)
	if summary["admins"] != float64(1) || summary["users"] != float64(1) || summary["nodes"] != float64(1) {
		t.Fatalf("summary = %+v", summary)
	}
	staged := target + ".restore"
	contents, err := store.InspectDatabase(staged)
	if err != nil {
		t.Fatal(err)
	}
	if contents.Admins != 1 || contents.AdminSecurityPath != entry {
		t.Fatalf("staged contents = %+v", contents)
	}
}

func TestInstallImportRejectsInvalidArchive(t *testing.T) {
	env, dir := newSetupModeEnv(t)
	resp := postInstallImport(t, env, filepath.Join(dir, "bad.db"), []byte("not a tar.gz"), "bad.tar.gz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("invalid archive = %d: %s", resp.StatusCode, b)
	}
	if _, err := os.Stat(filepath.Join(dir, "bad.db.restore")); !os.IsNotExist(err) {
		t.Fatalf("invalid archive left a staged database: %v", err)
	}
}

func TestInstallImportIntoExistingStoreSetup(t *testing.T) {
	env := newSetupEnv(t)
	restarted := make(chan struct{}, 1)
	env.app.cfg.Restart = func() { restarted <- struct{}{} }

	archive, wantEntry := buildMikvAceImportArchive(t)
	target := filepath.Join(env.app.cfg.DataDir, "imported.db")
	resp := postInstallImport(t, env, target, archive, "mikvace-test.tar.gz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("import = %d: %s", resp.StatusCode, b)
	}
	out := decode[map[string]any](t, resp)
	if out["source"] != "mikvace" || out["hasAdmin"] != true || out["adminEntry"] != wantEntry {
		t.Fatalf("import response = %+v", out)
	}
	if _, err := os.Stat(target + ".restore"); err != nil {
		t.Fatalf("staged database: %v", err)
	}
	raw, err := os.ReadFile(env.app.cfg.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), target) {
		t.Fatalf("panel.yaml = \n%s", raw)
	}
	select {
	case <-restarted:
	case <-time.After(3 * time.Second):
		t.Fatal("import did not trigger a restart")
	}
}

func TestInstallImportBlockedAfterInstall(t *testing.T) {
	env := newSetupEnv(t)
	resp := env.do(t, "POST", "/api/install", "", map[string]any{
		"username": "ops", "password": "a-strong-password-123",
	})
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		t.Fatalf("install = %d: %s", resp.StatusCode, b)
	}
	resp.Body.Close()

	archive, _ := buildMikvAceImportArchive(t)
	resp = postInstallImport(t, env, filepath.Join(env.app.cfg.DataDir, "other.db"), archive, "backup.tar.gz")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("import after install = %d: %s", resp.StatusCode, b)
	}
}
