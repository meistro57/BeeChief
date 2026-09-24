# BeeChief Roadmap 🐝

BeeChief is a Go terminal operations console for discovering, managing, reviewing, diagnosing, and maintaining OpenClaw agents across multiple machines.

The design rule is simple:

> **Hosts are configured. Agents are discovered. Skills are discovered. Services are inspected. Nothing agent-specific is hard-coded.**

## Product language

| BeeChief | Technical meaning |
|---|---|
| Hive | Host / machine |
| Bee | OpenClaw agent |
| Comb | Skills |
| Hive Scan | Host + agent discovery |
| Bee Check | Agent diagnostics |
| Comb Review | Skill inspection/review |
| Swarm Review | Multi-agent self-review |
| Hive Health | Machine diagnostics |
| Honey Report | Portable diagnostic export |

---

## v0.1 — Build the Hive

Goal: launch BeeChief, connect to one or more machines, discover agents automatically, and navigate the fleet.

- [ ] #1 Bootstrap BeeChief Go TUI with Bubble Tea
- [ ] #2 Add Hive host configuration and local/SSH execution layer
- [ ] #3 Discover OpenClaw agents dynamically on every Hive
- [ ] #4 Build Swarm Overview and Bee detail screens

### Exit criteria

A user can run:

```bash
beechief
```

BeeChief loads configured Hives, reports unreachable machines without failing, discovers OpenClaw agents dynamically, and displays them in a usable TUI.

---

## v0.2 — Comb & Self Review

Goal: inspect what each Bee can actually do and make skill maintenance manageable.

- [ ] #5 Add Comb skill inspection and per-Bee skill checks
- [ ] #6 Add Bee self-review and Workshop proposal management

### Exit criteria

A user can select any discovered Bee, inspect its visible skills, run skill checks, launch a structured self-review, and inspect Workshop proposals without silently applying changes.

---

## v0.3 — Troubleshooting

Goal: turn the archaeology expedition into a menu.

- [ ] #7 Implement Hive Health quick diagnostics
- [ ] #8 Add systemd, Docker, Ollama, and model troubleshooting
- [ ] #9 Build unified log browser with filtering and follow mode

### Exit criteria

BeeChief can perform a read-only health pass, isolate common host/service/model failures, and provide a useful log browser across local and SSH Hives.

---

## v0.4 — Swarm Operations

Goal: operate on the fleet instead of babysitting one Bee at a time.

- [ ] #10 Implement Swarm Review across selected or all Bees
- [ ] #11 Generate portable Honey Report diagnostics
- [ ] #12 Add interactive Hive configuration wizard

### Exit criteria

BeeChief can review multiple Bees, export a useful redacted diagnostic report, and add another Hive without requiring manual YAML edits.

---

## v0.5 — Hardening

Planned work:

- Linux release binaries
- Windows release binaries where OpenClaw workflows support them
- GitHub Actions build/test pipeline
- install/update script
- configuration migration/versioning
- richer capability detection
- graceful compatibility handling across OpenClaw versions
- structured event/log persistence
- improved filtering/search across large swarms
- SSH connection reuse and performance tuning
- accessibility and terminal-size handling

---

## v1.0 — Stable BeeChief

Target characteristics:

- Multi-Hive discovery is reliable.
- Bee inventory is entirely dynamic.
- OpenClaw command integration is isolated behind adapters.
- Missing optional subsystems degrade gracefully.
- Read-only diagnostics are safe by default.
- State-changing actions require explicit confirmation.
- Secret-bearing output is redacted.
- Core workflows are test-covered.
- A fresh user can install BeeChief, add a Hive, scan the swarm, diagnose a Bee, and export a Honey Report without reading the source code.

---

## Architecture principles

### Discover instead of hard-code

BeeChief should ask the environment what exists rather than encode Lewis, Grommet, Hope, Kepler, or any other Bee into the program.

### One execution path

Local and SSH Hives should expose the same execution interface to the rest of BeeChief.

### Separate UI from operations

Bubble Tea views should consume structured state. They should not contain shell-command logic.

### Detect capabilities

Do not assume every Hive has systemd, Docker, Ollama, or identical OpenClaw CLI features. Probe first and report unsupported capabilities cleanly.

### Safe by default

Diagnostics and inspection should be read-only unless the operator deliberately chooses an action that changes state.

### Partial failure is normal

One dead Hive or broken Bee should not take down the rest of the swarm.

### Agent-friendly issues

Issues should contain enough context, constraints, acceptance criteria, and expected deliverables that an autonomous coding agent can work from the issue without access to the original planning conversation.
