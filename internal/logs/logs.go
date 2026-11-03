// Package logs implements BeeChief's unified log browser sources, ANSI/control
// sequence sanitization, bounded tail loading, and filtering (issue #9).
package logs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/meistro57/beechief/internal/hosts"
)

const (
	// DefaultTail is the default number of lines fetched per query.
	DefaultTail = 100
	// MaxTail caps memory usage for any single log fetch.
	MaxTail = 500
)

// SourceKind identifies a log provider on a Hive.
type SourceKind string

const (
	SourceJournalctl SourceKind = "journalctl"
	SourceDocker     SourceKind = "docker"
	SourceGateway    SourceKind = "openclaw"
	SourceAgent      SourceKind = "agent"
)

// AllSources lists the supported log source types in display order.
var AllSources = []SourceKind{
	SourceJournalctl,
	SourceDocker,
	SourceGateway,
	SourceAgent,
}

// Level classifies a log line's severity.
type Level string

const (
	LevelInfo    Level = "info"
	LevelWarning Level = "warn"
	LevelError   Level = "error"
)

// Entry is one sanitized log line.
type Entry struct {
	Line  string `json:"line"`
	Level Level  `json:"level"`
}

// Filter specifies level and text filtering criteria for log entries.
type Filter struct {
	ErrorsOnly   bool
	WarningsOnly bool
	Search       string
}

// Apply returns entries matching f, capped to MaxTail lines.
func (f Filter) Apply(entries []Entry) []Entry {
	q := strings.ToLower(strings.TrimSpace(f.Search))
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		if f.ErrorsOnly && e.Level != LevelError {
			continue
		}
		if f.WarningsOnly && e.Level != LevelWarning && e.Level != LevelError {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(e.Line), q) {
			continue
		}
		out = append(out, e)
	}
	if len(out) > MaxTail {
		out = out[len(out)-MaxTail:]
	}
	return out
}

// Result is the output of fetching logs from a Source.
type Result struct {
	Source  SourceKind
	Target  string
	Command string
	Entries []Entry
}

// Source fetches bounded logs from a Hive and reports the underlying command.
type Source interface {
	Kind() SourceKind
	// Command returns the human-readable CLI command used to fetch logs.
	Command(target string, tail int) (string, []string)
	// Fetch retrieves up to tail lines from the source on exec.
	Fetch(ctx context.Context, exec hosts.Execution, target string, tail int) (*Result, error)
}

// AdapterFor returns the Source adapter for kind.
func AdapterFor(kind SourceKind) Source {
	switch kind {
	case SourceDocker:
		return dockerSource{}
	case SourceGateway:
		return gatewaySource{}
	case SourceAgent:
		return agentSource{}
	default:
		return journalctlSource{}
	}
}

