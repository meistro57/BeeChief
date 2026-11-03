// Package openclaw isolates every interaction with the OpenClaw CLI.
//
// All command construction lives here so the rest of BeeChief never shells
// out directly and so a future OpenClaw version change is absorbed in one
// place (roadmap: "Agent-friendly issues" / adapter isolation). Output
// parsing lives in parse.go.
package openclaw

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/hosts"
)

// Command returns the OpenClaw binary name BeeChief invokes.
//
// It is centralised here so discovery code never hard-codes the CLI name.
const Command = "openclaw"

// listAgentsArgs is the machine-readable agent listing.
var listAgentsArgs = []string{"agents", "list", "--json"}

// Discovery failure classes. Transport failures (unreachable, timeout) are
// returned as *hosts.Error unchanged; use hosts.IsKind to test for them.
var (
	// ErrNotInstalled means the OpenClaw CLI is not on the Hive's PATH.
	ErrNotInstalled = errors.New("openclaw not installed")
	// ErrCommandFailed means OpenClaw ran but exited non-zero.
	ErrCommandFailed = errors.New("openclaw command failed")
	// ErrInvalidOutput means OpenClaw's output could not be parsed.
	ErrInvalidOutput = errors.New("invalid openclaw output")
)

// exitCommandNotFound is the POSIX shell status for "command not found",
// which is what a remote Hive without OpenClaw returns over SSH.
const exitCommandNotFound = 127

// Client runs OpenClaw commands against a Hive.
type Client struct {
	exec hosts.Execution
}

// NewClient returns a Client bound to the given Hive execution.
func NewClient(exec hosts.Execution) *Client {
	return &Client{exec: exec}
}

// DiscoverAgents asks the Hive which OpenClaw agents exist by running
// `openclaw agents list --json`. Returned Bees have an empty Hive field;
// the caller owns Hive naming.
func (c *Client) DiscoverAgents(ctx context.Context) ([]agents.Bee, error) {
	res, err := c.exec.Run(ctx, Command, listAgentsArgs...)
	if err != nil {
		if hosts.IsKind(err, hosts.KindNotFound) {
			return nil, fmt.Errorf("%w: %v", ErrNotInstalled, err)
		}
		return nil, err
	}
	if res.ExitCode == exitCommandNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNotInstalled, summarize(res.Stderr, "command not found"))
	}
	if !res.Ok() {
		return nil, fmt.Errorf("%w (exit %d): %s", ErrCommandFailed, res.ExitCode, summarize(res.Stderr, res.Stdout))
	}
	return ParseAgents([]byte(res.Stdout))
}

// summarize returns the first non-empty line of the first non-empty input.
func summarize(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			if i := strings.IndexByte(s, '\n'); i >= 0 {
				s = s[:i]
			}
			return s
		}
	}
	return "no output"
}
