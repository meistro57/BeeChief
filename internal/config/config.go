// Package config loads BeeChief's Hive configuration.
//
// Hives are configured; Bees (agents) are discovered. This package therefore
// only ever describes machines and connection details, never agent names.
package config

// Hive describes a single machine BeeChief can reach.
type Hive struct {
	// Name is a stable operator-chosen identifier (e.g. "basement", "vps").
	Name string `yaml:"name" json:"name"`
	// Host is an SSH target ("user@host") or "local" for the local machine.
	Host string `yaml:"host" json:"host"`
	// Label is an optional human-friendly description.
	Label string `yaml:"label,omitempty" json:"label,omitempty"`
}

// Config is the root configuration document.
type Config struct {
	Hives []Hive `yaml:"hives" json:"hives"`
}

// Default returns an empty configuration with no Hives configured.
//
// BeeChief must start cleanly with zero Hives: discovery simply finds nothing
// rather than failing.
func Default() Config {
	return Config{Hives: []Hive{}}
}
