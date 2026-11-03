// Package services provides modular capability detection and troubleshooting
// adapters for systemd, Docker, and Ollama/model providers (issue #8).
//
// All inspection is read-only by default. State-changing systemd actions
// (start, stop, restart) require explicit operator confirmation and return
// ErrConfirmationRequired when called without confirmation.
package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/openclaw"
)

// ErrConfirmationRequired is returned when a state-changing action is invoked
// without explicit operator confirmation.
var ErrConfirmationRequired = errors.New("explicit operator confirmation is required for state-changing actions")

// ServiceAction represents a state-changing systemd unit operation.
type ServiceAction string

const (
	ActionStart   ServiceAction = "start"
	ActionStop    ServiceAction = "stop"
	ActionRestart ServiceAction = "restart"
)

// SystemdUnit describes one systemd service unit on a Hive.
type SystemdUnit struct {
	Name         string `json:"name"`
	LoadState    string `json:"loadState"`
	ActiveState  string `json:"activeState"`
	SubState     string `json:"subState"`
	EnabledState string `json:"enabledState"`
	Description  string `json:"description"`
	Details      string `json:"details,omitempty"`
	RecentLogs   string `json:"recentLogs,omitempty"`
}

// SystemdReport holds the systemd inspection state for a Hive.
type SystemdReport struct {
	Available   bool          `json:"available"`
	SystemState string        `json:"systemState"`
	Units       []SystemdUnit `json:"units"`
	Err         string        `json:"err,omitempty"`
}

// DockerContainer describes one Docker container on a Hive.
type DockerContainer struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Image      string `json:"image"`
	State      string `json:"state"`
	Status     string `json:"status"`
	Ports      string `json:"ports"`
	CPU        string `json:"cpu,omitempty"`
	Mem        string `json:"mem,omitempty"`
	RecentLogs string `json:"recentLogs,omitempty"`
}

// DockerReport holds the Docker inspection state for a Hive.
type DockerReport struct {
	Available  bool              `json:"available"`
	Version    string            `json:"version,omitempty"`
	Containers []DockerContainer `json:"containers"`
	Err        string            `json:"err,omitempty"`
}

// OllamaModel describes one model installed in Ollama on a Hive.
type OllamaModel struct {
	Name     string `json:"name"`
	ID       string `json:"id"`
	Size     string `json:"size"`
	Modified string `json:"modified"`
}

// ModelTroubleshootingReport holds Ollama and OpenClaw model/provider inspection results.
type ModelTroubleshootingReport struct {
	OllamaAvailable       bool                      `json:"ollamaAvailable"`
	OllamaReachable       bool                      `json:"ollamaReachable"`
	OllamaModels          []OllamaModel             `json:"ollamaModels"`
	ConfiguredDefault     string                    `json:"configuredDefault,omitempty"`
	AllowedModels         []string                  `json:"allowedModels,omitempty"`
	Providers             []openclaw.ProviderStatus `json:"providers,omitempty"`
	MissingProvidersInUse []string                  `json:"missingProvidersInUse,omitempty"`
	RouteIssues           []string                  `json:"routeIssues,omitempty"`
	Mismatches            []string                  `json:"mismatches,omitempty"`
	Err                   string                    `json:"err,omitempty"`
}

// HostCapabilities summarizes the OS, kernel, architecture, and optional
// runtime/service capabilities detected on a Hive.
type HostCapabilities struct {
	OS                     string                `json:"os,omitempty"`
	Kernel                 string                `json:"kernel,omitempty"`
	Arch                   string                `json:"arch,omitempty"`
	SystemdAvailable       bool                  `json:"systemdAvailable"`
	SystemdUserAvailable   bool                  `json:"systemdUserAvailable"`
	JournalctlAvailable    bool                  `json:"journalctlAvailable"`
	DockerAvailable        bool                  `json:"dockerAvailable"`
	DockerComposeAvailable bool                  `json:"dockerComposeAvailable"`
	OllamaAvailable        bool                  `json:"ollamaAvailable"`
	OllamaVersion          string                `json:"ollamaVersion,omitempty"`
	OpenClaw               openclaw.Capabilities `json:"openclaw"`
}

