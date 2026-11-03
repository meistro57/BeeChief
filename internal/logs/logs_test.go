package logs

import (
	"context"
	"strings"
	"testing"

	"github.com/meistro57/beechief/internal/hosts"
)

type fakeExec struct {
	cmd  string
	args []string
	res  hosts.Result
	err  error
}

func (f *fakeExec) Run(_ context.Context, cmd string, args ...string) (hosts.Result, error) {
	f.cmd, f.args = cmd, args
	return f.res, f.err
}

func TestStripControlSequencesAndFiltering(t *testing.T) {
	raw := "\x1b[32m2026-11-03 INFO started gateway\x1b[0m\r\n" +
		"\x1b[33m2026-11-03 WARN deprecated flag used\x1b[0m\r\n" +
		"\x1b[31m2026-11-03 ERROR connection refused to provider\x1b[0m\x07\n"

	entries := ParseLines(raw, 50)
	if len(entries) != 3 {
		t.Fatalf("entries count: got %d, want 3", len(entries))
	}
	if strings.Contains(entries[0].Line, "\x1b") || strings.Contains(entries[2].Line, "\x07") {
		t.Errorf("control characters not stripped: %+v", entries)
	}
	if entries[0].Level != LevelInfo || entries[1].Level != LevelWarning || entries[2].Level != LevelError {
		t.Errorf("unexpected levels: %+v", entries)
	}

	errOnly := (Filter{ErrorsOnly: true}).Apply(entries)
	if len(errOnly) != 1 || errOnly[0].Level != LevelError {
		t.Errorf("ErrorsOnly filter failed: %+v", errOnly)
	}

	warnPlus := (Filter{WarningsOnly: true}).Apply(entries)
	if len(warnPlus) != 2 {
		t.Errorf("WarningsOnly filter failed: %+v", warnPlus)
	}

	searched := (Filter{Search: "REFUSED"}).Apply(entries)
	if len(searched) != 1 || !strings.Contains(searched[0].Line, "refused") {
		t.Errorf("Search filter failed: %+v", searched)
	}
}

func TestJournalctlAndDockerAdapters(t *testing.T) {
	f := &fakeExec{res: hosts.Result{Stdout: "line 1\nERROR line 2\n"}}
	res, err := AdapterFor(SourceJournalctl).Fetch(context.Background(), f, "openclaw.service", 25)
	if err != nil {
		t.Fatalf("journalctl Fetch: %v", err)
	}
	if res.Command != "journalctl -u openclaw.service -n 25 --no-pager" {
		t.Errorf("unexpected command string: %q", res.Command)
	}
	if len(res.Entries) != 2 || res.Entries[1].Level != LevelError {
		t.Errorf("unexpected entries: %+v", res.Entries)
	}

	// Docker requires a container name
	if _, err := AdapterFor(SourceDocker).Fetch(context.Background(), f, "", 25); err == nil {
		t.Error("expected error when docker container name is empty")
	}

	res2, err := AdapterFor(SourceDocker).Fetch(context.Background(), f, "oc-box", 40)
	if err != nil {
		t.Fatalf("docker Fetch: %v", err)
	}
	if res2.Command != "docker logs --tail 40 oc-box" {
		t.Errorf("unexpected docker command: %q", res2.Command)
	}
}

func TestOfflineOrMissingSourceShowsUsefulError(t *testing.T) {
	f := &fakeExec{res: hosts.Result{ExitCode: 127, Stderr: "journalctl: not found"}}
	_, err := AdapterFor(SourceJournalctl).Fetch(context.Background(), f, "", 20)
	if err == nil || !strings.Contains(err.Error(), "unavailable on Hive") {
		t.Fatalf("expected useful unavailable error, got %v", err)
	}
}
