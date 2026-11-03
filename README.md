<p align="center">
  <img src="assets/BeeChief_banner.svg" alt="BeeChief — The Swarm Command Center" width="100%" />
</p>

# BeeChief 🐝

**The Swarm Command Center** — *One terminal. Every hive. Discover, review & diagnose your agents.*

A Go terminal control center for managing, reviewing, diagnosing, and maintaining OpenClaw agents across multiple machines.

BeeChief treats machines as **Hives**, OpenClaw agents as **Bees**, and skills as the **Comb**. Hosts are configured; agents, skills, and capabilities are discovered dynamically.

## Core idea

```text
Hive Scan → discover Bees → inspect Comb → self-review → troubleshoot → Honey Report
```

Nothing agent-specific should be hard-coded.

## Quick start

```bash
go build ./...
mkdir -p ~/.config/beechief
cp configs/hosts.example.yaml ~/.config/beechief/hosts.yaml   # then edit
go run ./cmd/beechief
```

With no config file BeeChief still starts, with zero Hives, and you can add local or SSH Hives interactively from **Settings**. Set `BEECHIEF_CONFIG=/path/to/hosts.yaml` to use a different file.

To generate and export a portable, secret-redacted **Honey Report** from the CLI:

```bash
go run ./cmd/beechief diagnose --export --output honey-report.md
```

Keys: `↑/↓` (or `k/j`) move · `enter` select · `/` filter/search · `r` rescan all Hives · `esc` back/quit · `q` quit.

## Configuring Hives

Only machines are configured. Agents are never listed in config.

```yaml
hives:
  - name: work-pc
    type: local

  - name: qmu
    type: ssh
    host: qmu-server   # hostname, IP, or ~/.ssh/config alias
    user: root         # optional
    # port: 22         # optional
    # label: Basement  # optional
```

| Field   | Required  | Notes |
|---------|-----------|-------|
| `name`  | yes       | Unique Hive identifier shown in the UI. |
| `type`  | no        | `local` or `ssh`. If you leave it out, an empty `host` (or `host: local`) means local; anything else means ssh. |
| `host`  | for ssh   | Passed to the system `ssh` client as-is. |
| `user`  | no        | `ssh -l`. |
| `port`  | no        | `ssh -p`. |
| `label` | no        | Free-text description. |

Unknown keys are rejected, so a typo (or a stray `password:`) fails loudly.

**SSH and credentials.** BeeChief uses your system `ssh` client, so your
`~/.ssh/config`, ssh-agent, keys, `ProxyJump`, and `known_hosts` all apply.
It runs with `BatchMode=yes` and a connect timeout: a host that would ask for
a password or a host-key confirmation fails right away with a clear error
instead of hanging. No passwords or keys go in BeeChief's config or source.

## Agent discovery

On startup, and whenever you press `r`, BeeChief runs this on every Hive
concurrently:

```bash
openclaw agents list --json
```

It records each agent's ID, display name, emoji, model, workspace, agent
directory, and default flag, along with the Hive the agent was found on. Agents
are grouped by Hive, so the same agent ID on two Hives never collides. Each Hive
is scanned on its own with its own timeout. If a Hive fails (unreachable,
timeout, OpenClaw not installed, command failed, or output that is not valid
JSON), it is marked with `✗` and the error, and the other Hives still load.

On SSH Hives, `openclaw` must be on the `PATH` of a *non-interactive* login
shell. If it is installed in a user-local directory, add that directory to
`PATH` in a file that non-interactive shells read (e.g. `~/.bashrc` above any
interactive-only guard, or `~/.ssh/environment` where it is enabled).

## Features & Workflows

### Swarm Overview & Bee Detail (`#4`)
- View total Hive count, total Bee count, per-Hive online/offline state, and last scan time.
- Filter discovered Bees interactively with `/`.
- Select any Bee (`enter`) to inspect its status, visible skills, skill check, self-review, Workshop proposals, curator/learning status, model status, or send a custom instruction.

### Comb / Skills Inspection (`#5`)
- Wraps `openclaw skills list --agent <id> --json` and `openclaw skills check --agent <id> --json`.
- Highlights ready/loadable skills vs. broken or missing-requirement skills (`bins`, `anyBins`, `env`, `config`, `os`).
- Surfaces unsupported OpenClaw CLI versions clearly and allows toggling raw CLI output (`v`) for troubleshooting.

