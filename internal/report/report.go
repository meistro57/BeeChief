package report

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
)

// DefaultVersion is the BeeChief version reported when not overridden at build time
// via -ldflags "-X github.com/meistro57/beechief/internal/report.DefaultVersion=...".
var DefaultVersion = "v0.5.0"

// BeeDiagnostic summarizes one Bee's Comb (skills) and Workshop state for a Honey Report.
type BeeDiagnostic struct {
	Bee            agents.Bee                 `json:"bee"`
	SkillSummary   *openclaw.SkillCheckSummary `json:"skillSummary,omitempty"`
	MissingSkills  []string                   `json:"missingSkills,omitempty"`
	WorkshopSkills []string                   `json:"workshopSkills,omitempty"`
	ReviewSummary  string                     `json:"reviewSummary,omitempty"`
}

// HiveDiagnostic bundles structured diagnostics for one Hive in a Honey Report.
type HiveDiagnostic struct {
	Hive            config.Hive                   `json:"hive"`
	Online          bool                          `json:"online"`
	ScanError       string                        `json:"scanError,omitempty"`
	OpenClawVersion string                        `json:"openClawVersion,omitempty"`
	Health          *diagnostics.HiveHealthReport `json:"health,omitempty"`
	Services        *services.HiveServicesReport  `json:"services,omitempty"`
	RecentErrors    []string                      `json:"recentErrors,omitempty"`
}

// HoneyReport is BeeChief's portable diagnostic snapshot.
type HoneyReport struct {
	BeeChiefVersion string           `json:"beechiefVersion"`
	GeneratedAt     time.Time        `json:"generatedAt"`
	Hives           []HiveDiagnostic `json:"hives"`
	Bees            []BeeDiagnostic  `json:"bees"`
	Unavailable     []string         `json:"unavailable"`
}

