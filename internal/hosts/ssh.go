package hosts

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// DefaultConnectTimeout bounds the SSH TCP connect + handshake.
const DefaultConnectTimeout = 10 * time.Second

// sshUnreachableExit is the exit status OpenSSH reserves for its own
// failures (connect, auth, host key). A remote command that itself exits
// 255 is indistinguishable and is likewise reported as unreachable.
const sshUnreachableExit = 255

// SSHExecution runs commands on a remote Hive via the system ssh client.
//
// It deliberately shells out to OpenSSH rather than embedding an SSH
// library so the operator's ~/.ssh/config, ssh-agent, ProxyJump, known_hosts,
// and key files all apply unchanged. No credentials are ever handled here.
// BatchMode is always on: anything that would prompt fails immediately.
type SSHExecution struct {
	// Hive is the configured Hive name, used in errors.
	Hive string
	// Host is a hostname, IP, ~/.ssh/config alias, or "user@host".
	Host string
	// User optionally overrides the login user (ssh -l).
	User string
	// Port optionally overrides the port (ssh -p). Zero defers to config.
	Port int
	// ConfigFile optionally replaces ~/.ssh/config (ssh -F). Mainly for tests.
	ConfigFile string
	// ConnectTimeout bounds connection setup. Zero means DefaultConnectTimeout.
	ConnectTimeout time.Duration
	// Timeout bounds each command when the context has no deadline.
	// Zero means DefaultTimeout.
	Timeout time.Duration
	// Binary is the ssh executable. Empty means "ssh" from PATH.
	Binary string
}

// Args returns the full ssh argv (excluding the binary) that Run would use
// for command and args. Exposed so command construction can be tested and
// shown to the operator.
func (s *SSHExecution) Args(command string, args ...string) []string {
	connect := s.ConnectTimeout
	if connect <= 0 {
		connect = DefaultConnectTimeout
	}
	secs := int(connect.Round(time.Second) / time.Second)
	if secs < 1 {
		secs = 1
	}

	argv := []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(secs),
	}
	if s.ConfigFile != "" {
		argv = append(argv, "-F", s.ConfigFile)
	}
	if s.Port > 0 {
		argv = append(argv, "-p", strconv.Itoa(s.Port))
	}
	if s.User != "" {
		argv = append(argv, "-l", s.User)
	}
	return append(argv, "--", s.Host, RemoteCommand(command, args...))
}

// Run implements Execution.
func (s *SSHExecution) Run(ctx context.Context, command string, args ...string) (Result, error) {
	bin := s.Binary
	if bin == "" {
		bin = "ssh"
	}
	argv := append([]string{bin}, s.Args(command, args...)...)
	display := fmt.Sprintf("ssh %s: %s", s.target(), displayCommand(command, args))

	res, exitErr, err := runProcess(ctx, s.Hive, s.Timeout, display, argv)
	if err != nil {
		// A missing local ssh binary is a reachability problem for the Hive.
		if he, ok := err.(*Error); ok && he.Kind == KindNotFound {
			he.Kind = KindUnreachable
		}
		return res, err
	}
	if exitErr != nil && res.ExitCode == sshUnreachableExit {
		return res, &Error{
			Hive: s.Hive, Kind: KindUnreachable, Command: display,
			ExitCode: res.ExitCode, Stderr: res.Stderr, Err: exitErr,
		}
	}
	return res, nil
}

func (s *SSHExecution) target() string {
	t := s.Host
	if s.User != "" {
		t = s.User + "@" + t
	}
	if s.Port > 0 {
		t += ":" + strconv.Itoa(s.Port)
	}
	return t
}

// RemoteCommand joins command and args into a single string safe to hand to
// the remote POSIX shell, quoting each word as needed.
func RemoteCommand(command string, args ...string) string {
	words := make([]string, 0, len(args)+1)
	words = append(words, ShellQuote(command))
	for _, a := range args {
		words = append(words, ShellQuote(a))
	}
	return strings.Join(words, " ")
}

// ShellQuote quotes s for a POSIX shell. Words made only of safe characters
// are returned unchanged for readability.
func ShellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			strings.ContainsRune("@%+=:,./_-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
