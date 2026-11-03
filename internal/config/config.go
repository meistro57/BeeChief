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
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// CurrentSchemaVersion is the active configuration schema version.
const CurrentSchemaVersion = 1

// ErrUnsupportedVersion is returned when a configuration file declares a
// schema version newer than this binary understands.
var ErrUnsupportedVersion = errors.New("unsupported config schema version")

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
	// ControlMaster optionally enables or disables SSH connection multiplexing
	// (ControlMaster=auto) for this Hive. Nil defers to the platform default.
	ControlMaster *bool `yaml:"controlMaster,omitempty" json:"controlMaster,omitempty"`
	// ControlPersist optionally overrides the SSH ControlPersist duration
	// (e.g. "60s", "5m") when connection reuse is enabled.
	ControlPersist string `yaml:"controlPersist,omitempty" json:"controlPersist,omitempty"`
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
		if cp := strings.TrimSpace(h.ControlPersist); cp != "" {
			if err := validateControlPersist(cp); err != nil {
				return fmt.Errorf("hive %q: %w", h.Name, err)
			}
		}
	default:
		return fmt.Errorf("hive %q: unknown type %q (want local or ssh)", h.Name, h.Type)
	}
	return nil
}

func validateControlPersist(cp string) error {
	if strings.HasPrefix(cp, "-") {
		return fmt.Errorf("controlPersist %q must not start with '-'", cp)
	}
	if cp == "yes" || cp == "no" {
		return nil
	}
	if secs, err := strconv.Atoi(cp); err == nil && secs > 0 {
		return nil
	}
	if d, err := time.ParseDuration(cp); err == nil && d > 0 {
		return nil
	}
	return fmt.Errorf("invalid controlPersist %q (want positive duration like 60s or 5m)", cp)
}

// Config is the root configuration document.
type Config struct {
	Version int    `yaml:"version,omitempty" json:"version,omitempty"`
	Hives   []Hive `yaml:"hives" json:"hives"`
}

// Validate checks the schema version and every Hive, rejecting duplicate names.
func (c Config) Validate() error {
	if c.Version < 0 || c.Version > CurrentSchemaVersion {
		return fmt.Errorf("%w: got version %d (supported up to %d)", ErrUnsupportedVersion, c.Version, CurrentSchemaVersion)
	}
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
	return Config{Version: CurrentSchemaVersion, Hives: []Hive{}}
}

// rawHiveWire supports both v1 Hive fields and legacy v0 field aliases
// (hostname -> host, username -> user) during migration while still allowing
// KnownFields(true) to reject unknown keys like "password".
type rawHiveWire struct {
	Name           string   `yaml:"name"`
	Type           HiveType `yaml:"type,omitempty"`
	Host           string   `yaml:"host,omitempty"`
	Hostname       string   `yaml:"hostname,omitempty"`
	User           string   `yaml:"user,omitempty"`
	Username       string   `yaml:"username,omitempty"`
	Port           int      `yaml:"port,omitempty"`
	Label          string   `yaml:"label,omitempty"`
	ControlMaster  *bool    `yaml:"controlMaster,omitempty"`
	ControlPersist string   `yaml:"controlPersist,omitempty"`
}

type rawConfigWire struct {
	Version *int          `yaml:"version,omitempty"`
	Hives   []rawHiveWire `yaml:"hives,omitempty"`
	Hosts   []rawHiveWire `yaml:"hosts,omitempty"`
}

