package openclaw

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/meistro57/beechief/internal/agents"
)

// This file is the single place that understands OpenClaw's JSON output.
// When the CLI's format changes, only this file should need updating.

// agentJSON mirrors one entry of `openclaw agents list --json`. Every field
// is optional; unknown fields are ignored.
type agentJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	IdentityName  string `json:"identityName"`
	IdentityEmoji string `json:"identityEmoji"`
	Workspace     string `json:"workspace"`
	AgentDir      string `json:"agentDir"`
	Model         string `json:"model"`
	Status        string `json:"status"`
	IsDefault     bool   `json:"isDefault"`
}

// ParseAgents decodes `openclaw agents list --json` output into Bees (with
// Hive left empty for the caller to fill in).
//
// It accepts either a bare JSON array or an object with an "agents" array,
// and tolerates non-JSON lines (banners, warnings) before the document.
// Entries with neither an id nor a name are skipped.
func ParseAgents(data []byte) ([]agents.Bee, error) {
	raw, err := decodeAgentList(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	bees := make([]agents.Bee, 0, len(raw))
	for _, a := range raw {
		id := strings.TrimSpace(a.ID)
		if id == "" {
			id = strings.TrimSpace(a.Name)
		}
		if id == "" {
			continue
		}
		name := firstNonEmpty(a.IdentityName, a.Name, id)
		bees = append(bees, agents.Bee{
			ID:        id,
			Name:      name,
			Runtime:   a.Model,
			Emoji:     a.IdentityEmoji,
			Workspace: a.Workspace,
			AgentDir:  a.AgentDir,
			Status:    a.Status,
			Default:   a.IsDefault,
		})
	}
	return bees, nil
}

func decodeAgentList(data []byte) ([]agentJSON, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty output")
	}
	list, err := decodeFirstValue(trimmed)
	if err == nil {
		return list, nil
	}
	// Skip any leading non-JSON lines and retry from the first line that
	// opens an array or object.
	for off := 0; off < len(trimmed); {
		nl := bytes.IndexByte(trimmed[off:], '\n')
		if nl < 0 {
			break
		}
		off += nl + 1
		rest := bytes.TrimLeft(trimmed[off:], " \t\r")
		if len(rest) > 0 && (rest[0] == '[' || rest[0] == '{') {
			if list, err2 := decodeFirstValue(rest); err2 == nil {
				return list, nil
			}
		}
	}
	return nil, err
}

