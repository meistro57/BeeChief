package hosts

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/meistro57/beechief/internal/config"
)

func TestLocalRunCapturesOutputAndArgs(t *testing.T) {
	res, err := NewLocal().Run(context.Background(), "echo", "hello", "a b")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Ok() || res.Stdout != "hello a b\n" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestLocalRunDoesNotUseShell(t *testing.T) {
	res, err := NewLocal().Run(context.Background(), "echo", "$HOME;", "`id`")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != "$HOME; `id`\n" {
		t.Errorf("args were shell-expanded: %q", res.Stdout)
	}
}

func TestLocalNonZeroExitIsResultNotError(t *testing.T) {
	res, err := NewLocal().Run(context.Background(), "sh", "-c", "echo oops >&2; exit 3")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Ok() || res.ExitCode != 3 || strings.TrimSpace(res.Stderr) != "oops" {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestLocalMissingCommandIsNotFound(t *testing.T) {
	_, err := (&LocalExecution{Hive: "here"}).Run(context.Background(), "beechief-definitely-not-a-command")
	if !IsKind(err, KindNotFound) {
		t.Fatalf("want KindNotFound, got %v", err)
	}
	var he *Error
	if !errors.As(err, &he) || he.Hive != "here" {
		t.Errorf("error should carry hive name: %#v", err)
	}
}

func TestLocalTimeout(t *testing.T) {
	l := &LocalExecution{Timeout: 100 * time.Millisecond}
	start := time.Now()
	_, err := l.Run(context.Background(), "sleep", "5")
	if !IsKind(err, KindTimeout) {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("timeout took too long: %v", time.Since(start))
	}
}

func TestLocalCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewLocal().Run(ctx, "sleep", "5")
	if !IsKind(err, KindCanceled) {
		t.Fatalf("want KindCanceled, got %v", err)
	}
}

func TestProbeLocal(t *testing.T) {
	pr := Probe(context.Background(), NewLocal())
	if !pr.Reachable || pr.Err != nil {
		t.Fatalf("local probe failed: %+v", pr)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"":             "''",
		"openclaw":     "openclaw",
		"--json":       "--json",
		"a b":          "'a b'",
		"it's":         `'it'\''s'`,
		"$(reboot)":    "'$(reboot)'",
		"/usr/bin/x.y": "/usr/bin/x.y",
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSSHArgsConstruction(t *testing.T) {
	s := &SSHExecution{Host: "qmu-server", User: "root", Port: 2222, ConnectTimeout: 5 * time.Second}
	got := s.Args("openclaw", "agents", "list", "--json")
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=5",
		"-p", "2222", "-l", "root",
		"--", "qmu-server", "openclaw agents list --json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args:\n got  %q\n want %q", got, want)
	}
}

func TestSSHArgsDefaultsAndQuoting(t *testing.T) {
	s := &SSHExecution{Host: "box"}
	got := s.Args("echo", "hello world", "$HOME")
	want := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10",
		"--", "box", "echo 'hello world' '$HOME'",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Args:\n got  %q\n want %q", got, want)
	}
}

func TestForHive(t *testing.T) {
	e, err := ForHive(config.Hive{Name: "work-pc", Type: config.TypeLocal})
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := e.(*LocalExecution); !ok || l.Hive != "work-pc" {
		t.Errorf("want LocalExecution, got %#v", e)
	}

	e, err = ForHive(config.Hive{Name: "qmu", Type: config.TypeSSH, Host: "qmu-server", User: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if s, ok := e.(*SSHExecution); !ok || s.Host != "qmu-server" || s.User != "root" || s.Hive != "qmu" {
		t.Errorf("want SSHExecution, got %#v", e)
	}

	if _, err := ForHive(config.Hive{Name: "bad", Type: config.TypeSSH}); err == nil {
		t.Error("ssh hive without host should fail")
	}
}

// fakeSSH writes an executable script standing in for the ssh binary.
func fakeSSH(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSSHRunPassesArgvToBinary(t *testing.T) {
	s := &SSHExecution{Hive: "qmu", Host: "qmu-server", Binary: fakeSSH(t, `printf '%s\n' "$@"`)}
	res, err := s.Run(context.Background(), "uname", "-a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(res.Stdout), "\n")
	if lines[len(lines)-2] != "qmu-server" || lines[len(lines)-1] != "uname -a" {
		t.Errorf("unexpected argv: %q", lines)
	}
}

func TestSSHExit255IsUnreachable(t *testing.T) {
	s := &SSHExecution{Hive: "qmu", Host: "qmu-server",
		Binary: fakeSSH(t, `echo "ssh: connect to host qmu-server port 22: Connection refused" >&2; exit 255`)}
	_, err := s.Run(context.Background(), "true")
	if !IsKind(err, KindUnreachable) {
		t.Fatalf("want KindUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), "Connection refused") || !strings.Contains(err.Error(), `"qmu"`) {
		t.Errorf("error should include hive and stderr: %v", err)
	}
}

func TestSSHRemoteNonZeroExitIsResult(t *testing.T) {
	s := &SSHExecution{Host: "h", Binary: fakeSSH(t, `exit 127`)}
	res, err := s.Run(context.Background(), "missing-cmd")
	if err != nil || res.ExitCode != 127 {
		t.Fatalf("want exit 127 result, got %+v, %v", res, err)
	}
}

func TestSSHHungConnectionTimesOut(t *testing.T) {
	s := &SSHExecution{Host: "h", Timeout: 200 * time.Millisecond, Binary: fakeSSH(t, `exec sleep 30`)}
	start := time.Now()
	_, err := s.Run(context.Background(), "true")
	if !IsKind(err, KindTimeout) {
		t.Fatalf("want KindTimeout, got %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("hung for %v", time.Since(start))
	}
}

func TestSSHMissingBinaryIsUnreachable(t *testing.T) {
	s := &SSHExecution{Host: "h", Binary: filepath.Join(t.TempDir(), "no-ssh-here")}
	_, err := s.Run(context.Background(), "true")
	if !IsKind(err, KindUnreachable) {
		t.Fatalf("want KindUnreachable, got %v", err)
	}
}

// TestSSHRealUnreachableHost uses the real ssh client against a closed local
// port, with an empty config so the operator's ~/.ssh/config cannot interfere.
func TestSSHRealUnreachableHost(t *testing.T) {
	if _, err := exec.LookPath("ssh"); err != nil {
		t.Skip("ssh client not installed")
	}
	s := &SSHExecution{
		Hive: "nowhere", Host: "127.0.0.1", Port: 1, ConfigFile: os.DevNull,
		ConnectTimeout: 2 * time.Second, Timeout: 10 * time.Second,
	}
	start := time.Now()
	_, err := s.Run(context.Background(), "true")
	if !IsKind(err, KindUnreachable) && !IsKind(err, KindTimeout) {
		t.Fatalf("want structured unreachable/timeout error, got %v", err)
	}
	if time.Since(start) > 12*time.Second {
		t.Errorf("unreachable host took %v", time.Since(start))
	}
	pr := Probe(context.Background(), s)
	if pr.Reachable || pr.Err == nil {
		t.Errorf("probe should report unreachable: %+v", pr)
	}
}
