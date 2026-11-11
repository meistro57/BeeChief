package app

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/events"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/swarm"
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

type staticExec struct{ out string }

func (s staticExec) Run(context.Context, string, ...string) (hosts.Result, error) {
	return hosts.Result{Stdout: s.out}, nil
}

func TestBackendRecordsOperationalEvents(t *testing.T) {
	tmp := t.TempDir()
	store := events.NewStore(filepath.Join(tmp, "events.jsonl"), events.DefaultMaxBytes)
	b := &backend{
		cfgPath: filepath.Join(tmp, "hosts.yaml"),
		events:  store,
		execs:   map[string]hosts.Execution{"alpha": staticExec{out: "ok"}},
	}
	bee := agents.Bee{ID: "main", Hive: "alpha"}
	if _, err := b.SendInstruction(context.Background(), bee, "say hello"); err != nil {
		t.Fatalf("SendInstruction: %v", err)
	}
	coord := b.StartSwarmReview(swarm.ModeSelectedBee, []agents.Bee{bee}, nil)
	coord.Run(context.Background())

	kinds := map[events.Kind]bool{}
	for _, ev := range store.Recent(10) {
		kinds[ev.Kind] = true
	}
	if !kinds[events.KindInstruction] {
		t.Errorf("expected an instruction event, got %+v", store.Recent(10))
	}
	if !kinds[events.KindSwarmReview] {
		t.Errorf("expected a swarm_review event, got %+v", store.Recent(10))
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

func TestScanCLIWithNoHives(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(config.EnvConfigPath, filepath.Join(tmp, "hosts.yaml"))
	var out bytes.Buffer
	if err := RunArgs([]string{"scan", "--json"}, &out); err != nil {
		t.Fatalf("scan: %v", err)
	}
	var resp scanResponse
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("decode scan JSON: %v\n%s", err, out.String())
	}
	if resp.Total != 0 || resp.Failed != 0 || len(resp.Hives) != 0 {
		t.Errorf("unexpected scan response: %+v", resp)
	}
}

func TestHealthCLIWithNoHives(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(config.EnvConfigPath, filepath.Join(tmp, "hosts.yaml"))
	var out bytes.Buffer
	if err := RunArgs([]string{"health", "--json"}, &out); err != nil {
		t.Fatalf("health: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("unexpected health output: %q", out.String())
	}
}

func TestCronCLIOnce(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "hosts.yaml")
	jobsPath := filepath.Join(tmp, "jobs.yaml")
	t.Setenv(config.EnvConfigPath, cfgPath)
	t.Setenv(events.EnvEventsPath, filepath.Join(tmp, "events.jsonl"))
	if err := os.WriteFile(jobsPath, []byte("version: 1\njobs:\n  - name: nightly\n    kind: scan\n    schedule: \"0 3 * * *\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := RunArgs([]string{"cron", "--once", "--file", jobsPath}, &out); err != nil {
		t.Fatalf("cron: %v", err)
	}
	if !strings.Contains(out.String(), "Job nightly (scan)") {
		t.Errorf("unexpected cron output: %q", out.String())
	}
}

func TestWebCLIRejectsRemoteBindWithoutOptIn(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv(config.EnvConfigPath, filepath.Join(tmp, "hosts.yaml"))
	var out bytes.Buffer
	if err := RunArgs([]string{"web", "--addr", "0.0.0.0:8099"}, &out); err == nil {
		t.Fatal("expected remote bind to be rejected")
	}
}

func TestIsLoopbackHost(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":   true,
		"::1":         true,
		"localhost":   true,
		"":            false,
		"0.0.0.0":     false,
		"example.com": false,
	}
	for host, want := range cases {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
