<p align="right">
  <img src="assets/BeeChief_avatar.svg" alt="BeeChief Avatar" width="96" />
</p>

# BeeChief architecture

This file explains how the code is laid out and why. Each layer depends only on the layers below it.

```text
cmd/beechief          entrypoint
internal/app          wiring: load/save config → build Scanner, Ops, Job Runner & Event Store → start TUI, web, or CLI
internal/web          embedded browser UI + token-protected JSON API (swarm, Bee, Hive, logs, jobs, settings)
internal/jobs         five-field cron parser, jobs.yaml persistence, and in-process job scheduler
internal/tui          Bubble Tea UI; consumes Inventory and Ops interface without shell logic (NO_COLOR & viewport windowing)
internal/events       secret-redacted JSONL operational event store with ring buffer and size-based log rotation
internal/report       Honey Report diagnostic collector, Markdown renderer, and secret redaction
internal/swarm        bounded-concurrency multi-Bee self-review coordinator with cancellation
internal/logs         unified log browser adapters (journalctl, docker, openclaw, agent, events) + ANSI stripping
internal/services     capability-detected systemd, Docker, and Ollama/model troubleshooting
internal/diagnostics  read-only Hive Health quick diagnostic checks and severity sorting
internal/discovery    concurrent per-Hive scan, partial failure, rescan
internal/openclaw     the only package that knows the OpenClaw CLI (commands, capability probes, JSON/text parsing)
internal/hosts        generic Execution: local and SSH (with ControlMaster multiplexing), timeouts, structured errors
internal/config       YAML Hive configuration loader, schema version migrator, validator, and round-trip mutation API
internal/agents       plain data models: Bee, Inventory, HiveStatus, and structured boolean/field query filtering
```

