// Package agents models discovered OpenClaw agents ("Bees").
//
// Nothing here may hard-code a specific agent, skill, or host name. A Bee is
// always the product of discovery against a Hive.
package agents

import (
	"sort"
	"strings"
	"time"
)

// Bee is a single OpenClaw agent discovered on a Hive.
type Bee struct {
	// ID is the agent identifier reported by OpenClaw.
	ID string `json:"id"`
	// Name is the display name, if the agent reports one.
	Name string `json:"name,omitempty"`
	// Hive is the name of the Hive this Bee was discovered on.
	Hive string `json:"hive,omitempty"`
	// Runtime is the agent runtime/model identifier, if reported.
	Runtime string `json:"runtime,omitempty"`
	// Emoji is the agent's identity emoji, if reported.
	Emoji string `json:"emoji,omitempty"`
	// Workspace is the agent's workspace directory on its Hive, if reported.
	Workspace string `json:"workspace,omitempty"`
	// AgentDir is the agent's state directory on its Hive, if reported.
	AgentDir string `json:"agentDir,omitempty"`
	// Status is a free-form status string, if OpenClaw reports one.
	Status string `json:"status,omitempty"`
	// Default reports whether OpenClaw marks this as the Hive's default agent.
	Default bool `json:"default,omitempty"`
}

// Key uniquely identifies a Bee across the whole swarm. Agent IDs are only
// unique per Hive, so the same ID on two Hives yields two distinct keys.
func (b Bee) Key() string { return b.Hive + "/" + b.ID }

// DisplayName returns Name, falling back to ID.
func (b Bee) DisplayName() string {
	if b.Name != "" {
		return b.Name
	}
	return b.ID
}

// HiveStatus records the outcome of the most recent scan of one Hive.
type HiveStatus struct {
	Hive      string
	Err       error
	ScannedAt time.Time
	Duration  time.Duration
}

// OK reports whether the Hive scanned successfully.
func (s HiveStatus) OK() bool { return s.Err == nil }

// Inventory is the set of Bees discovered across one or more Hives, grouped
// by Hive name. An unreachable Hive yields an empty slice plus a failed
// HiveStatus, never an error that takes down the whole inventory (roadmap:
// "Partial failure is normal").
type Inventory struct {
	// Hives lists Hive names in scan (configuration) order.
	Hives  []string
	ByHive map[string][]Bee
	Status map[string]HiveStatus
}

// NewInventory returns an empty Inventory ready for use.
func NewInventory() *Inventory {
	return &Inventory{ByHive: make(map[string][]Bee), Status: make(map[string]HiveStatus)}
}

