// Package diagnostics implements BeeChief's read-only Hive Health quick
// diagnostic pass (issue #7).
//
// Every check executes through hosts.Execution so local and SSH Hives behave
// identically. Missing optional subsystems (Docker, systemd, swap) are
// reported as StateUnavailable, and a failing check never aborts the rest of
// the diagnostic run.
package diagnostics

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/openclaw"
)

// CheckState classifies the outcome of a single diagnostic check.
type CheckState string

const (
	StatePass        CheckState = "pass"
	StateWarning     CheckState = "warning"
	StateFail        CheckState = "fail"
	StateUnavailable CheckState = "unavailable"
)

// SeverityRank orders CheckStates from most urgent (0 = fail) to least (3 = pass).
func (s CheckState) SeverityRank() int {
	switch s {
	case StateFail:
		return 0
	case StateWarning:
		return 1
	case StateUnavailable:
		return 2
	case StatePass:
		return 3
	default:
		return 4
	}
}

// CheckResult is the structured result of one Hive Health check.
type CheckResult struct {
	Name        string        `json:"name"`
	State       CheckState    `json:"state"`
	Summary     string        `json:"summary"`
	Detail      string        `json:"detail,omitempty"`
	Remediation []string      `json:"remediation,omitempty"`
	Duration    time.Duration `json:"duration"`
}

// CheckFunc executes a single read-only diagnostic check against a Hive.
type CheckFunc func(ctx context.Context, exec hosts.Execution) CheckResult

// HiveHealthReport aggregates all checks run against a Hive.
type HiveHealthReport struct {
	Hive      string        `json:"hive"`
	CheckedAt time.Time     `json:"checkedAt"`
	Duration  time.Duration `json:"duration"`
	Checks    []CheckResult `json:"checks"`
}

// Counts tallies checks by state.
func (r *HiveHealthReport) Counts() (pass, warn, fail, unavail int) {
	if r == nil {
		return 0, 0, 0, 0
	}
	for _, c := range r.Checks {
		switch c.State {
		case StatePass:
			pass++
		case StateWarning:
			warn++
		case StateFail:
			fail++
		case StateUnavailable:
			unavail++
		}
	}
	return
}

// SortedBySeverity returns a copy of Checks ordered fail -> warning -> unavailable -> pass.
func (r *HiveHealthReport) SortedBySeverity() []CheckResult {
	if r == nil {
		return nil
	}
	out := append([]CheckResult(nil), r.Checks...)
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].State.SeverityRank() < out[j].State.SeverityRank()
	})
	return out
}

// DefaultChecks returns the standard 11 read-only Hive Health checks.
func DefaultChecks() []CheckFunc {
	return []CheckFunc{
		CheckReachability,
		CheckOpenClawVersion,
		CheckGatewayHealth,
		CheckAgentDiscovery,
		CheckDiskFree,
		CheckRAMUsage,
		CheckSwapUsage,
		CheckModelProviders,
		CheckDockerAvailability,
		CheckSystemdAvailability,
		CheckRecentFailures,
	}
}

// Run executes checks sequentially with panic/error isolation so one broken
// check never aborts the rest of the pass. When checks is empty, DefaultChecks
// is used. If Hive reachability fails, subsequent checks still record clean
// unreachable/unavailable states without hanging.
func Run(ctx context.Context, hive string, exec hosts.Execution, checks ...CheckFunc) *HiveHealthReport {
	if len(checks) == 0 {
		checks = DefaultChecks()
	}
	start := time.Now()
	rep := &HiveHealthReport{
		Hive:      hive,
		CheckedAt: start,
		Checks:    make([]CheckResult, 0, len(checks)),
	}
	if exec == nil {
		rep.Checks = append(rep.Checks, CheckResult{
			Name:        "Hive reachability",
			State:       StateFail,
			Summary:     "no execution configured for Hive",
			Detail:      "Hive configuration could not construct an execution transport.",
			Remediation: []string{"Verify Hive settings in hosts.yaml or the Hive configuration wizard."},
		})
		rep.Duration = time.Since(start)
		return rep
	}

	probeExec := exec
	for _, fn := range checks {
		cStart := time.Now()
		res := safeRunCheck(ctx, probeExec, fn)
		if res.Duration == 0 {
			res.Duration = time.Since(cStart)
		}
		rep.Checks = append(rep.Checks, res)
		if res.Name == "Hive reachability" && res.State == StateFail {
			probeExec = unreachableExecution{hive: hive}
		}
	}
	rep.Duration = time.Since(start)
	return rep
}