func decodeFirstValue(data []byte) ([]agentJSON, error) {
	var v json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(&v); err != nil {
		return nil, err
	}
	switch v[0] {
	case '[':
		var list []agentJSON
		if err := json.Unmarshal(v, &list); err != nil {
			return nil, err
		}
		return list, nil
	case '{':
		var wrapped struct {
			Agents *[]agentJSON `json:"agents"`
		}
		if err := json.Unmarshal(v, &wrapped); err != nil {
			return nil, err
		}
		if wrapped.Agents == nil {
			return nil, fmt.Errorf("object has no \"agents\" array")
		}
		return *wrapped.Agents, nil
	default:
		return nil, fmt.Errorf("expected JSON array or object, got %.20q", v)
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// SkillMissing describes unmet requirements for a skill.
type SkillMissing struct {
	Bins    []string `json:"bins"`
	AnyBins []string `json:"anyBins"`
	Env     []string `json:"env"`
	Config  []string `json:"config"`
	OS      []string `json:"os"`
}

// HasMissing reports whether any requirement is missing.
func (m SkillMissing) HasMissing() bool {
	return len(m.Bins) > 0 || len(m.AnyBins) > 0 || len(m.Env) > 0 || len(m.Config) > 0 || len(m.OS) > 0
}

// Summary returns a concise description of missing requirements.
func (m SkillMissing) Summary() string {
	var parts []string
	if len(m.Bins) > 0 {
		parts = append(parts, "bins: "+strings.Join(m.Bins, ", "))
	}
	if len(m.AnyBins) > 0 {
		parts = append(parts, "anyBins: "+strings.Join(m.AnyBins, ", "))
	}
	if len(m.Env) > 0 {
		parts = append(parts, "env: "+strings.Join(m.Env, ", "))
	}
	if len(m.Config) > 0 {
		parts = append(parts, "config: "+strings.Join(m.Config, ", "))
	}
	if len(m.OS) > 0 {
		parts = append(parts, "os: "+strings.Join(m.OS, ", "))
	}
	return strings.Join(parts, "; ")
}

// Skill describes one OpenClaw skill ("Comb" item) for a Bee.
type Skill struct {
	Name                 string       `json:"name"`
	Description          string       `json:"description"`
	Emoji                string       `json:"emoji"`
	Eligible             bool         `json:"eligible"`
	Disabled             bool         `json:"disabled"`
	BlockedByAllowlist   bool         `json:"blockedByAllowlist"`
	BlockedByAgentFilter bool         `json:"blockedByAgentFilter"`
	ModelVisible         bool         `json:"modelVisible"`
	UserInvocable        bool         `json:"userInvocable"`
	CommandVisible       bool         `json:"commandVisible"`
	Source               string       `json:"source"`
	Bundled              bool         `json:"bundled"`
	Homepage             string       `json:"homepage"`
	Missing              SkillMissing `json:"missing"`
}

// Healthy reports whether the skill is ready/loadable for the Bee.
func (s Skill) Healthy() bool {
	return s.Eligible && !s.Disabled && !s.BlockedByAllowlist && !s.BlockedByAgentFilter && !s.Missing.HasMissing()
}

// Broken reports whether the skill has unmet requirements or is blocked.
func (s Skill) Broken() bool {
	return s.Missing.HasMissing() || s.BlockedByAllowlist || s.BlockedByAgentFilter || (!s.Disabled && !s.Eligible)
}

// StateLabel returns a short status string for the skill.
func (s Skill) StateLabel() string {
	switch {
	case s.Healthy():
		return "ready"
	case s.Missing.HasMissing():
		return "missing (" + s.Missing.Summary() + ")"
	case s.BlockedByAllowlist || s.BlockedByAgentFilter:
		return "blocked"
	case s.Disabled:
		return "disabled"
	default:
		return "ineligible"
	}
}

// SkillListReport is the parsed output of `openclaw skills list --json`.
type SkillListReport struct {
	AgentID          string  `json:"agentId"`
	WorkspaceDir     string  `json:"workspaceDir"`
	ManagedSkillsDir string  `json:"managedSkillsDir"`
	Skills           []Skill `json:"skills"`
	RawOutput        string  `json:"-"`
}

// HealthySkills returns loadable/healthy skills.
func (r *SkillListReport) HealthySkills() []Skill {
	if r == nil {
		return nil
	}
	var out []Skill
	for _, s := range r.Skills {
		if s.Healthy() {
			out = append(out, s)
		}
	}
	return out
}

// ProblemSkills returns broken or missing-requirement skills.
func (r *SkillListReport) ProblemSkills() []Skill {
	if r == nil {
		return nil
	}
	var out []Skill
	for _, s := range r.Skills {
		if s.Broken() {
			out = append(out, s)
		}
	}
	return out
}

// SkillCheckSummary summarizes `openclaw skills check --json` counts.
type SkillCheckSummary struct {
	Total               int `json:"total"`
	Eligible            int `json:"eligible"`
	ModelVisible        int `json:"modelVisible"`
	CommandVisible      int `json:"commandVisible"`
	Disabled            int `json:"disabled"`
	Blocked             int `json:"blocked"`
	AgentFiltered       int `json:"agentFiltered"`
	NotInjected         int `json:"notInjected"`
	MissingRequirements int `json:"missingRequirements"`
}

// SkillCheckReport is the parsed output of `openclaw skills check --json`.
type SkillCheckReport struct {
	AgentID             string            `json:"agentId"`
	WorkspaceDir        string            `json:"workspaceDir"`
	ManagedSkillsDir    string            `json:"managedSkillsDir"`
	Summary             SkillCheckSummary `json:"summary"`
	Eligible            []string          `json:"eligible"`
	ModelVisible        []string          `json:"modelVisible"`
	CommandVisible      []string          `json:"commandVisible"`
	Disabled            []string          `json:"disabled"`
	Blocked             []string          `json:"blocked"`
	MissingRequirements []Skill           `json:"missingRequirements"`
	RawOutput           string            `json:"-"`
}

// WorkshopProposal describes a learned or proposed skill in Skill Workshop.
type WorkshopProposal struct {
	Name         string   `json:"name"`
	Description  string   `json:"description"`
	Files        []string `json:"files"`
	SizeBytes    int64    `json:"sizeBytes"`
	UpdatedAtMs  int64    `json:"updatedAtMs"`
	UseCount     int      `json:"useCount"`
	LastUsedAtMs int64    `json:"lastUsedAtMs"`
	FilePath     string   `json:"filePath"`
	Content      string   `json:"content"`
	Archived     bool     `json:"archived"`
}

// WorkshopListReport is the parsed output of `openclaw skills workshop list --json`.
type WorkshopListReport struct {
	AgentID   string             `json:"agentId"`
	Mode      string             `json:"mode"`
	Root      string             `json:"root"`
	Proposals []WorkshopProposal `json:"skills"`
	Archived  []WorkshopProposal `json:"archived"`
	RawOutput string             `json:"-"`
}

// SelfReviewReport holds the result of a Bee skill self-review.
type SelfReviewReport struct {
	AgentID   string
	Prompt    string
	Summary   string
	RawOutput string
	Proposals []WorkshopProposal
}

// ProviderStatus describes one configured model provider's auth status.
type ProviderStatus struct {
	Provider string `json:"provider"`
	Kind     string `json:"kind"`
	Detail   string `json:"detail"`
}

// ModelStatusReport is the parsed output of `openclaw models status --json`.
type ModelStatusReport struct {
	ConfigPath            string           `json:"configPath"`
	AgentDir              string           `json:"agentDir"`
	DefaultModel          string           `json:"defaultModel"`
	ResolvedDefault       string           `json:"resolvedDefault"`
	Fallbacks             []string         `json:"fallbacks"`
	Allowed               []string         `json:"allowed"`
	MissingProvidersInUse []string         `json:"missingProvidersInUse"`
	ModelRouteIssues      []string         `json:"modelRouteIssues"`
	Providers             []ProviderStatus `json:"-"`
	RawOutput             string           `json:"-"`
}

// extractFirstJSON skips any leading non-JSON banner lines and returns the
// first complete JSON value in data.
func extractFirstJSON(data []byte) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty output")
	}
	var v json.RawMessage
	if err := json.NewDecoder(bytes.NewReader(trimmed)).Decode(&v); err == nil {
		return v, nil
	}
	for off := 0; off < len(trimmed); {
		nl := bytes.IndexByte(trimmed[off:], '\n')
		if nl < 0 {
			break
		}
		off += nl + 1
		rest := bytes.TrimLeft(trimmed[off:], " \t\r")
		if len(rest) > 0 && (rest[0] == '[' || rest[0] == '{') {
			if err := json.NewDecoder(bytes.NewReader(rest)).Decode(&v); err == nil {
				return v, nil
			}
		}
	}
	return nil, fmt.Errorf("invalid JSON output")
}