// Collect runs a read-only diagnostic pass across configured Hives and discovered
// Bees to populate a HoneyReport. Partial or offline Hives are recorded without
// aborting the report.
func Collect(ctx context.Context, cfg config.Config, inv *agents.Inventory, execByHive map[string]hosts.Execution) *HoneyReport {
	now := time.Now()
	hr := &HoneyReport{
		BeeChiefVersion: DefaultVersion,
		GeneratedAt:     now,
	}
	if inv == nil {
		inv = agents.NewInventory()
	}

	hiveConfigs := make(map[string]config.Hive, len(cfg.Hives))
	for _, h := range cfg.Hives {
		hiveConfigs[h.Name] = h
	}
	hiveNames := append([]string(nil), inv.Hives...)
	if len(hiveNames) == 0 {
		for _, h := range cfg.Hives {
			hiveNames = append(hiveNames, h.Name)
		}
	}

	for _, name := range hiveNames {
		hc, ok := hiveConfigs[name]
		if !ok {
			hc = config.Hive{Name: name}
		}
		exec := execByHive[name]
		st := inv.Status[name]
		hd := HiveDiagnostic{
			Hive:   hc,
			Online: st.OK() && exec != nil,
		}
		if st.Err != nil {
			hd.ScanError = st.Err.Error()
		}
		if exec == nil {
			hd.Online = false
			if hd.ScanError == "" {
				hd.ScanError = "no execution transport configured"
			}
			hr.Unavailable = append(hr.Unavailable, fmt.Sprintf("%s: Hive unreachable / no transport (%s)", name, hd.ScanError))
			hr.Hives = append(hr.Hives, hd)
			continue
		}

		// Run Hive Health pass.
		hd.Health = diagnostics.Run(ctx, name, exec)
		for _, c := range hd.Health.Checks {
			if c.Name == "Hive reachability" && c.State == diagnostics.StatePass {
				hd.Online = true
			}
			if c.Name == "OpenClaw CLI" && c.State == diagnostics.StatePass {
				hd.OpenClawVersion = c.Summary
			}
			if c.State == diagnostics.StateUnavailable {
				hr.Unavailable = append(hr.Unavailable, fmt.Sprintf("%s: %s (%s)", name, c.Name, c.Summary))
			}
		}

		if !hd.Online {
			hr.Unavailable = append(hr.Unavailable, fmt.Sprintf("%s: service, skill, and log inspection skipped (Hive offline)", name))
			hr.Hives = append(hr.Hives, hd)
			continue
		}

		// Collect systemd, Docker, and Ollama/model troubleshooting.
		hd.Services = services.InspectAll(ctx, name, exec)
		if !hd.Services.Systemd.Available {
			hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: systemd inspection (%s)", name, firstOr(hd.Services.Systemd.Err, "unavailable")))
		}
		if !hd.Services.Docker.Available {
			hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: Docker inspection (%s)", name, firstOr(hd.Services.Docker.Err, "unavailable")))
		}
		if !hd.Services.Models.OllamaAvailable {
			hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: Ollama inspection (not installed)", name))
		}

		// Collect recent errors/warnings from journalctl or gateway logs.
		if logRes, err := logs.AdapterFor(logs.SourceJournalctl).Fetch(ctx, exec, "", 40); err == nil && logRes != nil {
			filtered := (logs.Filter{WarningsOnly: true}).Apply(logRes.Entries)
			for _, e := range filtered {
				hd.RecentErrors = append(hd.RecentErrors, e.Line)
			}
		} else {
			hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: journalctl recent warnings/errors", name))
		}

		hr.Hives = append(hr.Hives, hd)

		// Collect per-Bee skill check and Workshop summary.
		oc := openclaw.NewClient(exec)
		for _, bee := range inv.ByHive[name] {
			bd := BeeDiagnostic{Bee: bee}
			if chk, err := oc.CheckSkills(ctx, bee.ID); err == nil && chk != nil {
				sum := chk.Summary
				bd.SkillSummary = &sum
				for _, m := range chk.MissingRequirements {
					bd.MissingSkills = append(bd.MissingSkills, fmt.Sprintf("%s (%s)", m.Name, m.Missing.Summary()))
				}
			} else {
				hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: skill check", bee.Key()))
			}
			if ws, err := oc.ListWorkshop(ctx, bee.ID); err == nil && ws != nil {
				for _, p := range ws.Proposals {
					bd.WorkshopSkills = append(bd.WorkshopSkills, p.Name)
				}
			} else {
				hr.Unavailable = appendUnique(hr.Unavailable, fmt.Sprintf("%s: Workshop proposals", bee.Key()))
			}
			hr.Bees = append(hr.Bees, bd)
		}
	}

	// Include any Bees from offline/unscanned Hives still in inventory.
	seenBees := make(map[string]bool, len(hr.Bees))
	for _, b := range hr.Bees {
		seenBees[b.Bee.Key()] = true
	}
	for _, bee := range inv.AllBees() {
		if !seenBees[bee.Key()] {
			hr.Bees = append(hr.Bees, BeeDiagnostic{Bee: bee})
		}
	}

	return hr
}