type unreachableExecution struct {
	hive string
}

func (u unreachableExecution) Run(context.Context, string, ...string) (hosts.Result, error) {
	return hosts.Result{ExitCode: -1}, &hosts.Error{
		Hive:    u.hive,
		Kind:    hosts.KindUnreachable,
		Command: "hive reachability probe",
	}
}

func safeRunCheck(ctx context.Context, exec hosts.Execution, fn CheckFunc) (res CheckResult) {
	defer func() {
		if r := recover(); r != nil {
			res = CheckResult{
				Name:    "Diagnostic check",
				State:   StateFail,
				Summary: "check panicked",
				Detail:  fmt.Sprintf("%v", r),
			}
		}
	}()
	return fn(ctx, exec)
}

// CheckReachability probes basic command execution on the Hive.
func CheckReachability(ctx context.Context, exec hosts.Execution) CheckResult {
	pr := hosts.Probe(ctx, exec)
	if !pr.Reachable {
		return CheckResult{
			Name:    "Hive reachability",
			State:   StateFail,
			Summary: "Hive is unreachable",
			Detail:  fmt.Sprintf("%v", pr.Err),
			Remediation: []string{
				"Verify hostname, SSH keys/agent, and network connectivity.",
				"Run `ssh -o BatchMode=yes <host> true` to test outside BeeChief.",
			},
			Duration: pr.Latency,
		}
	}
	return CheckResult{
		Name:     "Hive reachability",
		State:    StatePass,
		Summary:  fmt.Sprintf("reachable (%s)", pr.Latency.Round(time.Millisecond)),
		Duration: pr.Latency,
	}
}

// CheckOpenClawVersion verifies that the OpenClaw CLI is installed and reports its version.
func CheckOpenClawVersion(ctx context.Context, exec hosts.Execution) CheckResult {
	ver, err := openclaw.NewClient(exec).Version(ctx)
	if err != nil {
		state := StateFail
		rem := []string{"Ensure `openclaw` is installed and on the non-interactive PATH."}
		if hosts.IsKind(err, hosts.KindUnreachable) {
			return CheckResult{Name: "OpenClaw CLI", State: StateUnavailable, Summary: "Hive unreachable", Detail: err.Error()}
		}
		return CheckResult{
			Name:        "OpenClaw CLI",
			State:       state,
			Summary:     "OpenClaw CLI not available",
			Detail:      err.Error(),
			Remediation: rem,
		}
	}
	return CheckResult{
		Name:    "OpenClaw CLI",
		State:   StatePass,
		Summary: ver,
	}
}

// CheckGatewayHealth checks whether the OpenClaw Gateway responds.
func CheckGatewayHealth(ctx context.Context, exec hosts.Execution) CheckResult {
	out, err := openclaw.NewClient(exec).GatewayHealth(ctx)
	if err != nil {
		if errors.Is(err, openclaw.ErrNotInstalled) || errors.Is(err, openclaw.ErrUnsupported) || hosts.IsKind(err, hosts.KindUnreachable) {
			return CheckResult{
				Name:    "OpenClaw Gateway",
				State:   StateUnavailable,
				Summary: "Gateway health check unavailable",
				Detail:  err.Error(),
			}
		}
		return CheckResult{
			Name:    "OpenClaw Gateway",
			State:   StateWarning,
			Summary: "Gateway did not report healthy",
			Detail:  err.Error(),
			Remediation: []string{
				"Check whether the Gateway service is running (`openclaw status` or `openclaw gateway run`).",
			},
		}
	}
	return CheckResult{
		Name:    "OpenClaw Gateway",
		State:   StatePass,
		Summary: "Gateway responding",
		Detail:  out,
	}
}