// checkCLIErrorObject checks if a JSON object represents an OpenClaw CLI error
// envelope like `{"ok":false,"error":{"message":"..."}}`.
func checkCLIErrorObject(raw json.RawMessage) error {
	if len(raw) == 0 || raw[0] != '{' {
		return nil
	}
	var env struct {
		OK    *bool `json:"ok"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && env.OK != nil && !*env.OK {
		msg := "openclaw reported ok=false"
		if env.Error != nil && env.Error.Message != "" {
			msg = env.Error.Message
		}
		return fmt.Errorf("%w: %s", ErrCommandFailed, msg)
	}
	return nil
}

// ParseSkillList decodes `openclaw skills list --json` output. It accepts
// either `{"skills": [...]}` or a bare `[...]` array.
func ParseSkillList(data []byte) (*SkillListReport, error) {
	raw, err := extractFirstJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := checkCLIErrorObject(raw); err != nil {
		return nil, err
	}
	switch raw[0] {
	case '[':
		var skills []Skill
		if err := json.Unmarshal(raw, &skills); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		return &SkillListReport{Skills: skills}, nil
	case '{':
		var rep SkillListReport
		if err := json.Unmarshal(raw, &rep); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		if rep.Skills == nil {
			return nil, fmt.Errorf("%w: object has no \"skills\" array", ErrInvalidOutput)
		}
		return &rep, nil
	default:
		return nil, fmt.Errorf("%w: unexpected JSON root", ErrInvalidOutput)
	}
}

// ParseSkillCheck decodes `openclaw skills check --json` output.
func ParseSkillCheck(data []byte) (*SkillCheckReport, error) {
	raw, err := extractFirstJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := checkCLIErrorObject(raw); err != nil {
		return nil, err
	}
	if raw[0] != '{' {
		return nil, fmt.Errorf("%w: expected JSON object", ErrInvalidOutput)
	}
	var rep SkillCheckReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if rep.Summary.Total == 0 && len(rep.Eligible) > 0 {
		rep.Summary.Eligible = len(rep.Eligible)
		rep.Summary.Total = len(rep.Eligible) + len(rep.Disabled) + len(rep.MissingRequirements)
	}
	return &rep, nil
}

// ParseWorkshopList decodes `openclaw skills workshop list --json` (or plain
// text output if an older CLI ignores `--json`).
func ParseWorkshopList(data []byte) (*WorkshopListReport, error) {
	raw, err := extractFirstJSON(data)
	if err == nil {
		if err := checkCLIErrorObject(raw); err != nil {
			return nil, err
		}
		switch raw[0] {
		case '[':
			var props []WorkshopProposal
			if err := json.Unmarshal(raw, &props); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
			}
			return &WorkshopListReport{Proposals: props}, nil
		case '{':
			var rep WorkshopListReport
			if err := json.Unmarshal(raw, &rep); err != nil {
				return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
			}
			for i := range rep.Archived {
				rep.Archived[i].Archived = true
			}
			return &rep, nil
		}
	}
	// Fallback for plain-text workshop list output:
	// "Agent: main  Mode: auto\nRoot: ...\n\nname  updated ...  description"
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "Agent:") {
		rep := &WorkshopListReport{}
		for _, line := range strings.Split(text, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "Agent:") || strings.HasPrefix(line, "Root:") {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) > 0 {
				rep.Proposals = append(rep.Proposals, WorkshopProposal{
					Name:        fields[0],
					Description: strings.Join(fields[1:], " "),
				})
			}
		}
		return rep, nil
	}
	return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
}

// ParseWorkshopShow decodes `openclaw skills workshop show <name> --json` or
// falls back to treating raw Markdown output as the skill content.
func ParseWorkshopShow(data []byte, fallbackName string) (*WorkshopProposal, error) {
	raw, err := extractFirstJSON(data)
	if err == nil && raw[0] == '{' {
		if err := checkCLIErrorObject(raw); err != nil {
			return nil, err
		}
		var prop WorkshopProposal
		if err := json.Unmarshal(raw, &prop); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
		}
		if prop.Name == "" {
			prop.Name = fallbackName
		}
		return &prop, nil
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, fmt.Errorf("%w: empty workshop skill output", ErrInvalidOutput)
	}
	return &WorkshopProposal{Name: fallbackName, Content: content}, nil
}

// ParseAgentReply extracts the human-readable text reply from `openclaw agent --json`
// or returns the trimmed text when plain output is produced.
func ParseAgentReply(data []byte) string {
	raw, err := extractFirstJSON(data)
	if err != nil {
		return strings.TrimSpace(string(data))
	}
	var obj struct {
		Reply   string `json:"reply"`
		Message string `json:"message"`
		Output  string `json:"output"`
		Text    string `json:"text"`
		Summary string `json:"summary"`
		Result  struct {
			Reply   string `json:"reply"`
			Message string `json:"message"`
			Text    string `json:"text"`
		} `json:"result"`
		Payloads []struct {
			Text string `json:"text"`
		} `json:"payloads"`
	}
	if err := json.Unmarshal(raw, &obj); err == nil {
		for _, candidate := range []string{
			obj.Reply, obj.Message, obj.Output, obj.Text, obj.Summary,
			obj.Result.Reply, obj.Result.Message, obj.Result.Text,
		} {
			if s := strings.TrimSpace(candidate); s != "" {
				return s
			}
		}
		if len(obj.Payloads) > 0 {
			var parts []string
			for _, p := range obj.Payloads {
				if t := strings.TrimSpace(p.Text); t != "" {
					parts = append(parts, t)
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n")
			}
		}
	}
	return strings.TrimSpace(string(raw))
}

// ParseModelsStatus decodes `openclaw models status --json` output.
func ParseModelsStatus(data []byte) (*ModelStatusReport, error) {
	raw, err := extractFirstJSON(data)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	if err := checkCLIErrorObject(raw); err != nil {
		return nil, err
	}
	if raw[0] != '{' {
		return nil, fmt.Errorf("%w: expected JSON object", ErrInvalidOutput)
	}
	var wire struct {
		ConfigPath      string   `json:"configPath"`
		AgentDir        string   `json:"agentDir"`
		DefaultModel    string   `json:"defaultModel"`
		ResolvedDefault string   `json:"resolvedDefault"`
		Fallbacks       []string `json:"fallbacks"`
		Allowed         []string `json:"allowed"`
		Auth            struct {
			MissingProvidersInUse []string `json:"missingProvidersInUse"`
			ModelRouteIssues      []string `json:"modelRouteIssues"`
			Providers             []struct {
				Provider  string `json:"provider"`
				Effective struct {
					Kind   string `json:"kind"`
					Detail string `json:"detail"`
				} `json:"effective"`
			} `json:"providers"`
		} `json:"auth"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOutput, err)
	}
	rep := &ModelStatusReport{
		ConfigPath:            wire.ConfigPath,
		AgentDir:              wire.AgentDir,
		DefaultModel:          wire.DefaultModel,
		ResolvedDefault:       wire.ResolvedDefault,
		Fallbacks:             wire.Fallbacks,
		Allowed:               wire.Allowed,
		MissingProvidersInUse: wire.Auth.MissingProvidersInUse,
		ModelRouteIssues:      wire.Auth.ModelRouteIssues,
	}
	for _, p := range wire.Auth.Providers {
		rep.Providers = append(rep.Providers, ProviderStatus{
			Provider: p.Provider,
			Kind:     p.Effective.Kind,
			Detail:   p.Effective.Detail,
		})
	}
	return rep, nil
}

// ParseVersion extracts the concise OpenClaw version string from CLI output.
func ParseVersion(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if idx := strings.Index(line, "—"); idx > 0 {
			return strings.TrimSpace(line[:idx])
		}
		return line
	}
	return "unknown"
}