// Migrate decodes a YAML configuration document, automatically migrating
// legacy v0 schemas (unversioned documents, top-level `hosts:`, or `hostname` /
// `username` field aliases) to CurrentSchemaVersion. Unknown keys are still
// rejected so typos and stray secrets surface immediately.
func Migrate(data []byte) (Config, bool, error) {
	var wire rawConfigWire
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&wire); err != nil && !errors.Is(err, io.EOF) {
		return Config{}, false, fmt.Errorf("parse config: %w", err)
	}

	migrated := false
	ver := CurrentSchemaVersion
	if wire.Version != nil {
		ver = *wire.Version
		if ver == 0 {
			ver = CurrentSchemaVersion
			migrated = true
		}
	} else if len(wire.Hives) > 0 || len(wire.Hosts) > 0 {
		migrated = true
	}

	if ver < 0 || ver > CurrentSchemaVersion {
		return Config{}, false, fmt.Errorf("invalid config: %w: got version %d (supported up to %d)",
			ErrUnsupportedVersion, ver, CurrentSchemaVersion)
	}

	if len(wire.Hives) > 0 && len(wire.Hosts) > 0 {
		return Config{}, false, errors.New("invalid config: cannot specify both 'hives' and legacy 'hosts'")
	}
	rawList := wire.Hives
	if len(wire.Hosts) > 0 {
		rawList = wire.Hosts
		migrated = true
	}

	hives := make([]Hive, 0, len(rawList))
	for _, rh := range rawList {
		host := rh.Host
		if host == "" && rh.Hostname != "" {
			host = rh.Hostname
			migrated = true
		}
		user := rh.User
		if user == "" && rh.Username != "" {
			user = rh.Username
			migrated = true
		}
		hives = append(hives, Hive{
			Name:           rh.Name,
			Type:           rh.Type,
			Host:           host,
			User:           user,
			Port:           rh.Port,
			Label:          rh.Label,
			ControlMaster:  rh.ControlMaster,
			ControlPersist: rh.ControlPersist,
		})
	}

	cfg := Config{
		Version: ver,
		Hives:   hives,
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, false, fmt.Errorf("invalid config: %w", err)
	}
	return cfg, migrated, nil
}

// Parse decodes, migrates, and validates a YAML configuration document.
// Unknown keys are rejected so typos surface instead of being silently ignored.
func Parse(data []byte) (Config, error) {
	cfg, _, err := Migrate(data)
	return cfg, err
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

// AddHive validates h and appends it to c, rejecting duplicate names.
func (c *Config) AddHive(h Hive) error {
	h.Name = strings.TrimSpace(h.Name)
	h.Host = strings.TrimSpace(h.Host)
	h.User = strings.TrimSpace(h.User)
	h.Label = strings.TrimSpace(h.Label)
	if err := h.Validate(); err != nil {
		return err
	}
	for _, existing := range c.Hives {
		if existing.Name == h.Name {
			return fmt.Errorf("duplicate hive name %q", h.Name)
		}
	}
	c.Hives = append(c.Hives, h)
	return nil
}

// UpdateHive replaces the Hive named oldName with h after validating the
// resulting configuration.
func (c *Config) UpdateHive(oldName string, h Hive) error {
	h.Name = strings.TrimSpace(h.Name)
	h.Host = strings.TrimSpace(h.Host)
	h.User = strings.TrimSpace(h.User)
	h.Label = strings.TrimSpace(h.Label)
	if err := h.Validate(); err != nil {
		return err
	}
	idx := -1
	for i, existing := range c.Hives {
		if existing.Name == oldName {
			idx = i
		} else if existing.Name == h.Name {
			return fmt.Errorf("duplicate hive name %q", h.Name)
		}
	}
	if idx < 0 {
		return fmt.Errorf("hive %q not found", oldName)
	}
	c.Hives[idx] = h
	return nil
}

// RemoveHive deletes the Hive named name from c.
func (c *Config) RemoveHive(name string) error {
	for i, existing := range c.Hives {
		if existing.Name == name {
			c.Hives = append(c.Hives[:i], c.Hives[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("hive %q not found", name)
}

// Marshal encodes c as YAML after validating it.
func Marshal(c Config) ([]byte, error) {
	if c.Version == 0 {
		c.Version = CurrentSchemaVersion
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}
	if c.Hives == nil {
		c.Hives = []Hive{}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

// Save validates and writes c to path, preserving existing file permissions
// when the file already exists (defaulting to 0600 for new files).
func Save(path string, c Config) error {
	data, err := Marshal(c)
	if err != nil {
		return err
	}
	perm := fs.FileMode(0o600)
	if info, statErr := os.Stat(path); statErr == nil {
		perm = info.Mode().Perm()
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create config directory: %w", err)
		}
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	return nil
}