// CheckAgentDiscovery runs `openclaw agents list --json` and summarizes discovered Bees.
func CheckAgentDiscovery(ctx context.Context, exec hosts.Execution) CheckResult {
	bees, err := openclaw.NewClient(exec).DiscoverAgents(ctx)
	if err != nil {
		if hosts.IsKind(err, hosts.KindUnreachable) {
			return CheckResult{Name: "Agent discovery", State: StateUnavailable, Summary: "Hive unreachable", Detail: err.Error()}
		}
		return CheckResult{
			Name:        "Agent discovery",
			State:       StateFail,
			Summary:     "agent discovery failed",
			Detail:      err.Error(),
			Remediation: []string{"Run `openclaw agents list --json` on the Hive to inspect CLI output."},
		}
	}
	if len(bees) == 0 {
		return CheckResult{
			Name:        "Agent discovery",
			State:       StateWarning,
			Summary:     "0 agents discovered",
			Detail:      "OpenClaw returned an empty agent list.",
			Remediation: []string{"Run `openclaw onboard` or `openclaw setup` to configure an agent."},
		}
	}
	names := make([]string, 0, len(bees))
	for _, b := range bees {
		names = append(names, b.ID)
	}
	return CheckResult{
		Name:    "Agent discovery",
		State:   StatePass,
		Summary: fmt.Sprintf("%d agent(s) discovered", len(bees)),
		Detail:  "Agents: " + strings.Join(names, ", "),
	}
}

// CheckDiskFree inspects root filesystem usage with `df -P /`.
func CheckDiskFree(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "df", "-P", "/")
	if err != nil || !res.Ok() {
		return CheckResult{
			Name:    "Disk free space",
			State:   StateUnavailable,
			Summary: "df command unavailable",
			Detail:  errDetail(res, err),
		}
	}
	pct, availKB, ok := parseDFOutput(res.Stdout)
	if !ok {
		return CheckResult{
			Name:    "Disk free space",
			State:   StateUnavailable,
			Summary: "could not parse df output",
			Detail:  strings.TrimSpace(res.Stdout),
		}
	}
	availGB := float64(availKB) / (1024 * 1024)
	summary := fmt.Sprintf("%d%% used (%.1f GiB free)", pct, availGB)
	switch {
	case pct >= 95:
		return CheckResult{
			Name:        "Disk free space",
			State:       StateFail,
			Summary:     summary,
			Detail:      strings.TrimSpace(res.Stdout),
			Remediation: []string{"Free disk space on `/`; logs, Docker images, or old backups may be full."},
		}
	case pct >= 85:
		return CheckResult{
			Name:        "Disk free space",
			State:       StateWarning,
			Summary:     summary,
			Detail:      strings.TrimSpace(res.Stdout),
			Remediation: []string{"Monitor disk usage on `/`; usage is above 85%."},
		}
	default:
		return CheckResult{
			Name:    "Disk free space",
			State:   StatePass,
			Summary: summary,
			Detail:  strings.TrimSpace(res.Stdout),
		}
	}
}

// CheckRAMUsage inspects memory usage via `free -m`.
func CheckRAMUsage(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "free", "-m")
	if err != nil || !res.Ok() {
		return CheckResult{
			Name:    "RAM usage",
			State:   StateUnavailable,
			Summary: "free command unavailable",
			Detail:  errDetail(res, err),
		}
	}
	totalMB, usedMB, ok := parseFreeLine(res.Stdout, "Mem:")
	if !ok || totalMB <= 0 {
		return CheckResult{
			Name:    "RAM usage",
			State:   StateUnavailable,
			Summary: "could not parse memory stats",
			Detail:  strings.TrimSpace(res.Stdout),
		}
	}
	pct := (usedMB * 100) / totalMB
	summary := fmt.Sprintf("%d%% used (%d / %d MiB)", pct, usedMB, totalMB)
	switch {
	case pct >= 95:
		return CheckResult{
			Name:        "RAM usage",
			State:       StateFail,
			Summary:     summary,
			Detail:      strings.TrimSpace(res.Stdout),
			Remediation: []string{"Memory pressure is critical; inspect running containers/models."},
		}
	case pct >= 85:
		return CheckResult{
			Name:        "RAM usage",
			State:       StateWarning,
			Summary:     summary,
			Detail:      strings.TrimSpace(res.Stdout),
			Remediation: []string{"Memory usage is elevated (>85%)."},
		}
	default:
		return CheckResult{
			Name:    "RAM usage",
			State:   StatePass,
			Summary: summary,
		}
	}
}

