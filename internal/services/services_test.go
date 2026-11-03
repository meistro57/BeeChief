package services

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/meistro57/beechief/internal/hosts"
)

type fakeExec struct {
	handler func(cmd string, args ...string) (hosts.Result, error)
}

func (f *fakeExec) Run(_ context.Context, cmd string, args ...string) (hosts.Result, error) {
	return f.handler(cmd, args...)
}

func TestSystemdInspectionAndConfirmationGuard(t *testing.T) {
	calledAction := false
	exec := &fakeExec{
		handler: func(cmd string, args ...string) (hosts.Result, error) {
			key := cmd + " " + strings.Join(args, " ")
			switch {
			case key == "systemctl is-system-running":
				return hosts.Result{Stdout: "running\n"}, nil
			case strings.HasPrefix(key, "systemctl list-units"):
				return hosts.Result{Stdout: "" +
					"openclaw-gateway.service loaded active running OpenClaw Gateway\n" +
					"ollama.service loaded failed failed Ollama Daemon\n" +
					"cron.service loaded active running Regular background program\n"}, nil
			case key == "systemctl is-enabled openclaw-gateway.service":
				return hosts.Result{Stdout: "enabled\n"}, nil
			case strings.HasPrefix(key, "systemctl status openclaw-gateway.service"):
				return hosts.Result{Stdout: "Active: active (running)"}, nil
			case strings.HasPrefix(key, "journalctl -u openclaw-gateway.service"):
				return hosts.Result{Stdout: "Nov 03 14:00:00 host openclaw[123]: listening"}, nil
			case key == "systemctl restart openclaw-gateway.service":
				calledAction = true
				return hosts.Result{ExitCode: 0}, nil
			default:
				return hosts.Result{ExitCode: 1}, nil
			}
		},
	}

	rep := InspectSystemd(context.Background(), exec)
	if !rep.Available || rep.SystemState != "running" || len(rep.Units) != 3 {
		t.Fatalf("unexpected systemd report: %+v", rep)
	}
	if rep.Units[0].Name != "openclaw-gateway.service" || rep.Units[1].ActiveState != "failed" {
		t.Errorf("unexpected unit ordering: %+v", rep.Units)
	}

	u, err := InspectSystemdUnit(context.Background(), exec, "openclaw-gateway.service", 20)
	if err != nil {
		t.Fatalf("InspectSystemdUnit: %v", err)
	}
	if u.EnabledState != "enabled" || !strings.Contains(u.Details, "active (running)") || !strings.Contains(u.RecentLogs, "listening") {
		t.Errorf("unexpected unit detail: %+v", u)
	}

	// Unconfirmed state-changing action MUST be rejected without executing systemctl.
	if err := ControlSystemdService(context.Background(), exec, "openclaw-gateway.service", ActionRestart, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("expected ErrConfirmationRequired, got %v", err)
	}
	if calledAction {
		t.Error("systemctl restart should not have been called without confirmation")
	}

	// Confirmed action executes.
	if err := ControlSystemdService(context.Background(), exec, "openclaw-gateway.service", ActionRestart, true); err != nil {
		t.Fatalf("confirmed restart failed: %v", err)
	}
	if !calledAction {
		t.Error("expected confirmed restart to execute")
	}
}

func TestDockerInspectionAndStats(t *testing.T) {
	exec := &fakeExec{
		handler: func(cmd string, args ...string) (hosts.Result, error) {
			key := cmd + " " + strings.Join(args, " ")
			switch {
			case strings.HasPrefix(key, "docker info"):
				return hosts.Result{Stdout: "27.1.1\n"}, nil
			case strings.HasPrefix(key, "docker ps"):
				return hosts.Result{Stdout: "" +
					`{"ID":"c1","Names":"openclaw-sandbox","Image":"oc:latest","State":"running","Status":"Up 2 hours","Ports":"0.0.0.0:8080->8080/tcp"}` + "\n" +
					`{"ID":"c2","Names":"old-job","Image":"alpine","State":"exited","Status":"Exited (0) 1 day ago","Ports":""}` + "\n"}, nil
			case strings.HasPrefix(key, "docker stats"):
				return hosts.Result{Stdout: `{"ID":"c1","Name":"openclaw-sandbox","CPUPerc":"1.25%","MemUsage":"48MiB / 8GiB"}` + "\n"}, nil
			case strings.HasPrefix(key, "docker logs"):
				return hosts.Result{Stdout: "sandbox ready\n"}, nil
			default:
				return hosts.Result{ExitCode: 1}, nil
			}
		},
	}

	rep := InspectDocker(context.Background(), exec)
	if !rep.Available || rep.Version != "27.1.1" || len(rep.Containers) != 2 {
		t.Fatalf("unexpected docker report: %+v", rep)
	}
	if rep.Containers[0].CPU != "1.25%" || rep.Containers[0].Mem != "48MiB / 8GiB" {
		t.Errorf("stats not attached: %+v", rep.Containers[0])
	}
	logs, err := FetchDockerLogs(context.Background(), exec, "openclaw-sandbox", 25)
	if err != nil || logs != "sandbox ready" {
		t.Errorf("unexpected docker logs: %q, %v", logs, err)
	}
}

func TestModelsAndOllamaMismatchDetection(t *testing.T) {
	exec := &fakeExec{
		handler: func(cmd string, args ...string) (hosts.Result, error) {
			key := cmd + " " + strings.Join(args, " ")
			switch {
			case key == "ollama list":
				return hosts.Result{Stdout: "NAME               ID           SIZE    MODIFIED\nqwen2.5:7b         abc123def    4.7 GB  2 days ago\n"}, nil
			case strings.HasPrefix(key, "openclaw models status"):
				return hosts.Result{Stdout: `{
  "defaultModel": "ollama/dolphin-llama3:8b",
  "allowed": ["ollama/dolphin-llama3:8b", "ollama/qwen2.5:7b"],
  "auth": {
    "missingProvidersInUse": ["anthropic"],
    "modelRouteIssues": [],
    "providers": [{"provider":"ollama","effective":{"kind":"local","detail":"ok"}}]
  }
}`}, nil
			default:
				return hosts.Result{ExitCode: 1}, nil
			}
		},
	}

	rep := InspectModels(context.Background(), exec)
	if !rep.OllamaAvailable || !rep.OllamaReachable || len(rep.OllamaModels) != 1 {
		t.Fatalf("unexpected ollama state: %+v", rep)
	}
	if len(rep.Mismatches) != 2 {
		t.Fatalf("expected 2 mismatches (missing dolphin-llama3:8b + missing anthropic provider), got %v", rep.Mismatches)
	}
}

func TestSubsystemsAbsentDegradeGracefully(t *testing.T) {
	exec := &fakeExec{
		handler: func(string, ...string) (hosts.Result, error) {
			return hosts.Result{ExitCode: 127, Stderr: "not found"}, nil
		},
	}
	all := InspectAll(context.Background(), "minimal-hive", exec)
	if all.Systemd.Available || all.Docker.Available || all.Models.OllamaAvailable {
		t.Errorf("expected all optional subsystems unavailable: %+v", all)
	}
}