### Self-Review, Workshop Proposals & Swarm Review (`#6`, `#10`)
- Runs a structured 7-point skill self-review on a selected Bee (`s`) and lists resulting Skill Workshop proposals (`w`) and proposal Markdown content (`enter`).
- **Safety by default**: Workshop proposals are strictly read-only in BeeChief and are never silently auto-applied.
- **Swarm Review**: Run self-review across the selected Bee (`1`), all Bees on the selected Hive (`2`), or all Bees across all reachable Hives (`3`) with bounded concurrency, live per-Bee state (`queued` / `running` / `complete` / `failed` / `skipped`), proposal count rollups, and safe cancellation (`x`) that preserves completed results.

### Hive Health & Subsystem Troubleshooting (`#7`, `#8`)
- **Hive Health (`Troubleshooting`)**: Runs 11 read-only checks (reachability, OpenClaw CLI version, Gateway health, agent discovery, disk free space, RAM, swap, model providers, Docker, systemd, and recent service failures) with severity sorting (`s`).
- **Services (`Services`)**: Capability-detected inspection for **systemd** units (status, enabled state, recent logs, plus `S`/`T`/`R` start/stop/restart gated behind an explicit `[y/N]` confirmation prompt), **Docker** containers (state, ports, non-streaming CPU/RAM stats, recent logs), and **Models & Ollama** (detects mismatches between OpenClaw's configured models and reachable providers/local Ollama models).

### Unified Log Browser (`#9`)
- Browse bounded tails across `journalctl`, `docker`, `openclaw` Gateway logs, and `agent` status output on any Hive.
- Filter by errors-only (`e`), warnings+errors (`w`), or text search (`/`).
- Toggle non-blocking follow mode (`f`) and inspect the exact underlying CLI command (`c`). All remote output is stripped of ANSI/control sequences.

### Honey Report Diagnostics (`#11`)
- Export a portable Markdown diagnostic report from the TUI (`e` in Swarm Overview or Troubleshooting) or from the CLI:
  ```bash
  beechief diagnose --export --output honey-report.md
  ```
- Includes BeeChief & OpenClaw versions, Hive connectivity, Bee inventory, Comb & Workshop summaries, Hive Health checks, systemd/Docker/model states, recent warnings/errors, and a dedicated **Unavailable Checks** section.
- Automatically scrubs secrets, bearer tokens, API keys (`sk-...`, `ghp_...`, `xoxb-...`, `AIza...`), passwords, URL credentials, and PEM private keys.

### Interactive Hive Configuration Wizard (`#12`)
- Open **Settings** to add a local Hive (`l`) or SSH Hive (`a`), edit an existing Hive (`e`/`enter`), or remove a Hive with confirmation (`d`).
- Test SSH reachability and OpenClaw CLI presence (`t`) before saving, and automatically trigger a fleet rescan on save (`s`).

## Development

```bash
go build ./...
go test ./...
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the package layout and design choices.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the build plan and linked agent-ready GitHub issues.

## Brand & Art Pack

Visual assets live in [`assets/`](assets/):

| Asset | SVG | PNG | Usage |
|---|---|---|---|
| **Banner** | [`BeeChief_banner.svg`](assets/BeeChief_banner.svg) | [`BeeChief_banner.png`](assets/BeeChief_banner.png) | Repository hero banner & social preview |
| **Logo** | [`BeeChief_logo.svg`](assets/BeeChief_logo.svg) | [`BeeChief_logo.png`](assets/BeeChief_logo.png) | Documentation badge & release branding |
| **Avatar** | [`BeeChief_avatar.svg`](assets/BeeChief_avatar.svg) | [`BeeChief_avatar.png`](assets/BeeChief_avatar.png) | App icon, bot avatar & compact hexagon badge |

## Status

Implemented through v0.4:
- TUI navigation, Swarm Overview, and Bee detail screens (#1, #4)
- Hive configuration, interactive configuration wizard, and local/SSH execution (#2, #12)
- Dynamic OpenClaw agent discovery (#3)
- Comb skill inspection and per-Bee skill checks (#5)
- Bee self-review, Workshop proposal inspection, and multi-Bee Swarm Review (#6, #10)
- Hive Health quick diagnostics and systemd/Docker/Ollama/model troubleshooting (#7, #8)
- Unified log browser with filtering and follow mode (#9)
- Portable, secret-redacted Honey Report export via CLI and TUI (#11)