// CheckSwapUsage inspects swap usage via `free -m`.
func CheckSwapUsage(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "free", "-m")
	if err != nil || !res.Ok() {
		return CheckResult{
			Name:    "Swap usage",
			State:   StateUnavailable,
			Summary: "swap stats unavailable",
			Detail:  errDetail(res, err),
		}
	}
	totalMB, usedMB, ok := parseFreeLine(res.Stdout, "Swap:")
	if !ok {
		return CheckResult{
			Name:    "Swap usage",
			State:   StateUnavailable,
			Summary: "no Swap line in free output",
		}
	}
	if totalMB == 0 {
		return CheckResult{
			Name:    "Swap usage",
			State:   StatePass,
			Summary: "no swap configured (0 MiB)",
		}
	}
	pct := (usedMB * 100) / totalMB
	summary := fmt.Sprintf("%d%% used (%d / %d MiB)", pct, usedMB, totalMB)
	if pct >= 80 {
		return CheckResult{
			Name:        "Swap usage",
			State:       StateWarning,
			Summary:     summary,
			Remediation: []string{"High swap usage may slow down local model inference."},
		}
	}
	return CheckResult{
		Name:    "Swap usage",
		State:   StatePass,
		Summary: summary,
	}
}

// CheckModelProviders inspects OpenClaw's configured model provider status.
func CheckModelProviders(ctx context.Context, exec hosts.Execution) CheckResult {
	ms, err := openclaw.NewClient(exec).ModelsStatus(ctx, "")
	if err != nil {
		if errors.Is(err, openclaw.ErrNotInstalled) || errors.Is(err, openclaw.ErrUnsupported) || hosts.IsKind(err, hosts.KindUnreachable) {
			return CheckResult{
				Name:    "Model providers",
				State:   StateUnavailable,
				Summary: "model provider status unavailable",
				Detail:  err.Error(),
			}
		}
		return CheckResult{
			Name:        "Model providers",
			State:       StateWarning,
			Summary:     "could not query model status",
			Detail:      err.Error(),
			Remediation: []string{"Run `openclaw models status` on the Hive."},
		}
	}
	if len(ms.MissingProvidersInUse) > 0 || len(ms.ModelRouteIssues) > 0 {
		var details []string
		if len(ms.MissingProvidersInUse) > 0 {
			details = append(details, "Missing providers: "+strings.Join(ms.MissingProvidersInUse, ", "))
		}
		if len(ms.ModelRouteIssues) > 0 {
			details = append(details, "Route issues: "+strings.Join(ms.ModelRouteIssues, "; "))
		}
		return CheckResult{
			Name:        "Model providers",
			State:       StateWarning,
			Summary:     fmt.Sprintf("default %s (%d issue(s))", firstOr(ms.DefaultModel, "none"), len(ms.MissingProvidersInUse)+len(ms.ModelRouteIssues)),
			Detail:      strings.Join(details, "\n"),
			Remediation: []string{"Run `openclaw models status` or `openclaw configure` to verify provider credentials."},
		}
	}
	return CheckResult{
		Name:    "Model providers",
		State:   StatePass,
		Summary: fmt.Sprintf("default: %s (%d provider(s))", firstOr(ms.DefaultModel, "configured"), len(ms.Providers)),
	}
}

// CheckDockerAvailability checks whether Docker is installed and responsive.
func CheckDockerAvailability(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		if hosts.IsKind(err, hosts.KindNotFound) || hosts.IsKind(err, hosts.KindUnreachable) {
			return CheckResult{
				Name:    "Docker availability",
				State:   StateUnavailable,
				Summary: "Docker not installed",
				Detail:  err.Error(),
			}
		}
		return CheckResult{
			Name:    "Docker availability",
			State:   StateUnavailable,
			Summary: "Docker unavailable",
			Detail:  err.Error(),
		}
	}
	if res.ExitCode == 127 {
		return CheckResult{
			Name:    "Docker availability",
			State:   StateUnavailable,
			Summary: "Docker CLI not installed",
		}
	}
	if !res.Ok() {
		return CheckResult{
			Name:        "Docker availability",
			State:       StateWarning,
			Summary:     "Docker installed but daemon unreachable",
			Detail:      errDetail(res, nil),
			Remediation: []string{"Check whether the Docker daemon is running (`systemctl status docker`) and user permissions."},
		}
	}
	return CheckResult{
		Name:    "Docker availability",
		State:   StatePass,
		Summary: "Docker " + strings.TrimSpace(res.Stdout),
	}
}

