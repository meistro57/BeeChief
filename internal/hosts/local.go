package hosts

import (
	"context"
	"time"
)

// LocalExecution runs commands on the machine hosting BeeChief.
//
// Commands are executed directly (no shell), so args are never re-parsed.
type LocalExecution struct {
	// Hive is the configured Hive name, used in errors.
	Hive string
	// Timeout bounds each command when the context has no deadline.
	// Zero means DefaultTimeout.
	Timeout time.Duration
}

// NewLocal returns an Execution backed by the local machine.
func NewLocal() *LocalExecution { return &LocalExecution{} }

// Run implements Execution.
func (l *LocalExecution) Run(ctx context.Context, command string, args ...string) (Result, error) {
	argv := append([]string{command}, args...)
	res, _, err := runProcess(ctx, l.Hive, l.Timeout, displayCommand(command, args), argv)
	return res, err
}
