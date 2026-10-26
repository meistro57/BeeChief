// Package config loads BeeChief's Hive configuration.
//
// Hives are configured; Bees (agents) are discovered. This package therefore
// only ever describes machines and connection details, never agent names.
//
// The configuration never holds passwords or key material: SSH Hives reuse
// the operator's existing ~/.ssh/config, agent, and keys.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// HiveType selects how BeeChief reaches a Hive.
type HiveType string

const (
	// TypeLocal runs commands on the machine hosting BeeChief.
	TypeLocal HiveType = "local"
	// TypeSSH runs commands over the system ssh client.
	TypeSSH HiveType = "ssh"
)

// EnvConfigPath overrides the default configuration file location.
const EnvConfigPath = "BEECHIEF_CONFIG"

// Hive describes a single machine BeeChief can reach.
type Hive struct {
	// Name is a stable operator-chosen identifier (e.g. "basement", "vps").
	Name string `yaml:"name" json:"name"`
	// Type is "local" or "ssh". When omitted it is inferred from Host:
	// an empty Host or "local" means local, anything else means ssh.
	Type HiveType `yaml:"type,omitempty" json:"type,omitempty"`
	// Host is an SSH target: a hostname, an alias from ~/.ssh/config, or
	// "user@host". Ignored for local Hives.
	Host string `yaml:"host,omitempty" json:"host,omitempty"`
	// User is an optional SSH login user. It overrides any "user@" in Host.
	User string `yaml:"user,omitempty" json:"user,omitempty"`
	// Port is an optional SSH port. Zero defers to the SSH config.
	Port int `yaml:"port,omitempty" json:"port,omitempty"`
	// Label is an optional human-friendly description.
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
}

// Kind returns the Hive's effective type, applying inference when Type is
// unset.
func (h Hive) Kind() HiveType {
	if h.Type != "" {
		return HiveType(strings.ToLower(string(h.Type)))
	}
	if h.Host == "" || h.Host == "local" {
		return TypeLocal
	}
	return TypeSSH
}

// Validate reports the first problem with a single Hive entry.
func (h Hive) Validate() error {
	if strings.TrimSpace(h.Name) == "" {
		return errors.New("hive name is required")
	}
	switch h.Kind() {
	case TypeLocal:
	case TypeSSH:
		if strings.TrimSpace(h.Host) == "" {
			return fmt.Errorf("hive %q: type ssh requires host", h.Name)
		}
		if strings.HasPrefix(h.Host, "-") || strings.HasPrefix(h.User, "-") {
			return fmt.Errorf("hive %q: host and user must not start with '-'", h.Name)
		}
		if h.Port < 0 || h.Port > 65535 {
			return fmt.Errorf("hive %q: port %d out of range", h.Name, h.Port)
		}
	default:
		return fmt.Errorf("hive %q: unknown type %q (want local or ssh)", h.Name, h.Type)
	}
	return nil
}

// Config is the root configuration document.
type Config struct {
	Hives []Hive `yaml:"hives" json:"hives"`
}

// Validate checks every Hive and rejects duplicate names.
func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Hives))
	for i, h := range c.Hives {
		if err := h.Validate(); err != nil {
			return fmt.Errorf("hives[%d]: %w", i, err)
		}
		if seen[h.Name] {
			return fmt.Errorf("hives[%d]: duplicate hive name %q", i, h.Name)
		}
		seen[h.Name] = true
	}
	return nil
}

// Default returns an empty configuration with no Hives configured.
//
// BeeChief must start cleanly with zero Hives: discovery simply finds nothing
// rather than failing.
func Default() Config {
	return Config{Hives: []Hive{}}
}

// Parse decodes and validates a YAML configuration document. Unknown keys
// are rejected so typos surface instead of being silently ignored.
func Parse(data []byte) (Config, error) {
	cfg := Default()
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Hives == nil {
		cfg.Hives = []Hive{}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, nil
}

// Load reads and parses the configuration file at path.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	cfg, err := Parse(data)
	if err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

// LoadOrDefault is Load, except a missing file yields Default() so a fresh
// install starts cleanly. A present-but-invalid file is still an error.
func LoadOrDefault(path string) (Config, error) {
	cfg, err := Load(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	return cfg, err
}

// DefaultPath returns $BEECHIEF_CONFIG if set, otherwise
// <user config dir>/beechief/hosts.yaml (e.g. ~/.config/beechief/hosts.yaml).
func DefaultPath() string {
	if p := os.Getenv(EnvConfigPath); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "hosts.yaml"
	}
	return filepath.Join(dir, "beechief", "hosts.yaml")
}
