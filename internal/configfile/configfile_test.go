package configfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testConfig struct {
	Version int `yaml:"version"`
	Panel   struct {
		Name      string `yaml:"name"`
		PublicURL string `yaml:"public_url"`
	} `yaml:"panel"`
	Admin struct {
		Username     string `yaml:"username"`
		PasswordFile string `yaml:"password_file"`
	} `yaml:"admin"`
}

func TestLoadStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "panel.yaml")
	if err := os.WriteFile(path, []byte("version: 1\npanel:\n  name: Test\nunknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var cfg testConfig
	if err := Load(path, &cfg); err == nil {
		t.Fatal("unknown field must fail")
	}
	if err := os.WriteFile(path, []byte("version: 1\npanel:\n  name: Test\n  public_url: https://x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Panel.Name != "Test" || cfg.Panel.PublicURL != "https://x" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestSetCreatesAndPreserves(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "panel.yaml")
	if err := os.WriteFile(path, []byte("# keep me\nversion: 1\npanel:\n  name: Old\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Set(path, map[string]any{
		"panel.name":       "New",
		"panel.public_url": "https://panel.example.com",
		"admin.username":   "ops",
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(b)
	for _, want := range []string{"# keep me", "name: New", "public_url: https://panel.example.com", "username: ops", "version: 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in:\n%s", want, text)
		}
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %o", st.Mode().Perm())
	}
	// A second update must not duplicate keys.
	if err := Set(path, map[string]any{"panel.name": "Again"}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(path)
	if strings.Count(string(b), "  name:") != 1 {
		t.Fatalf("duplicated key:\n%s", b)
	}
}

func TestSetCreatesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "panel.yaml")
	if err := Set(path, map[string]any{"panel.name": "Fresh"}); err != nil {
		t.Fatal(err)
	}
	var cfg testConfig
	if err := Load(path, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Version != 1 || cfg.Panel.Name != "Fresh" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestExistsIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	if Exists(dir) {
		t.Fatal("a directory must not count as an existing config file")
	}
	path := filepath.Join(dir, "panel.yaml")
	if err := os.WriteFile(path, []byte("version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !Exists(path) {
		t.Fatal("regular file should exist")
	}
}

func TestSecretAndWritable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("  abc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Secret(path)
	if err != nil || got != "abc" {
		t.Fatalf("secret = %q %v", got, err)
	}
	if !Writable(path) || !Writable(filepath.Join(dir, "new.yaml")) {
		t.Fatal("file should be writable")
	}
}