// HiveServicesReport bundles systemd, Docker, Model/Ollama, and capability
// reports for a Hive.
type HiveServicesReport struct {
	Hive         string                     `json:"hive"`
	Capabilities HostCapabilities           `json:"capabilities"`
	Systemd      SystemdReport              `json:"systemd"`
	Docker       DockerReport               `json:"docker"`
	Models       ModelTroubleshootingReport `json:"models"`
}

// DetectCapabilities probes the host OS/architecture, optional service tooling
// (systemd system/user bus, journalctl, Docker, Docker Compose, Ollama), and
// OpenClaw CLI subcommand capabilities on a Hive.
func DetectCapabilities(ctx context.Context, exec hosts.Execution) HostCapabilities {
	if exec == nil {
		return HostCapabilities{}
	}
	var caps HostCapabilities
	if uRes, err := exec.Run(ctx, "uname", "-s", "-r", "-m"); err == nil && uRes.Ok() {
		fields := strings.Fields(strings.TrimSpace(uRes.Stdout))
		if len(fields) >= 1 {
			caps.OS = fields[0]
		}
		if len(fields) >= 2 {
			caps.Kernel = fields[1]
		}
		if len(fields) >= 3 {
			caps.Arch = fields[2]
		}
	}
	if sdRes, err := exec.Run(ctx, "systemctl", "is-system-running"); err == nil && sdRes.ExitCode != 127 {
		st := strings.TrimSpace(sdRes.Stdout + " " + sdRes.Stderr)
		if st != "offline" && !strings.Contains(strings.ToLower(st), "not been booted with systemd") {
			caps.SystemdAvailable = true
		}
	}
	if sduRes, err := exec.Run(ctx, "systemctl", "--user", "is-system-running"); err == nil && sduRes.Ok() {
		caps.SystemdUserAvailable = true
	}
	if jRes, err := exec.Run(ctx, "journalctl", "--version"); err == nil && jRes.Ok() {
		caps.JournalctlAvailable = true
	}
	if dRes, err := exec.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}}"); err == nil && dRes.Ok() {
		caps.DockerAvailable = true
	}
	if dcRes, err := exec.Run(ctx, "docker", "compose", "version", "--short"); err == nil && dcRes.Ok() {
		caps.DockerComposeAvailable = true
	}
	if olRes, err := exec.Run(ctx, "ollama", "--version"); err == nil && olRes.Ok() {
		caps.OllamaAvailable = true
		caps.OllamaVersion = strings.TrimSpace(firstNonEmpty(olRes.Stdout, olRes.Stderr))
	}
	caps.OpenClaw = openclaw.NewClient(exec).ProbeCapabilities(ctx)
	return caps
}

// InspectAll runs capability-detected read-only inspection for systemd, Docker,
// and Ollama/model providers on the given Hive.
func InspectAll(ctx context.Context, hive string, exec hosts.Execution) *HiveServicesReport {
	if exec == nil {
		return &HiveServicesReport{
			Hive:    hive,
			Systemd: SystemdReport{Err: "no execution configured"},
			Docker:  DockerReport{Err: "no execution configured"},
			Models:  ModelTroubleshootingReport{Err: "no execution configured"},
		}
	}
	sd := InspectSystemd(ctx, exec)
	dk := InspectDocker(ctx, exec)
	md := InspectModels(ctx, exec)
	caps := DetectCapabilities(ctx, exec)
	if sd.Available {
		caps.SystemdAvailable = true
	}
	if dk.Available {
		caps.DockerAvailable = true
	}
	if md.OllamaAvailable {
		caps.OllamaAvailable = true
	}
	return &HiveServicesReport{
		Hive:         hive,
		Capabilities: caps,
		Systemd:      sd,
		Docker:       dk,
		Models:       md,
	}
}

