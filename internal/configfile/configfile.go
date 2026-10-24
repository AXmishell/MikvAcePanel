// Package configfile loads and updates the panel and node YAML configuration.
//
// The configuration file is the only application configuration source. Only
// the file path itself comes from a command-line flag.
package configfile

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load decodes path into out and rejects unknown fields so typos fail fast.
func Load(path string, out any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Exists reports whether path exists.
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// Writable reports whether the process can write path, or create it in its
// parent directory when it does not exist yet.
func Writable(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if _, err := os.Stat(path); err == nil {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return false
		}
		return f.Close() == nil
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".config-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Secret reads a *_file value and trims trailing whitespace/newlines.
func Secret(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// SecretBytes reads a binary secret file.
func SecretBytes(path string) ([]byte, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	return os.ReadFile(path)
}

// Set updates dotted paths (for example "panel.name") in an existing YAML
// document, creating missing mappings and preserving comments on existing
// nodes. The file is written atomically with mode 0600.
func Set(path string, values map[string]any) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("config path is empty")
	}
	root := &yaml.Node{Kind: yaml.DocumentNode}
	if b, err := os.ReadFile(path); err == nil {
		if err := yaml.Unmarshal(b, root); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(root.Content) == 0 || root.Content[0].Kind == 0 {
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: top-level document must be a mapping", path)
	}
	if findValue(doc, "version") == nil {
		setPath(doc, []string{"version"}, 1)
	}
	for key, value := range values {
		setPath(doc, strings.Split(key, "."), value)
	}
	b, err := yaml.Marshal(root)
	if err != nil {
		return err
	}
	return writeAtomic(path, b)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		// A Docker single-file bind mount cannot be replaced by rename. Fall
		// back to an in-place write; the config is small and only updated by
		// /install.
		if werr := os.WriteFile(path, data, mode); werr == nil {
			return nil
		}
		return err
	}
	return nil
}

func setPath(node *yaml.Node, path []string, value any) {
	if len(path) == 0 || node == nil {
		return
	}
	if node.Kind != yaml.MappingNode {
		node.Kind = yaml.MappingNode
		node.Tag = "!!map"
		node.Content = nil
	}
	existing := findValue(node, path[0])
	if len(path) == 1 {
		encoded := encode(value)
		if existing != nil {
			// Keep the key node (and its comments), replace only the value.
			for i := 0; i+1 < len(node.Content); i += 2 {
				if node.Content[i].Value == path[0] {
					node.Content[i+1] = encoded
					return
				}
			}
		}
		node.Content = append(node.Content, scalar(path[0]), encoded)
		return
	}
	if existing == nil {
		child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		node.Content = append(node.Content, scalar(path[0]), child)
		existing = child
	}
	setPath(existing, path[1:], value)
}

func findValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}

func encode(v any) *yaml.Node {
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return scalar(fmt.Sprint(v))
	}
	return n
}
