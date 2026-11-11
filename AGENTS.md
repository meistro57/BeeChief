# AGENTS.md

BeeChief is a Go terminal operations console for discovering, inspecting, and
diagnosing OpenClaw agents across multiple machines. Machines are **Hives**
(configured), agents are **Bees** (discovered), skills are the **Comb**.

Read `ARCHITECTURE.md` for the design rationale and `README.md` for user-facing
features. This file covers what an agent needs to change code safely.

## Commands

```bash
make all          # vet + test + build (the real gate; `make test` alone skips vet)
make build        # CGO_ENABLED=0 build to ./beechief with version ldflags
make test         # go test -race ./...
make vet          # go vet ./...
make release      # cross-platform archives + SHA256SUMS.txt into dist/
make install      # ./scripts/install.sh --from-source
go run ./cmd/beechief
go run ./cmd/beechief diagnose --export --output honey-report.md
```

There is no configured linter or formatter task. `go vet` and `go test -race`
are the supported gates, which is what CI runs (`.github/workflows/ci.yml`).

## Architecture and data flow

Strict one-way dependency layering. Higher layers never leak downward:
`cmd -> app -> {tui, events, report, swarm, logs, services, diagnostics, discovery} -> openclaw -> hosts -> config`, with `agents` as shared plain data.

```text
Hive config -> hosts.Execution (local/SSH) -> openclaw.Client -> parse.go
                                   |
discovery.Scanner -> agents.Inventory -> tui.Model -> views.go
                                   |
                     report / events / logs / diagnostics / services / swarm
```

- `cmd/beechief/main.go` only delegates to `app.Run()`.
- `internal/app` owns startup, config load/save, CLI dispatch (`version`,
  `diagnose`), backend construction, and the `tui.Ops` implementation. It
  contains no rendering and no raw shell strings.
- `internal/tui` is the Bubble Tea model. `app.go` holds state, `Update`,
  messages, and key handling; `views.go` holds rendering. It must not contain
  shell commands or OpenClaw specifics.
- `internal/hosts` is the only transport. Local execution uses argv directly
  (no shell); SSH shells out to the system `ssh` with `BatchMode=yes`, applies
  ControlMaster multiplexing on non-Windows, and POSIX-quotes each remote word.
- `internal/openclaw` is the only place that constructs `openclaw` commands.
  `openclaw.go` owns commands/fallbacks; `parse.go` is the only file that
  understands OpenClaw JSON and plain-text output. A CLI output change should
  touch `parse.go` alone.
- `internal/config` only describes machines. Never add agent, skill, or host
  names to code or config.
- `internal/agents` holds plain domain data (`Bee`, `Inventory`,
  `HiveStatus`) and filtering. No I/O.

### Key invariants

- **Hives configured, Bees discovered.** Never hard-code an agent or skill.
- **Errors vs results.** A command that ran and exited non-zero is
  `hosts.Result{ExitCode: ...}` with a nil error. An error means it could not
  run at all and is a `*hosts.Error` with a `Kind`
  (`unreachable`, `timeout`, `canceled`, `not_found`, `start_failed`).
  Use `hosts.IsKind` and `errors.Is`; OpenClaw sentinels live in
  `openclaw` (`ErrNotInstalled`, `ErrCommandFailed`, `ErrInvalidOutput`,
  `ErrUnsupported`).
- **Arguments are words, not strings.** Pass command and args separately.
  Never build a shell pipeline or concatenate user input into a command.
- **Partial failure is normal.** One bad Hive must not abort the scan, health
  pass, or swarm review. Record the failure in the domain struct and continue.
- **Read-only by default.** Diagnostics, inspection, and Workshop operations
  never mutate state. `services.ControlSystemdService` requires
  `confirmed == true` and returns `services.ErrConfirmationRequired` otherwise;
  the TUI adds an explicit `[y/N]` prompt.
- **Redact before persistence/export.** Every event field and every report goes
  through `report.Redact`. Do not add a path that writes raw command output to
  disk without it.
- **Timeouts always apply.** `hosts.DefaultTimeout` (30s) is used when the
  caller context has no deadline, and `cmd.WaitDelay` prevents hangs on
  inherited pipes. Per-Hive scan timeout is 20s (`discovery.DefaultHiveTimeout`).

## Package map

| Package | Responsibility |
|---|---|
| `internal/app` | Wiring, backend, CLI (`version`, `diagnose`), config save/rescan |
| `internal/tui` | Bubble Tea model, screens, key handling, views, `Ops` interface |
| `internal/events` | Ring buffer + size-rotated 0600 JSONL event store |
| `internal/report` | Honey Report collection, Markdown rendering, `Export`, `Redact` |
| `internal/swarm` | Bounded-concurrency self-review coordinator (`DefaultConcurrency = 2`) |
| `internal/logs` | Log adapters (journalctl, docker, gateway, agent, events) + ANSI stripping |
| `internal/services` | systemd/Docker/Ollama inspection and guarded control |
| `internal/diagnostics` | 11 read-only Hive Health checks, sorted by severity |
| `internal/discovery` | Concurrent per-Hive scan and selective `Rescan` |
| `internal/openclaw` | CLI adapter, capability probes, JSON/text parsing |
| `internal/hosts` | `Execution` interface, local + SSH transport, process runner |
| `internal/config` | YAML load/migrate/validate/marshal/save and Hive CRUD |
| `internal/agents` | `Bee`, `Inventory`, `HiveStatus`, structured filtering |

