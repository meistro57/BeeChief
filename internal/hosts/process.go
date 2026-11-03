package hosts

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os/exec"
	"strings"
	"time"
)

// waitDelay caps how long Run waits for output pipes after the process is
// killed, so a grandchild holding stdout open cannot stall BeeChief.
const waitDelay = 2 * time.Second

// runProcess executes argv locally with timeout handling and classifies
// failures into *Error. display is the command shown in errors.
func runProcess(ctx context.Context, hive string, timeout time.Duration, display string, argv []string) (Result, *exec.ExitError, error) {
	if _, ok := ctx.Deadline(); !ok {
		if timeout <= 0 {
			timeout = DefaultTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = waitDelay

	start := time.Now()
	err := cmd.Run()
	res := Result{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(start),
		ExitCode: -1,
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}

	newErr := func(kind ErrorKind, cause error) error {
		return &Error{Hive: hive, Kind: kind, Command: display, ExitCode: res.ExitCode, Stderr: res.Stderr, Err: cause}
	}

	switch ctxErr := ctx.Err(); {
	case errors.Is(ctxErr, context.DeadlineExceeded):
		return res, nil, newErr(KindTimeout, ctxErr)
	case errors.Is(ctxErr, context.Canceled):
		return res, nil, newErr(KindCanceled, ctxErr)
	}

	if err == nil {
		return res, nil, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return res, exitErr, nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return res, nil, newErr(KindNotFound, err)
	}
	return res, nil, newErr(KindStart, err)
}

// displayCommand renders command and args for error messages.
func displayCommand(command string, args []string) string {
	return strings.Join(append([]string{command}, args...), " ")
}
