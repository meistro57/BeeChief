// Package hosts defines how BeeChief reaches a Hive.
//
// Local and SSH Hives expose the same Execution interface so that everything
// above this layer is transport-agnostic (roadmap: "One execution path").
package hosts

import "context"

// Result is the outcome of running a command on a Hive.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Ok reports whether the command exited successfully.
func (r Result) Ok() bool { return r.ExitCode == 0 }

// Execution runs read-only commands against a Hive.
//
// Implementations are expected to be safe-by-default: the interface is
// deliberately narrow, and callers should prefer read-only commands.
type Execution interface {
	// Run executes a command on the Hive and returns its output.
	Run(ctx context.Context, command string) (Result, error)
}

// LocalExecution runs commands on the machine hosting BeeChief.
type LocalExecution struct{}

// NewLocal returns an Execution backed by the local machine.
func NewLocal() *LocalExecution { return &LocalExecution{} }
