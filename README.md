# BeeChief 🐝

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

With no config file BeeChief still starts, with zero Hives. Set
`BEECHIEF_CONFIG=/path/to/hosts.yaml` to use a different file.

Keys: `↑/↓` (or `k/j`) move · `enter` select · `r` rescan all Hives · `q`/`esc` quit.

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

## Development

```bash
go build ./...
go test ./...
```

See [ARCHITECTURE.md](ARCHITECTURE.md) for the package layout and design choices.

## Roadmap

See [ROADMAP.md](ROADMAP.md) for the build plan and linked agent-ready GitHub issues.

## Status

Early development. Implemented so far: the TUI skeleton (#1), Hive configuration with local/SSH execution (#2), and dynamic agent discovery (#3). The rest of the implementation queue is tracked in GitHub Issues.