## TUI conventions

- Add a new operation by: adding it to `tui.Ops` (`internal/tui/ops.go`),
  implementing it on `app.backend`, defining a `tea.Msg` payload and
  `tea.Cmd` in `app.go`, then handling it in `Update` and rendering it in
  `views.go`. Keep all I/O in the command, never in `Update`/`View`.
- Screens are `screen*` constants; navigation goes through `m.screen` and
  `m.prevScreen`. Key dispatch is centralized in `handleKey`; text-entry modes
  are `inputMode` values handled by `handleTextInput` before `handleKey`.
- The TUI accepts `NO_COLOR` and `TERM=dumb` for plain glyphs, and windows
  large lists to the terminal height. Preserve both when adding views.
- Every async result carries the target Bee key or Hive name. Handlers ignore
  results that no longer match the active selection, and navigation resets
  `busy`. Preserve this when adding new operations.
- Swarm Review streams snapshots through `swarmStream`. Starting a review
  returns a `tea.Batch` of the coordinator run and a channel wait; each
  non-final `swarmProgressMsg` schedules the next wait. Never call
  `coordinator.Run` synchronously from `Update`.
- Tests drive `Model.Update`/`Model.View` directly with `tea.KeyMsg` and
  `tea.WindowSizeMsg`; use `tui.WithScanner` / `tui.WithOps` and the
  `fakeOps` pattern in `app_test.go`.

## Testing conventions

- Standard library `testing` only; no assertion or mock frameworks.
- Fake transports are small local structs implementing `hosts.Execution`
  (`scripted`, `fakeExec`, `scriptedExec`) with canned `hosts.Result` or
  `*hosts.Error`. Prefer this over real SSH or external services.
- CLI behavior is tested through `app.RunArgs(args, &buf)` with
  `t.Setenv(config.EnvConfigPath, tempPath)`; avoid `os.Exit` and globals.
- Tests call real local binaries in a few places (`echo`, `sh`, `sleep`,
  `true`); the SSH test skips when `ssh` is absent. Keep new tests hermetic.
- `go test -race ./...` must pass. Tests are fast except `internal/hosts`.

## Formatting note

`gofmt -l` is already non-clean for `internal/report/report.go` and
`internal/swarm/swarm.go` (struct-tag alignment only). Format your own edits
and leave unrelated noise out of diffs.

## Configuration details and gotchas

- Config default path: `$BEECHIEF_CONFIG` or
  `<user config dir>/beechief/hosts.yaml`. Missing file means zero Hives, not
  an error. Present-but-invalid is an error.
- Schema is version `1`. `config.Migrate` upgrades legacy v0 (`hosts:` key,
  `hostname:`/`username:` aliases). Unknown keys are rejected via
  `KnownFields(true)`, so typos and stray `password:` fail loudly. Never add
  secrets to config.
- YAML tags are camelCase: `controlMaster`, `controlPersist`. The strict
  decoder rejects snake_case aliases, so follow `configs/hosts.example.yaml`
  and the struct tags.
- Event log env var is `BEECHIEF_EVENTS_LOG` (`events.EnvEventsPath`). Default:
  `<user config dir>/beechief/events.jsonl`, rotated at 2 MiB to `.1`, mode 0600.
- Version is injected at build time into `report.DefaultVersion` via
  `-ldflags -X github.com/meistro57/beechief/internal/report.DefaultVersion=...`.
  Do not hard-code a release version elsewhere.
- `internal/logs` caps every fetch at `MaxTail = 500` and strips ANSI/control
  sequences; `SourceEvents` is served from the in-memory event store, not a Hive.
- SSH uses the operator's real `~/.ssh/config`, agent, and keys. Hosts and users
  starting with `-` are rejected so they cannot become ssh options. The argv
  includes `--` before the host. Remote exit 255 is classified as unreachable.
- Build artifacts are ignored: `/beechief`, `/beechief.exe`, `/dist/`,
  `/honey-report*.md`.

## When adding a feature

1. Model it in `agents`, `diagnostics`, `services`, `logs`, or a new domain
   package, using plain structs and JSON tags where exported.
2. Put every CLI invocation in `internal/openclaw` (or the owning adapter) and
   parse it in `parse.go` for OpenClaw output.
3. Route it through `hosts.Execution`; never bypass the transport.
4. Keep it read-only unless it is explicitly guarded by a confirmation.
5. Add the `tui.Ops` method, backend method, async message, key handler, and
   view.
6. Record a redacted event for user-visible operational actions.
7. Add a Hermetic test with a fake `hosts.Execution`, then run
   `make all`.
