// Package openclaw isolates every interaction with the OpenClaw CLI.
//
// All command construction lives here so the rest of BeeChief never shells
// out directly and so a future OpenClaw version change is absorbed in one
// place (roadmap: "Agent-friendly issues" / adapter isolation).
package openclaw

import (
	"context"

	"github.com/meistro57/beechief/internal/hosts"
)

// Client runs OpenClaw commands against a Hive.
type Client struct {
	exec hosts.Execution
}

// NewClient returns a Client bound to the given Hive execution.
func NewClient(exec hosts.Execution) *Client {
	return &Client{exec: exec}
}

// DiscoverAgents asks the Hive which OpenClaw agents exist.
//
// This is a placeholder until issue #3 defines the real discovery command; it
// currently returns no agents rather than guessing at a CLI surface.
func (c *Client) DiscoverAgents(ctx context.Context) ([]string, error) {
	_ = ctx
	return nil, nil
}

// Command returns the OpenClaw binary name BeeChief invokes.
//
// It is centralised here so discovery code never hard-codes the CLI name.
const Command = "openclaw"