// CheckSystemdAvailability checks whether systemd is active on the Hive.
func CheckSystemdAvailability(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "systemctl", "is-system-running")
	if err != nil {
		return CheckResult{
			Name:    "systemd availability",
			State:   StateUnavailable,
			Summary: "systemctl not available",
			Detail:  err.Error(),
		}
	}
	if res.ExitCode == 127 {
		return CheckResult{
			Name:    "systemd availability",
			State:   StateUnavailable,
			Summary: "systemd not installed",
		}
	}
	state := strings.TrimSpace(res.Stdout)
	if state == "" {
		state = strings.TrimSpace(res.Stderr)
	}
	switch state {
	case "running":
		return CheckResult{
			Name:    "systemd availability",
			State:   StatePass,
			Summary: "systemd running",
		}
	case "degraded":
		return CheckResult{
			Name:        "systemd availability",
			State:       StateWarning,
			Summary:     "systemd degraded (one or more failed units)",
			Detail:      state,
			Remediation: []string{"Inspect failed units in BeeChief Services or run `systemctl --failed`."},
		}
	case "offline":
		return CheckResult{
			Name:    "systemd availability",
			State:   StateUnavailable,
			Summary: "systemd offline (container or non-systemd host)",
		}
	default:
		if res.Ok() {
			return CheckResult{Name: "systemd availability", State: StatePass, Summary: state}
		}
		return CheckResult{
			Name:    "systemd availability",
			State:   StateUnavailable,
			Summary: firstOr(state, "unavailable"),
		}
	}
}

// CheckRecentFailures checks for failed systemd units or recent journal errors.
func CheckRecentFailures(ctx context.Context, exec hosts.Execution) CheckResult {
	res, err := exec.Run(ctx, "systemctl", "--failed", "--no-legend", "--plain")
	if err != nil || res.ExitCode == 127 {
		return CheckResult{
			Name:    "Recent service failures",
			State:   StateUnavailable,
			Summary: "systemctl failure check unavailable",
		}
	}
	lines := nonEmptyLines(res.Stdout)
	if len(lines) > 0 {
		return CheckResult{
			Name:        "Recent service failures",
			State:       StateWarning,
			Summary:     fmt.Sprintf("%d failed systemd unit(s)", len(lines)),
			Detail:      strings.Join(lines, "\n"),
			Remediation: []string{"Open Services or Logs in BeeChief to inspect the failed units."},
		}
	}
	return CheckResult{
		Name:    "Recent service failures",
		State:   StatePass,
		Summary: "0 failed systemd units",
	}
}

func parseDFOutput(out string) (usedPct int, availKB int64, ok bool) {
	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		return 0, 0, false
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 5 {
		return 0, 0, false
	}
	avail, err1 := strconv.ParseInt(fields[3], 10, 64)
	pctStr := strings.TrimSuffix(fields[4], "%")
	pct, err2 := strconv.Atoi(pctStr)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pct, avail, true
}

func parseFreeLine(out, prefix string) (totalMB, usedMB int, ok bool) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			return 0, 0, false
		}
		t, err1 := strconv.Atoi(fields[1])
		u, err2 := strconv.Atoi(fields[2])
		if err1 != nil || err2 != nil {
			return 0, 0, false
		}
		return t, u, true
	}
	return 0, 0, false
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func errDetail(res hosts.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	if s := strings.TrimSpace(res.Stderr); s != "" {
		return s
	}
	return strings.TrimSpace(res.Stdout)
}

func firstOr(s, fallback string) string {
	if strings.TrimSpace(s) != "" {
		return s
	}
	return fallback
}