// CommandString formats the underlying command for display/copying in the UI.
func CommandString(kind SourceKind, target string, tail int) string {
	src := AdapterFor(kind)
	bin, args := src.Command(target, normalizeTail(tail))
	parts := make([]string, 0, 1+len(args))
	parts = append(parts, bin)
	for _, a := range args {
		if strings.ContainsAny(a, " \t") {
			parts = append(parts, hosts.ShellQuote(a))
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

type journalctlSource struct{}

func (journalctlSource) Kind() SourceKind { return SourceJournalctl }

func (journalctlSource) Command(target string, tail int) (string, []string) {
	tail = normalizeTail(tail)
	args := []string{"-n", fmt.Sprintf("%d", tail), "--no-pager"}
	if t := strings.TrimSpace(target); t != "" && !strings.HasPrefix(t, "-") {
		args = append([]string{"-u", t}, args...)
	}
	return "journalctl", args
}

func (s journalctlSource) Fetch(ctx context.Context, exec hosts.Execution, target string, tail int) (*Result, error) {
	return runSource(ctx, exec, s, target, tail)
}

type dockerSource struct{}

func (dockerSource) Kind() SourceKind { return SourceDocker }

func (dockerSource) Command(target string, tail int) (string, []string) {
	tail = normalizeTail(tail)
	t := strings.TrimSpace(target)
	if t == "" {
		t = "<container>"
	}
	return "docker", []string{"logs", "--tail", fmt.Sprintf("%d", tail), t}
}

func (s dockerSource) Fetch(ctx context.Context, exec hosts.Execution, target string, tail int) (*Result, error) {
	if strings.TrimSpace(target) == "" || strings.HasPrefix(strings.TrimSpace(target), "-") {
		return nil, errors.New("select or enter a Docker container name first")
	}
	return runSource(ctx, exec, s, target, tail)
}

type gatewaySource struct{}

func (gatewaySource) Kind() SourceKind { return SourceGateway }

func (gatewaySource) Command(_ string, tail int) (string, []string) {
	tail = normalizeTail(tail)
	return "openclaw", []string{"logs", "--plain", "--no-color", "--limit", fmt.Sprintf("%d", tail)}
}

func (s gatewaySource) Fetch(ctx context.Context, exec hosts.Execution, target string, tail int) (*Result, error) {
	return runSource(ctx, exec, s, target, tail)
}

type agentSource struct{}

func (agentSource) Kind() SourceKind { return SourceAgent }

func (agentSource) Command(target string, _ int) (string, []string) {
	args := []string{"status", "--no-color"}
	if t := strings.TrimSpace(target); t != "" && !strings.HasPrefix(t, "-") {
		args = append(args, "--agent", t)
	}
	return "openclaw", args
}

func (s agentSource) Fetch(ctx context.Context, exec hosts.Execution, target string, tail int) (*Result, error) {
	return runSource(ctx, exec, s, target, tail)
}

func runSource(ctx context.Context, exec hosts.Execution, src Source, target string, tail int) (*Result, error) {
	if exec == nil {
		return nil, errors.New("no execution configured for Hive")
	}
	tail = normalizeTail(tail)
	bin, args := src.Command(target, tail)
	cmdStr := CommandString(src.Kind(), target, tail)
	res, err := exec.Run(ctx, bin, args...)
	if err != nil {
		return nil, fmt.Errorf("%s failed: %w", cmdStr, err)
	}
	if res.ExitCode == 127 {
		return nil, fmt.Errorf("%s unavailable on Hive: command not found", bin)
	}
	combined := res.Stdout
	if strings.TrimSpace(res.Stderr) != "" {
		if combined != "" {
			combined += "\n" + res.Stderr
		} else {
			combined = res.Stderr
		}
	}
	if !res.Ok() && strings.TrimSpace(combined) == "" {
		return nil, fmt.Errorf("%s exited %d", cmdStr, res.ExitCode)
	}
	if !res.Ok() {
		return nil, fmt.Errorf("%s (exit %d): %s", cmdStr, res.ExitCode, StripControlSequences(firstLine(combined)))
	}
	entries := ParseLines(combined, tail)
	return &Result{
		Source:  src.Kind(),
		Target:  target,
		Command: cmdStr,
		Entries: entries,
	}, nil
}

var ansiSequenceRE = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\)|[@-Z\\-_])`)

// StripControlSequences removes ANSI CSI/OSC escape sequences and dangerous
// terminal control characters from remote log output.
func StripControlSequences(s string) string {
	clean := ansiSequenceRE.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(clean))
	for _, r := range clean {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r == '\r':
			// drop carriage returns
		case r < 0x20 || r == 0x7f:
			// drop other control characters
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ParseLines splits raw log output into sanitized, classified Entries, bounded
// to the last tail lines.
func ParseLines(raw string, tail int) []Entry {
	tail = normalizeTail(tail)
	cleaned := StripControlSequences(raw)
	lines := strings.Split(cleaned, "\n")
	var entries []Entry
	for _, line := range lines {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed == "" {
			continue
		}
		entries = append(entries, Entry{
			Line:  trimmed,
			Level: ClassifyLine(trimmed),
		})
	}
	if len(entries) > tail {
		entries = entries[len(entries)-tail:]
	}
	return entries
}

// ClassifyLine infers a log line's severity level.
func ClassifyLine(line string) Level {
	lower := strings.ToLower(line)
	for _, tok := range []string{"error", "err:", "fatal", "panic", "failed", "exception", "critical", "level=error", `"level":"error"`} {
		if strings.Contains(lower, tok) {
			return LevelError
		}
	}
	for _, tok := range []string{"warn", "warning", "deprecated", "timeout", "retry", "level=warn", `"level":"warn"`} {
		if strings.Contains(lower, tok) {
			return LevelWarning
		}
	}
	return LevelInfo
}

func normalizeTail(tail int) int {
	if tail <= 0 {
		return DefaultTail
	}
	if tail > MaxTail {
		return MaxTail
	}
	return tail
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
