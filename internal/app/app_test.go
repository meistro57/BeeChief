package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meistro57/beechief/internal/config"
)

func TestDiagnoseExportCLI(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "hosts.yaml")
	t.Setenv(config.EnvConfigPath, cfgPath)

	outFile := filepath.Join(tmp, "report.md")
	var stdout bytes.Buffer
	if err := RunArgs([]string{"diagnose", "--export", "--output", outFile}, &stdout); err != nil {
		t.Fatalf("RunArgs(diagnose --export): %v", err)
	}
	if !strings.Contains(stdout.String(), outFile) {
		t.Errorf("expected output path printed to stdout, got %q", stdout.String())
	}
	data, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("expected report file written at %s: %v", outFile, err)
	}
	if !strings.Contains(string(data), "# BeeChief Honey Report") {
		t.Errorf("unexpected report content:\n%s", string(data))
	}
}

func TestVersionCLI(t *testing.T) {
	for _, arg := range []string{"version", "--version", "-v"} {
		var stdout bytes.Buffer
		if err := RunArgs([]string{arg}, &stdout); err != nil {
			t.Fatalf("RunArgs(%q): %v", arg, err)
		}
		if !strings.HasPrefix(stdout.String(), "beechief v") {
			t.Errorf("RunArgs(%q) output = %q, want prefix 'beechief v'", arg, stdout.String())
		}
	}
}
