package config

import (
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
