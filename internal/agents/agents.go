// Package agents models discovered OpenClaw agents ("Bees").
//
// Nothing here may hard-code a specific agent, skill, or host name. A Bee is
// always the product of discovery against a Hive.
package agents

import (
	"sort"
	"time"
)

// Bee is a single OpenClaw agent discovered on a Hive.
type Bee struct {
	// ID is the agent identifier reported by OpenClaw.
	ID string
	// Name is the display name, if the agent reports one.
	Name string
	// Hive is the name of the Hive this Bee was discovered on.
	Hive string
	// Runtime is the agent runtime/model identifier, if reported.
	Runtime string
	// Emoji is the agent's identity emoji, if reported.
	Emoji string
	// Workspace is the agent's workspace directory on its Hive, if reported.
	Workspace string
	// AgentDir is the agent's state directory on its Hive, if reported.
	AgentDir string
	// Status is a free-form status string, if OpenClaw reports one.
	Status string
	// Default reports whether OpenClaw marks this as the Hive's default agent.
	Default bool
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
