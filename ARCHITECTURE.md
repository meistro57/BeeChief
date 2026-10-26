# BeeChief architecture

This file explains how the code is laid out and why. Each layer depends only on the layers below it.

```text
cmd/beechief      entrypoint
internal/app      wiring: load config → build Scanner → start TUI
internal/tui      Bubble Tea UI; consumes agents.Inventory via an injected ScanFunc
internal/discovery  concurrent per-Hive scan, partial failure, rescan
internal/openclaw   the only package that knows the OpenClaw CLI (command + parsing)
internal/hosts      generic Execution: local and SSH, timeouts, structured errors
internal/config     YAML Hive configuration
internal/agents     plain data models: Bee, Inventory, HiveStatus
```

## Design choices

**One execution interface.** `hosts.Execution` has a single method,
`Run(ctx, command, args...)`. Local and SSH Hives implement it the same way,
so everything above it works the same whatever the transport. Nothing in
`hosts` is specific to OpenClaw, so later diagnostics (systemd, Docker,
Ollama, network) can reuse it unchanged.

**Arguments are words, not shell strings.** Local execution calls the binary
directly with no shell. For SSH, OpenSSH always hands the remote command to
the remote shell, so each word is quoted with POSIX single quotes
(`hosts.ShellQuote`). Config validation rejects host and user values that
start with `-`, so they can't be read as ssh options. The argv also includes a
`--` before the host.

**System `ssh` instead of an embedded SSH library.** The operator's
`~/.ssh/config`, agent, keys, `ProxyJump`, and `known_hosts` all work without
BeeChief ever touching credentials. `BatchMode=yes` turns every interactive
prompt into a fast failure. `ConnectTimeout` bounds the connection handshake.
A context deadline (`DefaultTimeout` when the caller doesn't set one) plus
`exec.Cmd.WaitDelay` means `Run` always returns, even if a child process
keeps its pipes open.

**Errors vs results.** If a command ran and exited non-zero, `Run` returns a
`Result` with that `ExitCode` and a nil error. That lets callers interpret
exit codes in their own terms, for example 127 → OpenClaw not installed. If
the command could not run at all, `Run` returns a `*hosts.Error` with a
`Kind`: `unreachable`, `timeout`, `canceled`, `not_found`, or
`start_failed`. The error includes the Hive name, the command, and the
captured stderr. OpenSSH's own failure status, 255, maps to `unreachable`.

**OpenClaw isolated in one package, parsing in one file.** Only
`internal/openclaw` constructs OpenClaw commands. Only
`internal/openclaw/parse.go` understands its JSON. The parser accepts a bare
array or `{"agents": [...]}`, ignores unknown fields, and skips banner lines
before the JSON, so a change in CLI output means editing one file. Discovery
failures are sentinel errors (`ErrNotInstalled`, `ErrCommandFailed`,
`ErrInvalidOutput`). Transport errors pass through as `*hosts.Error`, so
`errors.Is` and `hosts.IsKind` both work.

**Partial failure is normal.** `discovery.Scanner` scans Hives concurrently,
and each Hive gets its own timeout. Each outcome is written to
`agents.Inventory` as a `HiveStatus`, which is either OK or holds an error.
Healthy Hives always load, even when others fail. `Rescan()` rescans
everything; `Rescan(ctx, "hive")` refreshes only the named Hives and merges
the results into the last inventory. Bees are keyed as `hive/id`, so the
same agent ID on different Hives never collides.

**Configured vs discovered.** `config` only describes machines. No agent,
skill, or host name is hard-coded anywhere. Discovery produces every Bee. A
missing config file starts BeeChief with zero Hives. A malformed file, or one
with unknown keys, is an error, so mistakes and stray secrets don't go
unnoticed.

**UI knows nothing about operations.** The TUI receives a `ScanFunc` and
renders the resulting `agents.Inventory`. The scan runs as an async
`tea.Cmd`, so the UI never blocks on the network.