```mermaid
flowchart TD
    CLI["cmd/beechief"] --> APP["internal/app"]
    APP --> TUI["internal/tui (Bubble Tea Views + Ops Interface)"]
    APP --> WEB["internal/web (Embedded UI + JSON API)"]
    APP --> JOBS["internal/jobs (Cron Scheduler)"]
    APP --> EVT["internal/events (Redacted JSONL Event Store)"]
    APP --> REP["internal/report (Honey Report + Secret Redaction)"]
    APP --> SWARM["internal/swarm (Multi-Bee Coordinator)"]
    APP --> LOGS["internal/logs (Journalctl / Docker / Gateway / Agent / Events)"]
    APP --> SVC["internal/services (systemd / Docker / Ollama & Capabilities)"]
    APP --> DIAG["internal/diagnostics (11 Hive Health Checks)"]
    APP --> DISC["internal/discovery (Concurrent Hive Scanner)"]
    EVT --> REP
    REP --> DIAG
    REP --> SVC
    REP --> LOGS
    REP --> OC["internal/openclaw (CLI Adapter + JSON/Text Parsers)"]
    SWARM --> OC
    SVC --> OC
    DIAG --> OC
    DISC --> OC
    OC --> HOSTS["internal/hosts (Execution: Local & SSH + ControlMaster)"]
    LOGS --> HOSTS
    SVC --> HOSTS
    DIAG --> HOSTS
    HOSTS --> CFG["internal/config (YAML Load/Migrate/Save/Validate)"]
    DISC --> AGENTS["internal/agents (Bee, Inventory, Structured Filters)"]
    TUI --> AGENTS
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
On non-Windows hosts, `hosts.ForHive` enables OpenSSH connection multiplexing
by default (`ControlMaster=auto`, `ControlPersist=60s`,
`ControlPath=/tmp/beechief-ssh-%C`) so repeated diagnostics, skill checks, and
log fetches reuse a single authenticated SSH session. A context deadline
(`DefaultTimeout` when the caller doesn't set one) plus `exec.Cmd.WaitDelay`
means `Run` always returns, even if a child process keeps its pipes open.

**Errors vs results.** If a command ran and exited non-zero, `Run` returns a
`Result` with that `ExitCode` and a nil error. That lets callers interpret
exit codes in their own terms, for example 127 → OpenClaw not installed. If
the command could not run at all, `Run` returns a `*hosts.Error` with a
`Kind`: `unreachable`, `timeout`, `canceled`, `not_found`, or
`start_failed`. The error includes the Hive name, the command, and the
captured stderr. OpenSSH's own failure status, 255, maps to `unreachable`.

**OpenClaw isolated in one package, parsing in one file.** Only
`internal/openclaw` constructs OpenClaw commands (`agents list`, `skills list`,
`skills check`, `skills workshop list/show`, `agent --message`, `models status`,
`health`, `--version`), probes subcommand support (`ProbeCapabilities`), and
gracefully retries without `--json` (`runWithJSONFallback`) when interacting
with older OpenClaw CLI versions. Only `internal/openclaw/parse.go` understands
its JSON (both camelCase and legacy snake_case / `items` / `data` wrappers) and
fallback plain-text formats. The parser skips banner lines before JSON, so a
change in CLI output means editing one file. Discovery and command failures use
sentinel errors (`ErrNotInstalled`, `ErrCommandFailed`, `ErrInvalidOutput`,
`ErrUnsupported`). Transport errors pass through as `*hosts.Error`, so
`errors.Is` and `hosts.IsKind` both work.

**Partial failure is normal.** `discovery.Scanner` scans Hives concurrently,
and each Hive gets its own timeout. Each outcome is written to
`agents.Inventory` as a `HiveStatus`, which is either OK or holds an error.
Healthy Hives always load, even when others fail. `Rescan()` rescans
everything; `Rescan(ctx, "hive")` refreshes only the named Hives and merges
the results into the last inventory. Bees are keyed as `hive/id`, so the
same agent ID on different Hives never collides. Similarly, `diagnostics.Run`
isolates each Hive Health check (including panic recovery), and
`swarm.Coordinator` isolates per-Bee review failures while preserving
completed reviews.

**Capability detection & safe-by-default operations.** `internal/diagnostics`
and `internal/services` probe whether optional subsystems (`systemd`, `Docker`,
`Ollama`) exist on a Hive before inspecting them, reporting absent tools as
`unavailable` rather than failing. All inspection is read-only; state-changing
systemd actions (`start`, `stop`, `restart`) require `confirmed == true` at the
API boundary (`services.ErrConfirmationRequired`) and an explicit `[y/N]`
confirmation in the TUI. Workshop proposals are never auto-applied.

**Structured diagnostics, event persistence & redaction.** `internal/report`
builds portable **Honey Reports** directly from structured domain outputs
(`Inventory`, `HiveHealthReport`, `HiveServicesReport`, `SkillCheckSummary`,
`WorkshopListReport`, and `logs.Entry`) rather than scraping TUI strings, and
passes the rendered Markdown through `report.Redact` to strip tokens,
passwords, authorization headers, URL credentials, and PEM private keys.
`internal/events` records structured operational events (scans, skill checks,
reviews, health passes, systemd actions, report exports, config updates) into
both an in-memory ring buffer and a size-rotated `0600` JSONL file
(`events.jsonl`), scrubbing every field through `report.Redact` before storage
and exposing the history as the `events` log source in the Unified Log Browser.

**Configured vs discovered.** `config` only describes machines. No agent,
skill, or host name is hard-coded anywhere. Discovery produces every Bee. A
missing config file starts BeeChief with zero Hives. `config.Migrate`
automatically upgrades legacy v0 schemas (`hosts:` top-level key, `hostname:` /
`username:` field aliases) to `CurrentSchemaVersion` (`1`) while rejecting
unknown keys (such as a stray `password:`) and future unsupported schema
versions (`version > 1`). `config.Save` stamps `version: 1`, validates before
writing, and preserves existing file permissions.

**Browser UI shares the same backend.** `internal/web` depends only on a
narrow `web.Backend` interface, which `app.backend` satisfies. It does not
import `app` or `tui`, so terminal and browser clients exercise the same
operations and redaction paths. The UI is embedded with `go:embed` and uses
no CDN or build step. Requests require a per-process CSRF token and the server
binds loopback by default; `--read-only` rejects config mutations, service
control, instructions, Swarm Review starts, and job edits/runs. Long-running
Swarm Reviews run in a background coordinator and are polled, so HTTP handlers
never block on `coordinator.Run`.

**Scheduled jobs are first-class operations.** `internal/jobs` parses
five-field cron expressions, persists job definitions in a separate
`jobs.yaml`, and runs `scan`, `health`, and `report` jobs through a
`jobs.Runner` implemented by `app.backend`. `beechief cron --once` makes every
enabled job runnable from external cron, while the daemon form and the web UI
use the same manager. Every run is recorded as a redacted `job_run` event.

**UI knows nothing about operations.** The TUI receives a `ScanFunc` and a
`tui.Ops` interface, rendering the resulting domain structs. Scans, skill
checks, self-reviews, service inspections, log fetches, and exports run as
async `tea.Cmd`s so the UI never blocks on network or subprocess I/O. The TUI
automatically detects `NO_COLOR` / `TERM=dumb` for plain-text glyph rendering,
adapts gracefully to compact terminal dimensions (`<50x12`), and windows large
Bee/Skill/Log lists dynamically.
