package diagnostics

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/hosts"
)

type scriptedExec struct {
	handler func(cmd string, args ...string) (hosts.Result, error)
}

func (s *scriptedExec) Run(_ context.Context, cmd string, args ...string) (hosts.Result, error) {
	return s.handler(cmd, args...)
}

func TestRunAggregatesAndIsolatesFailures(t *testing.T) {
	exec := &scriptedExec{
		handler: func(cmd string, args ...string) (hosts.Result, error) {
			key := cmd + " " + strings.Join(args, " ")
			switch {
			case cmd == "true":
				return hosts.Result{ExitCode: 0}, nil
			case strings.HasPrefix(key, "openclaw --version"):
				return hosts.Result{Stdout: "OpenClaw 2026.10.5 — All your chats"}, nil
			case strings.HasPrefix(key, "openclaw health"):
				return hosts.Result{Stdout: `{"ok":true}`}, nil
			case strings.HasPrefix(key, "openclaw agents list"):
				return hosts.Result{Stdout: `[{"id":"alpha"},{"id":"beta"}]`}, nil
			case strings.HasPrefix(key, "df -P /"):
				return hosts.Result{Stdout: "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/sda1 100000000 90000000 10000000 90% /\n"}, nil
			case strings.HasPrefix(key, "free -m"):
				return hosts.Result{Stdout: "              total        used        free\nMem:          16000        8000        8000\nSwap:          4000         100        3900\n"}, nil
			case strings.HasPrefix(key, "openclaw models status"):
				return hosts.Result{Stdout: `{"defaultModel":"openrouter/auto","auth":{"providers":[{"provider":"openrouter","effective":{"kind":"env"}}]}}`}, nil
			case strings.HasPrefix(key, "docker info"):
				// Simulate Docker not installed on this Hive -> must be StateUnavailable, not crash
				return hosts.Result{ExitCode: 127, Stderr: "docker: not found"}, nil
			case strings.HasPrefix(key, "systemctl is-system-running"):
				return hosts.Result{Stdout: "running\n"}, nil
			case strings.HasPrefix(key, "systemctl --failed"):
				return hosts.Result{Stdout: "openclaw-worker.service loaded failed failed Worker\n"}, nil
			default:
				return hosts.Result{ExitCode: 1, Stderr: "unexpected: " + key}, nil
			}
		},
	}

	rep := Run(context.Background(), "alpha-hive", exec)
	if len(rep.Checks) != 11 {
		t.Fatalf("checks count: got %d, want 11", len(rep.Checks))
	}
	pass, warn, fail, unavail := rep.Counts()
	if fail != 0 {
		t.Errorf("expected 0 fails, got %d (%+v)", fail, rep.Checks)
	}
	if warn != 2 { // disk 90% warning + 1 failed systemd unit warning
		t.Errorf("expected 2 warnings, got %d", warn)
	}
	if unavail != 1 { // docker unavailable
		t.Errorf("expected 1 unavailable, got %d", unavail)
	}
	if pass != 8 {
		t.Errorf("expected 8 passes, got %d", pass)
	}

	sorted := rep.SortedBySeverity()
	if sorted[0].State != StateWarning {
		t.Errorf("expected warning first in sorted checks, got %s", sorted[0].State)
	}
}

func TestPanicIsolationAndNilExec(t *testing.T) {
	rep := Run(context.Background(), "broken", nil)
	if len(rep.Checks) != 1 || rep.Checks[0].State != StateFail {
		t.Fatalf("nil exec should produce 1 fail check, got %+v", rep.Checks)
	}

	exec := &scriptedExec{handler: func(string, ...string) (hosts.Result, error) { return hosts.Result{}, nil }}
	rep2 := Run(context.Background(), "panic-hive", exec,
		func(context.Context, hosts.Execution) CheckResult {
			panic("boom")
		},
		func(context.Context, hosts.Execution) CheckResult {
			return CheckResult{Name: "Second check", State: StatePass, Summary: "still ran"}
		},
	)
	if len(rep2.Checks) != 2 {
		t.Fatalf("expected both checks to record results, got %d", len(rep2.Checks))
	}
	if rep2.Checks[0].State != StateFail || rep2.Checks[1].State != StatePass {
		t.Errorf("unexpected states after panic isolation: %+v", rep2.Checks)
	}
}

func TestRunShortCircuitsAfterUnreachableProbe(t *testing.T) {
	calls := 0
	exec := &scriptedExec{handler: func(string, ...string) (hosts.Result, error) {
		calls++
		return hosts.Result{ExitCode: -1}, &hosts.Error{Hive: "dead", Kind: hosts.KindUnreachable}
	}}
	start := time.Now()
	rep := Run(context.Background(), "dead", exec)
	if calls != 1 {
		t.Errorf("expected a single reachability probe, got %d calls", calls)
	}
	if len(rep.Checks) != 11 {
		t.Fatalf("checks count: got %d, want 11", len(rep.Checks))
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("unreachable pass took too long: %v", time.Since(start))
	}
	if rep.Checks[0].State != StateFail {
		t.Errorf("first check should fail reachability, got %+v", rep.Checks[0])
	}
}