// RenderMarkdown formats r as a portable, secret-redacted Markdown document.
func RenderMarkdown(r *HoneyReport) string {
	if r == nil {
		return "# BeeChief Honey Report\n\nNo diagnostic data available.\n"
	}
	var b strings.Builder
	ver := firstOr(r.BeeChiefVersion, DefaultVersion)
	ts := r.GeneratedAt.UTC().Format(time.RFC3339)
	if r.GeneratedAt.IsZero() {
		ts = time.Now().UTC().Format(time.RFC3339)
	}

	b.WriteString("# BeeChief Honey Report 🍯\n\n")
	fmt.Fprintf(&b, "- **BeeChief Version**: `%s`\n", ver)
	fmt.Fprintf(&b, "- **Generated At**: `%s`\n", ts)
	fmt.Fprintf(&b, "- **Hives Configured**: %d\n", len(r.Hives))
	fmt.Fprintf(&b, "- **Bees Discovered**: %d\n\n", len(r.Bees))

	// 1. Hive list and connectivity
	b.WriteString("## Hives & Connectivity\n\n")
	if len(r.Hives) == 0 {
		b.WriteString("_No Hives configured._\n\n")
	} else {
		b.WriteString("| Hive | Type | Target | State | OpenClaw Version | Notes |\n")
		b.WriteString("|---|---|---|---|---|---|\n")
		for _, h := range r.Hives {
			state := "online"
			if !h.Online {
				state = "offline"
			}
			target := string(h.Hive.Kind())
			if h.Hive.Kind() == config.TypeSSH {
				target = h.Hive.Host
				if h.Hive.User != "" {
					target = h.Hive.User + "@" + target
				}
			}
			fmt.Fprintf(&b, "| `%s` | `%s` | `%s` | **%s** | `%s` | %s |\n",
				h.Hive.Name,
				h.Hive.Kind(),
				target,
				state,
				firstOr(h.OpenClawVersion, "n/a"),
				firstOr(h.ScanError, "ok"),
			)
		}
		b.WriteString("\n")
	}

	// 2. Bee Inventory, Skill Health, and Workshop Summary
	b.WriteString("## Bee Inventory & Comb / Workshop Summary\n\n")
	if len(r.Bees) == 0 {
		b.WriteString("_No Bees discovered._\n\n")
	} else {
		for _, bd := range r.Bees {
			bee := bd.Bee
			fmt.Fprintf(&b, "### Bee `%s` (`%s`)\n\n", bee.DisplayName(), bee.Key())
			fmt.Fprintf(&b, "- **Hive**: `%s`\n", bee.Hive)
			fmt.Fprintf(&b, "- **Model / Runtime**: `%s`\n", firstOr(bee.Runtime, "unspecified"))
			fmt.Fprintf(&b, "- **Workspace**: `%s`\n", firstOr(bee.Workspace, "unspecified"))
			fmt.Fprintf(&b, "- **Default Agent**: `%v`\n", bee.Default)
			if bd.SkillSummary != nil {
				s := bd.SkillSummary
				fmt.Fprintf(&b, "- **Comb (Skills)**: %d total, %d ready, %d disabled, %d missing requirements\n",
					s.Total, s.Eligible, s.Disabled, s.MissingRequirements)
			}
			if len(bd.MissingSkills) > 0 {
				fmt.Fprintf(&b, "- **Missing Skill Requirements**: %s\n", strings.Join(bd.MissingSkills, "; "))
			}
			if len(bd.WorkshopSkills) > 0 {
				fmt.Fprintf(&b, "- **Workshop Skills / Proposals**: %s\n", strings.Join(bd.WorkshopSkills, ", "))
			} else {
				b.WriteString("- **Workshop Skills / Proposals**: none\n")
			}
			if bd.ReviewSummary != "" {
				fmt.Fprintf(&b, "- **Self-Review Summary**: %s\n", bd.ReviewSummary)
			}
			b.WriteString("\n")
		}
	}

	// 3. Per-Hive Health, Disk/RAM/Swap, Network/Port, Services, Docker, Models, Recent Errors
	b.WriteString("## Hive Health & Subsystem Diagnostics\n\n")
	for _, h := range r.Hives {
		fmt.Fprintf(&b, "### Hive `%s`\n\n", h.Hive.Name)
		if h.Health != nil && len(h.Health.Checks) > 0 {
			b.WriteString("#### Quick Health Checks\n\n")
			for _, c := range h.Health.Checks {
				fmt.Fprintf(&b, "- **[%s] %s**: %s\n", strings.ToUpper(string(c.State)), c.Name, c.Summary)
				if c.Detail != "" && c.State != diagnostics.StatePass {
					fmt.Fprintf(&b, "  - Detail: `%s`\n", singleLine(c.Detail))
				}
			}
			b.WriteString("\n")
		}

		if h.Services != nil {
			caps := h.Services.Capabilities
			if caps.OS != "" || caps.OpenClaw.Installed {
				b.WriteString("#### Detected Host & OpenClaw Capabilities\n\n")
				if caps.OS != "" {
					fmt.Fprintf(&b, "- **Platform**: `%s` (`%s`, kernel `%s`)\n", caps.OS, firstOr(caps.Arch, "unknown"), firstOr(caps.Kernel, "unknown"))
				}
				fmt.Fprintf(&b, "- **Subsystems**: systemd=`%v` (user=`%v`), journalctl=`%v`, docker=`%v` (compose=`%v`), ollama=`%v`\n",
					caps.SystemdAvailable, caps.SystemdUserAvailable, caps.JournalctlAvailable,
					caps.DockerAvailable, caps.DockerComposeAvailable, caps.OllamaAvailable)
				if caps.OpenClaw.Installed {
					oc := caps.OpenClaw
					fmt.Fprintf(&b, "- **OpenClaw CLI Features**: skills=`%v`, check=`%v`, workshop=`%v`, models=`%v`, health=`%v`, logs=`%v`\n",
						oc.SupportsSkills, oc.SupportsSkillCheck, oc.SupportsWorkshop,
						oc.SupportsModelsStatus, oc.SupportsGatewayHealth, oc.SupportsLogs)
				}
				b.WriteString("\n")
			}

			// Model / Provider Summary
			ms := h.Services.Models
			b.WriteString("#### Model & Provider Summary\n\n")
			fmt.Fprintf(&b, "- **Configured Default Model**: `%s`\n", firstOr(ms.ConfiguredDefault, "none"))
			fmt.Fprintf(&b, "- **Ollama Available / Reachable**: `%v` / `%v` (%d local models)\n",
				ms.OllamaAvailable, ms.OllamaReachable, len(ms.OllamaModels))
			if len(ms.Mismatches) > 0 {
				for _, m := range ms.Mismatches {
					fmt.Fprintf(&b, "- **Mismatch / Warning**: %s\n", m)
				}
			}
			b.WriteString("\n")

			// systemd Service States
			sd := h.Services.Systemd
			b.WriteString("#### systemd Service States\n\n")
			if !sd.Available {
				fmt.Fprintf(&b, "_Unavailable: %s_\n\n", firstOr(sd.Err, "not present"))
			} else {
				fmt.Fprintf(&b, "- **System State**: `%s`\n", sd.SystemState)
				for _, u := range sd.Units {
					fmt.Fprintf(&b, "- `%s`: `%s` (`%s`) — %s\n", u.Name, u.ActiveState, u.SubState, u.Description)
				}
				b.WriteString("\n")
			}

			// Docker Container States
			dk := h.Services.Docker
			b.WriteString("#### Docker Container States\n\n")
			if !dk.Available {
				fmt.Fprintf(&b, "_Unavailable: %s_\n\n", firstOr(dk.Err, "not present"))
			} else if len(dk.Containers) == 0 {
				b.WriteString("_Docker available; 0 containers._\n\n")
			} else {
				for _, c := range dk.Containers {
					fmt.Fprintf(&b, "- `%s` (`%s`): **%s** (%s) ports=`%s` cpu=`%s` mem=`%s`\n",
						c.Name, c.Image, c.State, c.Status, firstOr(c.Ports, "none"), firstOr(c.CPU, "-"), firstOr(c.Mem, "-"))
				}
				b.WriteString("\n")
			}
		}

		// Recent Errors / Warnings
		b.WriteString("#### Recent Errors & Warnings\n\n")
		if len(h.RecentErrors) == 0 {
			b.WriteString("_No recent errors or warnings captured._\n\n")
		} else {
			limit := len(h.RecentErrors)
			if limit > 15 {
				limit = 15
			}
			for _, line := range h.RecentErrors[:limit] {
				fmt.Fprintf(&b, "- `%s`\n", singleLine(line))
			}
			b.WriteString("\n")
		}
	}

	// 4. Clear section listing checks that were unavailable
	b.WriteString("## Unavailable Checks\n\n")
	if len(r.Unavailable) == 0 {
		b.WriteString("_All diagnostic checks were available._\n")
	} else {
		for _, item := range r.Unavailable {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}

	return Redact(b.String())
}

// Export writes the rendered, redacted Honey Report to path (or a timestamped
// file in the current directory when path is empty) and returns the file path.
func Export(r *HoneyReport, path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		ts := time.Now().UTC().Format("20060102-150405")
		if r != nil && !r.GeneratedAt.IsZero() {
			ts = r.GeneratedAt.UTC().Format("20060102-150405")
		}
		path = fmt.Sprintf("honey-report-%s.md", ts)
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		path = abs
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", fmt.Errorf("create report directory: %w", err)
		}
	}
	content := RenderMarkdown(r)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("write honey report: %w", err)
	}
	return path, nil
}

func appendUnique(slice []string, val string) []string {
	for _, existing := range slice {
		if existing == val {
			return slice
		}
	}
	return append(slice, val)
}

func singleLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", " | ")
	return s
}

func firstOr(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
