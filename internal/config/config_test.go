package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `
hives:
  - name: work-pc
    type: local

  - name: qmu
    type: ssh
    host: qmu-server
    user: root
`

func TestParseSample(t *testing.T) {
	cfg, err := Parse([]byte(sample))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(cfg.Hives) != 2 {
		t.Fatalf("hives: got %d, want 2", len(cfg.Hives))
	}
	local, remote := cfg.Hives[0], cfg.Hives[1]
	if local.Name != "work-pc" || local.Kind() != TypeLocal {
		t.Errorf("local hive: %+v", local)
	}
	if remote.Name != "qmu" || remote.Kind() != TypeSSH || remote.Host != "qmu-server" || remote.User != "root" {
		t.Errorf("ssh hive: %+v", remote)
	}
}

func TestParseEmpty(t *testing.T) {
	for _, doc := range []string{"", "hives: []\n", "# only a comment\n"} {
		cfg, err := Parse([]byte(doc))
		if err != nil {
			t.Fatalf("Parse(%q): %v", doc, err)
		}
		if cfg.Hives == nil || len(cfg.Hives) != 0 {
			t.Errorf("Parse(%q): want empty non-nil hives, got %#v", doc, cfg.Hives)
		}
	}
}

func TestKindInference(t *testing.T) {
	cases := []struct {
		hive Hive
		want HiveType
	}{
		{Hive{Name: "a"}, TypeLocal},
		{Hive{Name: "a", Host: "local"}, TypeLocal},
		{Hive{Name: "a", Host: "root@vps"}, TypeSSH},
		{Hive{Name: "a", Type: "SSH", Host: "vps"}, TypeSSH},
	}
	for _, c := range cases {
		if got := c.hive.Kind(); got != c.want {
			t.Errorf("%+v: got %q, want %q", c.hive, got, c.want)
		}
	}
}

func TestParseRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"missing name":   "hives:\n  - type: local\n",
		"ssh no host":    "hives:\n  - name: x\n    type: ssh\n",
		"bad type":       "hives:\n  - name: x\n    type: telnet\n",
		"duplicate":      "hives:\n  - name: x\n  - name: x\n",
		"unknown field":  "hives:\n  - name: x\n    password: hunter2\n",
		"option inject":  "hives:\n  - name: x\n    type: ssh\n    host: -oProxyCommand=evil\n",
		"bad port":       "hives:\n  - name: x\n    type: ssh\n    host: h\n    port: 70000\n",
		"malformed yaml": "hives: [\n",
	}
	for name, doc := range cases {
		if _, err := Parse([]byte(doc)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestLoadOrDefaultMissingFile(t *testing.T) {
	cfg, err := LoadOrDefault(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("LoadOrDefault: %v", err)
	}
	if len(cfg.Hives) != 0 {
		t.Errorf("want no hives, got %d", len(cfg.Hives))
	}
}

func TestLoadInvalidFileReportsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hosts.yaml")
	if err := os.WriteFile(path, []byte("hives:\n  - type: ssh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrDefault(path)
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatalf("want error mentioning path, got %v", err)
	}
}

func TestExampleConfigParses(t *testing.T) {
	cfg, err := Load(filepath.Join("..", "..", "configs", "hosts.example.yaml"))
	if err != nil {
		t.Fatalf("example config: %v", err)
	}
	if len(cfg.Hives) == 0 {
		t.Fatal("example config has no hives")
	}
}

func TestDefaultPathEnvOverride(t *testing.T) {
	t.Setenv(EnvConfigPath, "/tmp/custom.yaml")
	if got := DefaultPath(); got != "/tmp/custom.yaml" {
		t.Errorf("DefaultPath: got %q", got)
	}
}

func TestConfigMutationAndRoundTripSave(t *testing.T) {
	cfg := Default()
	if err := cfg.AddHive(Hive{Name: "local-pc", Type: TypeLocal, Label: "Workstation"}); err != nil {
		t.Fatalf("AddHive local: %v", err)
	}
	if err := cfg.AddHive(Hive{Name: "remote-1", Type: TypeSSH, Host: "10.0.0.5", User: "admin", Port: 2222}); err != nil {
		t.Fatalf("AddHive ssh: %v", err)
	}
	// Duplicate rejected
	if err := cfg.AddHive(Hive{Name: "local-pc", Type: TypeLocal}); err == nil {
		t.Error("expected duplicate hive error")
	}
	// Update existing
	if err := cfg.UpdateHive("remote-1", Hive{Name: "remote-1", Type: TypeSSH, Host: "10.0.0.6", User: "ops", Port: 22}); err != nil {
		t.Fatalf("UpdateHive: %v", err)
	}
	// Save and preserve permissions
	path := filepath.Join(t.TempDir(), "sub", "hosts.yaml")
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := cfg.RemoveHive("local-pc"); err != nil {
		t.Fatalf("RemoveHive: %v", err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatalf("Save second time: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Errorf("permissions not preserved: got %o, want 0640", info.Mode().Perm())
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Save: %v", err)
	}
	if len(loaded.Hives) != 1 || loaded.Hives[0].Host != "10.0.0.6" || loaded.Hives[0].User != "ops" {
		t.Errorf("round-trip mismatch: %+v", loaded.Hives)
	}
	if loaded.Version != CurrentSchemaVersion {
		t.Errorf("expected version %d after save, got %d", CurrentSchemaVersion, loaded.Version)
	}
}

func TestConfigVersioningAndMigration(t *testing.T) {
	// Legacy v0 schema with top-level "hosts:" and "hostname:"/"username:" aliases
	legacy := `
version: 0
hosts:
  - name: old-box
    type: ssh
    hostname: 192.168.1.50
    username: deploy
    controlPersist: 90s
`
	cfg, migrated, err := Migrate([]byte(legacy))
	if err != nil {
		t.Fatalf("Migrate legacy: %v", err)
	}
	if !migrated {
		t.Error("expected migrated=true for v0 legacy config")
	}
	if cfg.Version != CurrentSchemaVersion {
		t.Errorf("expected migrated version %d, got %d", CurrentSchemaVersion, cfg.Version)
	}
	if len(cfg.Hives) != 1 || cfg.Hives[0].Host != "192.168.1.50" || cfg.Hives[0].User != "deploy" {
		t.Errorf("unexpected migrated hive: %+v", cfg.Hives)
	}

	// Current v1 schema should report migrated=false
	v1Doc := "version: 1\nhives:\n  - name: work\n    type: local\n"
	cfgV1, migratedV1, err := Migrate([]byte(v1Doc))
	if err != nil {
		t.Fatalf("Migrate v1: %v", err)
	}
	if migratedV1 || cfgV1.Version != 1 {
		t.Errorf("expected migrated=false and version=1, got migrated=%v version=%d", migratedV1, cfgV1.Version)
	}

	// Unsupported future version must fail with ErrUnsupportedVersion
	futureDoc := "version: 99\nhives:\n  - name: work\n    type: local\n"
	if _, err := Parse([]byte(futureDoc)); !errors.Is(err, ErrUnsupportedVersion) {
		t.Errorf("expected ErrUnsupportedVersion for version 99, got %v", err)
	}
}