// Record stores the scan outcome for one Hive, replacing any previous one.
// Bees are stamped with the Hive name and sorted by ID.
func (i *Inventory) Record(status HiveStatus, bees []Bee) {
	if _, seen := i.Status[status.Hive]; !seen {
		i.Hives = append(i.Hives, status.Hive)
	}
	out := make([]Bee, len(bees))
	for n, b := range bees {
		b.Hive = status.Hive
		out[n] = b
	}
	sort.SliceStable(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	i.ByHive[status.Hive] = out
	i.Status[status.Hive] = status
}

// Clone returns a copy that can be modified without affecting i.
func (i *Inventory) Clone() *Inventory {
	c := NewInventory()
	c.Hives = append([]string(nil), i.Hives...)
	for k, v := range i.ByHive {
		c.ByHive[k] = append([]Bee(nil), v...)
	}
	for k, v := range i.Status {
		c.Status[k] = v
	}
	return c
}

// Failed returns the status of every Hive whose last scan failed, in order.
func (i *Inventory) Failed() []HiveStatus {
	var out []HiveStatus
	for _, h := range i.Hives {
		if s := i.Status[h]; !s.OK() {
			out = append(out, s)
		}
	}
	return out
}

// Total returns the number of discovered Bees across all Hives.
func (i *Inventory) Total() int {
	n := 0
	for _, bees := range i.ByHive {
		n += len(bees)
	}
	return n
}

// AllBees returns every discovered Bee in Hive order, then ID order.
func (i *Inventory) AllBees() []Bee {
	if i == nil {
		return nil
	}
	var out []Bee
	for _, h := range i.Hives {
		out = append(out, i.ByHive[h]...)
	}
	return out
}

// FilterBees returns discovered Bees matching query.
//
// It supports both free-text multi-word substring search (across ID, display
// name, Hive, model/runtime, status, workspace, and hive/id key) and structured
// filter tokens for large swarms:
//   - `hive:<name>` / `host:<name>`
//   - `model:<substr>` / `runtime:<substr>`
//   - `id:<substr>` / `name:<substr>`
//   - `status:<substr>` (including `status:online` / `status:offline` for Hive state)
//   - `default:true` / `default:false`
//   - `workspace:<substr>`
//   - `!token` or `-token` to negate any token or field filter
//
// Multiple whitespace-separated tokens are combined with logical AND. An empty
// query returns AllBees().
func (i *Inventory) FilterBees(query string) []Bee {
	all := i.AllBees()
	q := strings.TrimSpace(query)
	if q == "" {
		return all
	}
	tokens := strings.Fields(strings.ToLower(q))
	var out []Bee
	for _, b := range all {
		hiveOnline := true
		if i != nil && i.Status != nil {
			if st, ok := i.Status[b.Hive]; ok {
				hiveOnline = st.OK()
			}
		}
		if matchBeeTokens(b, hiveOnline, tokens) {
			out = append(out, b)
		}
	}
	return out
}

func matchBeeTokens(b Bee, hiveOnline bool, tokens []string) bool {
	for _, tok := range tokens {
		negate := false
		if strings.HasPrefix(tok, "!") && len(tok) > 1 {
			negate = true
			tok = tok[1:]
		} else if strings.HasPrefix(tok, "-") && len(tok) > 1 {
			negate = true
			tok = tok[1:]
		}
		matched := matchSingleBeeToken(b, hiveOnline, tok)
		if negate {
			matched = !matched
		}
		if !matched {
			return false
		}
	}
	return true
}

func matchSingleBeeToken(b Bee, hiveOnline bool, tok string) bool {
	sep := strings.IndexAny(tok, ":=")
	if sep > 0 && sep < len(tok)-1 {
		field := tok[:sep]
		val := tok[sep+1:]
		switch field {
		case "hive", "host":
			return strings.Contains(strings.ToLower(b.Hive), val)
		case "model", "runtime":
			return strings.Contains(strings.ToLower(b.Runtime), val)
		case "id":
			return strings.Contains(strings.ToLower(b.ID), val)
		case "name":
			return strings.Contains(strings.ToLower(b.DisplayName()), val)
		case "workspace", "dir":
			return strings.Contains(strings.ToLower(b.Workspace), val) ||
				strings.Contains(strings.ToLower(b.AgentDir), val)
		case "status", "state":
			if val == "online" {
				return hiveOnline
			}
			if val == "offline" {
				return !hiveOnline
			}
			return strings.Contains(strings.ToLower(b.Status), val)
		case "default":
			wantDefault := val == "true" || val == "yes" || val == "1"
			return b.Default == wantDefault
		}
	}
	return strings.Contains(strings.ToLower(b.ID), tok) ||
		strings.Contains(strings.ToLower(b.DisplayName()), tok) ||
		strings.Contains(strings.ToLower(b.Hive), tok) ||
		strings.Contains(strings.ToLower(b.Runtime), tok) ||
		strings.Contains(strings.ToLower(b.Status), tok) ||
		strings.Contains(strings.ToLower(b.Workspace), tok) ||
		strings.Contains(strings.ToLower(b.Key()), tok)
}

// LastScanTime returns the most recent ScannedAt timestamp across all Hives,
// or the zero Time if no Hive has been scanned.
func (i *Inventory) LastScanTime() time.Time {
	if i == nil {
		return time.Time{}
	}
	var latest time.Time
	for _, h := range i.Hives {
		if t := i.Status[h].ScannedAt; t.After(latest) {
			latest = t
		}
	}
	return latest
}
