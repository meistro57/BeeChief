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
	// ErrUnsupported means the installed OpenClaw CLI does not support a
	// subcommand or flag required by this feature.
	ErrUnsupported = errors.New("unsupported openclaw command or version")
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

// SelfReviewPrompt is the structured self-review instruction sent to a Bee
// during skill self-review (issue #6).
const SelfReviewPrompt = `Perform a self-review of your current skills.

1. Inspect the skills currently available to you.
2. Identify skills that are outdated, incomplete, redundant, overlapping, or unclear.
3. Look for procedures from recent work that should become reusable skills.
4. Check whether existing skills accurately describe how you perform the work.
5. Propose improvements through Skill Workshop where appropriate.
6. Do not change unrelated user-owned or shared skills.
7. Report what you inspected, what you recommend changing, and why.`

// DiscoverAgents asks the Hive which OpenClaw agents exist by running
// `openclaw agents list --json`. Returned Bees have an empty Hive field;
// the caller owns Hive naming.
func (c *Client) DiscoverAgents(ctx context.Context) ([]agents.Bee, error) {
	res, err := c.run(ctx, listAgentsArgs...)
	if err != nil {
		return nil, err
	}
	return ParseAgents([]byte(res.Stdout))
}

// Version returns the OpenClaw CLI version string reported on the Hive.
func (c *Client) Version(ctx context.Context) (string, error) {
	res, err := c.run(ctx, "--version")
	if err != nil {
		return "", err
	}
	return ParseVersion(res.Stdout + "\n" + res.Stderr), nil
}

// GatewayHealth checks whether the OpenClaw Gateway on the Hive responds to
// `openclaw health --json`.
func (c *Client) GatewayHealth(ctx context.Context) (string, error) {
	res, err := c.run(ctx, "health", "--json")
	if err != nil {
		return "", err
	}
	return summarize(res.Stdout, "ok"), nil
}

// ListSkills runs `openclaw skills list --agent <agent> --json` and returns
// the parsed skill inventory along with raw CLI output for troubleshooting.
func (c *Client) ListSkills(ctx context.Context, agentID string) (*SkillListReport, error) {
	args := []string{"skills", "list"}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	rep, err := ParseSkillList([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	if rep.AgentID == "" {
		rep.AgentID = agentID
	}
	rep.RawOutput = combinedOutput(res)
	return rep, nil
}

// CheckSkills runs `openclaw skills check --agent <agent> --json` and returns
// the parsed skill check report along with raw CLI output.
func (c *Client) CheckSkills(ctx context.Context, agentID string) (*SkillCheckReport, error) {
	args := []string{"skills", "check"}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	rep, err := ParseSkillCheck([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	if rep.AgentID == "" {
		rep.AgentID = agentID
	}
	rep.RawOutput = combinedOutput(res)
	return rep, nil
}

// ListWorkshop runs `openclaw skills workshop list --agent <agent> --json` to
// inspect Workshop proposals/learned skills for a Bee without modifying them.
func (c *Client) ListWorkshop(ctx context.Context, agentID string) (*WorkshopListReport, error) {
	args := []string{"skills", "workshop", "list"}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	rep, err := ParseWorkshopList([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	if rep.AgentID == "" {
		rep.AgentID = agentID
	}
	rep.RawOutput = combinedOutput(res)
	return rep, nil
}

// ShowWorkshopProposal runs `openclaw skills workshop show <name> --agent <agent> --json`
// to inspect the full content of a single Workshop proposal/learned skill.
func (c *Client) ShowWorkshopProposal(ctx context.Context, agentID, name string) (*WorkshopProposal, error) {
	args := []string{"skills", "workshop", "show", name}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return ParseWorkshopShow([]byte(res.Stdout), name)
}

// SelfReview runs a structured skill self-review on the given Bee and then
// queries Workshop proposals (where supported) without applying any changes.
func (c *Client) SelfReview(ctx context.Context, agentID string) (*SelfReviewReport, error) {
	out, raw, err := c.runAgentMessage(ctx, agentID, SelfReviewPrompt)
	if err != nil {
		return nil, err
	}
	rep := &SelfReviewReport{
		AgentID:   agentID,
		Prompt:    SelfReviewPrompt,
		Summary:   out,
		RawOutput: raw,
	}
	if ws, wsErr := c.ListWorkshop(ctx, agentID); wsErr == nil && ws != nil {
		rep.Proposals = ws.Proposals
	}
	return rep, nil
}

// SendInstruction sends a custom instruction message to a specific Bee and
// returns the agent's response text.
func (c *Client) SendInstruction(ctx context.Context, agentID, message string) (string, error) {
	if strings.TrimSpace(message) == "" {
		return "", errors.New("instruction message is required")
	}
	out, _, err := c.runAgentMessage(ctx, agentID, message)
	return out, err
}

// ModelsStatus runs `openclaw models status [--agent <id>] --json` to inspect
// configured models, providers, and route issues.
func (c *Client) ModelsStatus(ctx context.Context, agentID string) (*ModelStatusReport, error) {
	args := []string{"models", "status"}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	rep, err := ParseModelsStatus([]byte(res.Stdout))
	if err != nil {
		return nil, err
	}
	rep.RawOutput = combinedOutput(res)
	return rep, nil
}

func (c *Client) runAgentMessage(ctx context.Context, agentID, message string) (string, string, error) {
	args := []string{"agent"}
	if strings.TrimSpace(agentID) != "" {
		args = append(args, "--agent", agentID)
	}
	args = append(args, "--message", message, "--json")
	res, err := c.run(ctx, args...)
	if err != nil {
		return "", "", err
	}
	raw := combinedOutput(res)
	summary := ParseAgentReply([]byte(res.Stdout))
	if summary == "" {
		summary = strings.TrimSpace(res.Stdout)
	}
	return summary, raw, nil
}

func (c *Client) run(ctx context.Context, args ...string) (hosts.Result, error) {
	res, err := c.exec.Run(ctx, Command, args...)
	if err != nil {
		if hosts.IsKind(err, hosts.KindNotFound) {
			return res, fmt.Errorf("%w: %v", ErrNotInstalled, err)
		}
		return res, err
	}
	if res.ExitCode == exitCommandNotFound {
		return res, fmt.Errorf("%w: %s", ErrNotInstalled, summarize(res.Stderr, "command not found"))
	}
	if !res.Ok() {
		detail := summarize(res.Stderr, res.Stdout)
		if isUnsupportedOutput(res.Stderr + "\n" + res.Stdout) {
			return res, fmt.Errorf("%w (exit %d): %s", ErrUnsupported, res.ExitCode, detail)
		}
		return res, fmt.Errorf("%w (exit %d): %s", ErrCommandFailed, res.ExitCode, detail)
	}
	return res, nil
}

func isUnsupportedOutput(out string) bool {
	lower := strings.ToLower(out)
	for _, pat := range []string{
		"unknown command",
		"unknown option",
		"unrecognized option",
		"unknown flag",
		"not a valid command",
		"unsupported command",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func combinedOutput(res hosts.Result) string {
	out := strings.TrimSpace(res.Stdout)
	errOut := strings.TrimSpace(res.Stderr)
	if out != "" && errOut != "" {
		return out + "\n" + errOut
	}
	if out != "" {
		return out
	}
	return errOut
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
