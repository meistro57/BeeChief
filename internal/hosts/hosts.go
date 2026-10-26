// Package hosts defines how BeeChief reaches a Hive.
//
// Local and SSH Hives expose the same Execution interface so that everything
// above this layer is transport-agnostic (roadmap: "One execution path").
// Nothing in this package knows about OpenClaw; diagnostics, Docker, systemd,
// and Ollama checks are expected to reuse it unchanged.
package hosts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DefaultTimeout bounds a single command when the caller's context carries
// no deadline. Every Run is therefore guaranteed to return.
const DefaultTimeout = 30 * time.Second

// Result is the outcome of running a command on a Hive.
//
// A command that ran but exited non-zero is reported here (ExitCode != 0)
// with a nil error; errors are reserved for "could not run it at all".
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
}

// Ok reports whether the command exited successfully.
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Execution runs read-only commands against a Hive.
//
// Implementations are expected to be safe-by-default: the interface is
// deliberately narrow, and callers should prefer read-only commands. The
// command and args are passed as discrete words; no implementation feeds
// them to a local shell, and the SSH implementation quotes each word for the
// remote shell.
type Execution interface {
	// Run executes command with args on the Hive and returns its output.
	Run(ctx context.Context, command string, args ...string) (Result, error)
}

// ErrorKind classifies why a command could not be run.
type ErrorKind string

const (
	// KindUnreachable means the Hive could not be contacted (DNS, refused,
	// auth failure, host key mismatch, ...).
	KindUnreachable ErrorKind = "unreachable"
	// KindTimeout means the command or connection exceeded its deadline.
	KindTimeout ErrorKind = "timeout"
	// KindCanceled means the caller canceled the context.
	KindCanceled ErrorKind = "canceled"
	// KindNotFound means the executable does not exist on the Hive.
	KindNotFound ErrorKind = "not_found"
	// KindStart means the process could not be started for another reason.
	KindStart ErrorKind = "start_failed"
)

// Error is the structured error returned by every Execution.
type Error struct {
	// Hive is the configured Hive name, if known.
	Hive string
	// Kind classifies the failure.
	Kind ErrorKind
	// Command is a human-readable rendering of what was attempted.
	Command string
	// ExitCode is the process exit code, or -1 if it never exited.
	ExitCode int
	// Stderr is any diagnostic output captured before the failure.
	Stderr string
	// Err is the underlying cause.
	Err error
}

func (e *Error) Error() string {
	var b strings.Builder
	if e.Hive != "" {
		fmt.Fprintf(&b, "hive %q: ", e.Hive)
	}
	fmt.Fprintf(&b, "%s: %s", e.Kind, e.Command)
	if msg := strings.TrimSpace(e.Stderr); msg != "" {
		fmt.Fprintf(&b, ": %s", firstLine(msg))
	} else if e.Err != nil {
		fmt.Fprintf(&b, ": %v", e.Err)
	}
	return b.String()
}

func (e *Error) Unwrap() error { return e.Err }

// IsKind reports whether err is (or wraps) an *Error of the given kind.
func IsKind(err error, kind ErrorKind) bool {
	var he *Error
	return errors.As(err, &he) && he.Kind == kind
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ProbeResult reports whether a Hive answered a trivial command.
type ProbeResult struct {
	Reachable bool
	Latency   time.Duration
	Err       error
}

// Probe checks that a Hive can run commands at all by executing `true`.
// It never hangs beyond the context deadline (or DefaultTimeout).
func Probe(ctx context.Context, e Execution) ProbeResult {
	start := time.Now()
	res, err := e.Run(ctx, "true")
	pr := ProbeResult{Latency: time.Since(start), Err: err}
	if err == nil && !res.Ok() {
		pr.Err = fmt.Errorf("probe exited %d: %s", res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	pr.Reachable = pr.Err == nil
	return pr
}
