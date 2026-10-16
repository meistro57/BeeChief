// Package agents models discovered OpenClaw agents ("Bees").
//
// Nothing here may hard-code a specific agent, skill, or host name. A Bee is
// always the product of discovery against a Hive.
package agents

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
}

// Inventory is the set of Bees discovered across one or more Hives, grouped
// by Hive name. An unreachable Hive yields an empty slice, never an error
// that takes down the whole inventory (roadmap: "Partial failure is normal").
type Inventory struct {
	ByHive map[string][]Bee
}

// NewInventory returns an empty Inventory ready for use.
func NewInventory() *Inventory {
	return &Inventory{ByHive: make(map[string][]Bee)}
}

// Total returns the number of discovered Bees across all Hives.
func (i *Inventory) Total() int {
	n := 0
	for _, bees := range i.ByHive {
		n += len(bees)
	}
	return n
}
