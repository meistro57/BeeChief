package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/hosts"
)

func TestRedactSecrets(t *testing.T) {
	raw := strings.Join([]string{
		"Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.secret",
		"OPENAI_API_KEY=sk-proj-1234567890abcdefghijklmnop",
		"token: ghp_1234567890abcdef1234567890abcdef",
		"password=\"hunter2secret\"",
		"db=postgres://admin:supersecret@db.internal:5432/app",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA...\n-----END RSA PRIVATE KEY-----",
	}, "\n")

	got := Redact(raw)
	for _, forbidden := range []string{
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9",
		"sk-proj-1234567890abcdefghijklmnop",
		"ghp_1234567890abcdef1234567890abcdef",
		"hunter2secret",
		"supersecret",
		"MIIEpAIBAAKCAQEA",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("Redact leaked %q in:\n%s", forbidden, got)
		}
	}
	if !strings.Contains(got, RedactionToken) {
		t.Errorf("expected %s in output:\n%s", RedactionToken, got)
	}
}

type fakeExec struct{}

func (fakeExec) Run(_ context.Context, cmd string, args ...string) (hosts.Result, error) {
	key := cmd + " " + strings.Join(args, " ")
	switch {
	case cmd == "true":
		return hosts.Result{ExitCode: 0}, nil
	case strings.HasPrefix(key, "openclaw --version"):
		return hosts.Result{Stdout: "OpenClaw 2026.10.5"}, nil
	case strings.HasPrefix(key, "openclaw health"):
		return hosts.Result{Stdout: `{"ok":true}`}, nil
	case strings.HasPrefix(key, "openclaw agents list"):
		return hosts.Result{Stdout: `[{"id":"scout"}]`}, nil
	case strings.HasPrefix(key, "openclaw skills check"):
		return hosts.Result{Stdout: `{"summary":{"total":4,"eligible":3,"disabled":1,"missingRequirements":0}}`}, nil
	case strings.HasPrefix(key, "openclaw skills workshop list"):
		return hosts.Result{Stdout: `{"skills":[{"name":"auto-triage"}]}`}, nil
	default:
		return hosts.Result{ExitCode: 127, Stderr: "not found"}, nil
	}
}

func TestCollectRenderAndExportWithPartialOfflineHives(t *testing.T) {
	cfg := config.Config{
		Hives: []config.Hive{
			{Name: "online-hive", Type: config.TypeLocal},
			{Name: "offline-hive", Type: config.TypeSSH, Host: "10.99.0.1"},
		},
	}
	inv := agents.NewInventory()
	inv.Record(agents.HiveStatus{Hive: "online-hive"}, []agents.Bee{
		{ID: "scout", Name: "Scout", Runtime: "openrouter/auto"},
	})
	inv.Record(agents.HiveStatus{Hive: "offline-hive", Err: errors.New("ssh connection refused with password=leakedpass")}, nil)

	execs := map[string]hosts.Execution{
		"online-hive": fakeExec{},
		// offline-hive intentionally nil to simulate unreachable/unconfigured execution
	}

	hr := Collect(context.Background(), cfg, inv, execs)
	hr.GeneratedAt = time.Date(2026, 11, 3, 20, 0, 0, 0, time.UTC)

	outPath, err := Export(hr, filepath.Join(t.TempDir(), "honey.md"))
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	md := string(data)

	for _, want := range []string{
		"# BeeChief Honey Report",
		"online-hive",
		"offline-hive",
		"Scout",
		"auto-triage",
		"## Unavailable Checks",
		RedactionToken,
	} {
		if !strings.Contains(md, want) {
			t.Errorf("report missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "leakedpass") {
		t.Errorf("report leaked secret from scan error:\n%s", md)
	}
}
