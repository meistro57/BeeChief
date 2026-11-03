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