// InspectSystemd detects whether systemd is present and lists relevant units
// (prioritizing openclaw, ollama, docker, ssh, and any failed units).
func InspectSystemd(ctx context.Context, exec hosts.Execution) SystemdReport {
	res, err := exec.Run(ctx, "systemctl", "is-system-running")
	if err != nil {
		return SystemdReport{Available: false, Err: err.Error()}
	}
	if res.ExitCode == 127 {
		return SystemdReport{Available: false, Err: "systemd not installed"}
	}
	sysState := strings.TrimSpace(res.Stdout)
	if sysState == "" {
		sysState = strings.TrimSpace(res.Stderr)
	}
	if sysState == "offline" || strings.Contains(strings.ToLower(sysState), "system has not been booted with systemd") {
		return SystemdReport{Available: false, SystemState: "offline", Err: "host is not booted with systemd"}
	}

	listRes, listErr := exec.Run(ctx, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain")
	if listErr != nil {
		return SystemdReport{Available: true, SystemState: sysState, Err: listErr.Error()}
	}
	units := parseSystemctlUnits(listRes.Stdout)
	return SystemdReport{
		Available:   true,
		SystemState: sysState,
		Units:       units,
	}
}

// InspectSystemdUnit fetches enabled/disabled state, unit status details, and
// recent journal logs for a single systemd service unit.
func InspectSystemdUnit(ctx context.Context, exec hosts.Execution, unitName string, tail int) (SystemdUnit, error) {
	if exec == nil {
		return SystemdUnit{}, errors.New("no execution configured")
	}
	if tail <= 0 {
		tail = 30
	}
	u := SystemdUnit{Name: unitName}
	if enRes, err := exec.Run(ctx, "systemctl", "is-enabled", unitName); err == nil {
		u.EnabledState = strings.TrimSpace(enRes.Stdout)
	}
	if stRes, err := exec.Run(ctx, "systemctl", "status", unitName, "--no-pager", "-n", "10"); err == nil {
		u.Details = strings.TrimSpace(stRes.Stdout)
		if u.Details == "" {
			u.Details = strings.TrimSpace(stRes.Stderr)
		}
	}
	if logRes, err := exec.Run(ctx, "journalctl", "-u", unitName, "-n", fmt.Sprintf("%d", tail), "--no-pager"); err == nil {
		u.RecentLogs = strings.TrimSpace(logRes.Stdout)
	}
	return u, nil
}

// ControlSystemdService executes start, stop, or restart on unitName ONLY when
// confirmed is true.
func ControlSystemdService(ctx context.Context, exec hosts.Execution, unitName string, action ServiceAction, confirmed bool) error {
	if !confirmed {
		return ErrConfirmationRequired
	}
	if exec == nil {
		return errors.New("no execution configured")
	}
	unitName = strings.TrimSpace(unitName)
	if unitName == "" || strings.HasPrefix(unitName, "-") {
		return fmt.Errorf("invalid systemd unit name %q", unitName)
	}
	switch action {
	case ActionStart, ActionStop, ActionRestart:
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
	res, err := exec.Run(ctx, "systemctl", string(action), unitName)
	if err != nil {
		return err
	}
	if !res.Ok() {
		return fmt.Errorf("systemctl %s %s exited %d: %s", action, unitName, res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return nil
}

// InspectDocker detects Docker availability and lists containers along with
// basic non-streaming stats where available.
func InspectDocker(ctx context.Context, exec hosts.Execution) DockerReport {
	verRes, err := exec.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return DockerReport{Available: false, Err: err.Error()}
	}
	if verRes.ExitCode == 127 {
		return DockerReport{Available: false, Err: "docker not installed"}
	}
	if !verRes.Ok() {
		return DockerReport{
			Available: false,
			Err:       firstNonEmpty(verRes.Stderr, verRes.Stdout, "docker daemon unreachable"),
		}
	}
	rep := DockerReport{
		Available: true,
		Version:   strings.TrimSpace(verRes.Stdout),
	}
	psRes, psErr := exec.Run(ctx, "docker", "ps", "-a", "--format", "{{json .}}")
	if psErr != nil || !psRes.Ok() {
		rep.Err = firstNonEmpty(psRes.Stderr, "failed to list containers")
		return rep
	}
	rep.Containers = parseDockerPS(psRes.Stdout)

	// Attach inexpensive non-streaming stats when at least one container is running.
	hasRunning := false
	for _, c := range rep.Containers {
		if strings.EqualFold(c.State, "running") || strings.HasPrefix(strings.ToLower(c.Status), "up") {
			hasRunning = true
			break
		}
	}
	if hasRunning {
		if stRes, stErr := exec.Run(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}"); stErr == nil && stRes.Ok() {
			applyDockerStats(rep.Containers, stRes.Stdout)
		}
	}
	return rep
}

// FetchDockerLogs fetches the recent logs for a container.
func FetchDockerLogs(ctx context.Context, exec hosts.Execution, container string, tail int) (string, error) {
	if exec == nil {
		return "", errors.New("no execution configured")
	}
	if tail <= 0 {
		tail = 50
	}
	res, err := exec.Run(ctx, "docker", "logs", "--tail", fmt.Sprintf("%d", tail), container)
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(res.Stdout)
	errOut := strings.TrimSpace(res.Stderr)
	if out != "" && errOut != "" {
		return out + "\n" + errOut, nil
	}
	if out != "" {
		return out, nil
	}
	if !res.Ok() {
		return "", fmt.Errorf("docker logs exited %d: %s", res.ExitCode, errOut)
	}
	return errOut, nil
}

// InspectModels inspects Ollama reachability/models and compares them against
// OpenClaw's configured models and providers to detect obvious mismatches.
func InspectModels(ctx context.Context, exec hosts.Execution) ModelTroubleshootingReport {
	var rep ModelTroubleshootingReport

	// 1. Check Ollama CLI/service if installed.
	olRes, olErr := exec.Run(ctx, "ollama", "list")
	switch {
	case olErr != nil && hosts.IsKind(olErr, hosts.KindNotFound):
		rep.OllamaAvailable = false
	case olErr == nil && olRes.ExitCode == 127:
		rep.OllamaAvailable = false
	case olErr != nil:
		rep.OllamaAvailable = true
		rep.OllamaReachable = false
		rep.Err = olErr.Error()
	case !olRes.Ok():
		rep.OllamaAvailable = true
		rep.OllamaReachable = false
		rep.Err = firstNonEmpty(olRes.Stderr, olRes.Stdout, "ollama daemon unreachable")
	default:
		rep.OllamaAvailable = true
		rep.OllamaReachable = true
		rep.OllamaModels = parseOllamaList(olRes.Stdout)
	}

	// 2. Query OpenClaw models status when available.
	ms, msErr := openclaw.NewClient(exec).ModelsStatus(ctx, "")
	if msErr == nil && ms != nil {
		rep.ConfiguredDefault = firstNonEmpty(ms.ResolvedDefault, ms.DefaultModel)
		rep.AllowedModels = ms.Allowed
		rep.Providers = ms.Providers
		rep.MissingProvidersInUse = ms.MissingProvidersInUse
		rep.RouteIssues = ms.ModelRouteIssues
	}

	// 3. Detect obvious mismatches between configured models and reachable providers/Ollama.
	installedOllama := make(map[string]bool, len(rep.OllamaModels))
	for _, m := range rep.OllamaModels {
		installedOllama[strings.ToLower(m.Name)] = true
		if !strings.Contains(m.Name, ":") {
			installedOllama[strings.ToLower(m.Name)+":latest"] = true
		}
	}

	candidates := append([]string{}, rep.AllowedModels...)
	if rep.ConfiguredDefault != "" {
		candidates = append([]string{rep.ConfiguredDefault}, candidates...)
	}
	seenMismatch := make(map[string]bool)
	addMismatch := func(msg string) {
		if !seenMismatch[msg] {
			seenMismatch[msg] = true
			rep.Mismatches = append(rep.Mismatches, msg)
		}
	}

	for _, modelRef := range candidates {
		lower := strings.ToLower(strings.TrimSpace(modelRef))
		if !strings.HasPrefix(lower, "ollama/") {
			continue
		}
		modelName := strings.TrimPrefix(lower, "ollama/")
		if !rep.OllamaAvailable {
			addMismatch(fmt.Sprintf("model %q requires Ollama, but Ollama is not installed on this Hive", modelRef))
			continue
		}
		if !rep.OllamaReachable {
			addMismatch(fmt.Sprintf("model %q requires Ollama, but the Ollama service is unreachable", modelRef))
			continue
		}
		if !installedOllama[modelName] && !installedOllama[modelName+":latest"] {
			addMismatch(fmt.Sprintf("configured Ollama model %q is not present in `ollama list`", modelRef))
		}
	}
	for _, mp := range rep.MissingProvidersInUse {
		addMismatch(fmt.Sprintf("provider %q is in use but has missing credentials/configuration", mp))
	}
	for _, ri := range rep.RouteIssues {
		addMismatch(fmt.Sprintf("model route issue: %s", ri))
	}

	return rep
}

func parseSystemctlUnits(out string) []SystemdUnit {
	var relevant, other []SystemdUnit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		desc := ""
		if len(fields) > 4 {
			desc = strings.Join(fields[4:], " ")
		}
		u := SystemdUnit{
			Name:        fields[0],
			LoadState:   fields[1],
			ActiveState: fields[2],
			SubState:    fields[3],
			Description: desc,
		}
		lower := strings.ToLower(u.Name + " " + u.Description)
		if u.ActiveState == "failed" ||
			strings.Contains(lower, "openclaw") ||
			strings.Contains(lower, "ollama") ||
			strings.Contains(lower, "docker") ||
			strings.Contains(lower, "containerd") ||
			strings.Contains(lower, "ssh") {
			relevant = append(relevant, u)
		} else {
			other = append(other, u)
		}
	}
	if len(relevant) > 0 {
		// Append up to 15 additional units for context without flooding UI.
		limit := 15
		if len(other) < limit {
			limit = len(other)
		}
		return append(relevant, other[:limit]...)
	}
	if len(other) > 25 {
		return other[:25]
	}
	return other
}

func parseDockerPS(out string) []DockerContainer {
	var list []DockerContainer
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var raw struct {
			ID     string `json:"ID"`
			Names  string `json:"Names"`
			Image  string `json:"Image"`
			State  string `json:"State"`
			Status string `json:"Status"`
			Ports  string `json:"Ports"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}
		list = append(list, DockerContainer{
			ID:     raw.ID,
			Name:   raw.Names,
			Image:  raw.Image,
			State:  raw.State,
			Status: raw.Status,
			Ports:  raw.Ports,
		})
	}
	return list
}

func applyDockerStats(containers []DockerContainer, out string) {
	byName := make(map[string]int, len(containers))
	byID := make(map[string]int, len(containers))
	for i, c := range containers {
		byName[c.Name] = i
		byID[c.ID] = i
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var st struct {
			ID       string `json:"ID"`
			Name     string `json:"Name"`
			CPUPerc  string `json:"CPUPerc"`
			MemUsage string `json:"MemUsage"`
		}
		if err := json.Unmarshal([]byte(line), &st); err != nil {
			continue
		}
		if idx, ok := byName[st.Name]; ok {
			containers[idx].CPU = st.CPUPerc
			containers[idx].Mem = st.MemUsage
		} else if idx, ok := byID[st.ID]; ok {
			containers[idx].CPU = st.CPUPerc
			containers[idx].Mem = st.MemUsage
		}
	}
}

func parseOllamaList(out string) []OllamaModel {
	var models []OllamaModel
	lines := strings.Split(out, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if i == 0 && strings.HasPrefix(strings.ToUpper(line), "NAME") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		m := OllamaModel{
			Name: fields[0],
			ID:   fields[1],
		}
		if len(fields) >= 4 {
			m.Size = fields[2] + " " + fields[3]
		}
		if len(fields) > 4 {
			m.Modified = strings.Join(fields[4:], " ")
		}
		models = append(models, m)
	}
	return models
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}
