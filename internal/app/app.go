// Package app wires BeeChief's domain packages to the TUI and CLI subcommands.
//
// It owns startup, configuration persistence, and CLI dispatch; it contains
// no TUI rendering and no raw shell-command strings.
package app

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/discovery"
	"github.com/meistro57/beechief/internal/hosts"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/report"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
	"github.com/meistro57/beechief/internal/tui"
)

// Run starts BeeChief using os.Args and os.Stdout.
//
// With no subcommand, it starts the Bubble Tea terminal UI. With
// `beechief diagnose [--export] [-o path]`, it runs a non-interactive
// diagnostic pass and exports a redacted Honey Report (issue #11).
func Run() error {
	return RunArgs(os.Args[1:], os.Stdout)
}

// RunArgs dispatches CLI arguments or launches the interactive TUI.
func RunArgs(args []string, stdout io.Writer) error {
	cfgPath := config.DefaultPath()
	cfg, err := config.LoadOrDefault(cfgPath)
	if err != nil {
		return err
	}

	if len(args) > 0 && args[0] == "diagnose" {
		return runDiagnoseCLI(cfg, args[1:], stdout)
	}

	backend := newBackend(cfgPath, cfg)
	scan := func(ctx context.Context) *agents.Inventory { return backend.Scan(ctx) }

	p := tea.NewProgram(
		tui.New(tui.WithScanner(scan), tui.WithOps(backend)),
		tea.WithAltScreen(),
	)
	_, err = p.Run()
	return err
}

func runDiagnoseCLI(cfg config.Config, args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	export := fs.Bool("export", false, "export portable Honey Report to disk")
	outPath := fs.String("output", "", "output file path for Honey Report")
	outShort := fs.String("o", "", "output file path for Honey Report (shorthand)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("diagnose flags: %w", err)
	}
	path := *outPath
	if path == "" {
		path = *outShort
	}

	ctx := context.Background()
	scanner := discovery.FromConfig(cfg)
	inv := scanner.Scan(ctx)
	execs := buildExecMap(cfg)
	hr := report.Collect(ctx, cfg, inv, execs)

	if *export || path != "" {
		written, err := report.Export(hr, path)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Honey Report exported to: %s\n", written)
		return nil
	}

	fmt.Fprint(stdout, report.RenderMarkdown(hr))
	return nil
}

type backend struct {
	mu      sync.RWMutex
	cfgPath string
	cfg     config.Config
	scanner *discovery.Scanner
	execs   map[string]hosts.Execution
}

func newBackend(cfgPath string, cfg config.Config) *backend {
	return &backend{
		cfgPath: cfgPath,
		cfg:     cfg,
		scanner: discovery.FromConfig(cfg),
		execs:   buildExecMap(cfg),
	}
}

func buildExecMap(cfg config.Config) map[string]hosts.Execution {
	m := make(map[string]hosts.Execution, len(cfg.Hives))
	for _, h := range cfg.Hives {
		if exec, err := hosts.ForHive(h); err == nil {
			m[h.Name] = exec
		}
	}
	return m
}

func (b *backend) Scan(ctx context.Context) *agents.Inventory {
	b.mu.RLock()
	sc := b.scanner
	b.mu.RUnlock()
	return sc.Rescan(ctx)
}

func (b *backend) execFor(hive string) (hosts.Execution, error) {
	b.mu.RLock()
	exec := b.execs[hive]
	b.mu.RUnlock()
	if exec == nil {
		return nil, fmt.Errorf("hive %q has no configured execution transport", hive)
	}
	return exec, nil
}

func (b *backend) ListSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillListReport, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).ListSkills(ctx, bee.ID)
}

func (b *backend) CheckSkills(ctx context.Context, bee agents.Bee) (*openclaw.SkillCheckReport, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).CheckSkills(ctx, bee.ID)
}

func (b *backend) SelfReview(ctx context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).SelfReview(ctx, bee.ID)
}

func (b *backend) ListWorkshop(ctx context.Context, bee agents.Bee) (*openclaw.WorkshopListReport, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).ListWorkshop(ctx, bee.ID)
}

func (b *backend) ShowWorkshopProposal(ctx context.Context, bee agents.Bee, name string) (*openclaw.WorkshopProposal, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).ShowWorkshopProposal(ctx, bee.ID, name)
}

func (b *backend) ModelsStatus(ctx context.Context, bee agents.Bee) (*openclaw.ModelStatusReport, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return nil, err
	}
	return openclaw.NewClient(exec).ModelsStatus(ctx, bee.ID)
}

func (b *backend) SendInstruction(ctx context.Context, bee agents.Bee, message string) (string, error) {
	exec, err := b.execFor(bee.Hive)
	if err != nil {
		return "", err
	}
	return openclaw.NewClient(exec).SendInstruction(ctx, bee.ID, message)
}

func (b *backend) RunHiveHealth(ctx context.Context, hive string) (*diagnostics.HiveHealthReport, error) {
	b.mu.RLock()
	exec := b.execs[hive]
	b.mu.RUnlock()
	return diagnostics.Run(ctx, hive, exec), nil
}

func (b *backend) InspectServices(ctx context.Context, hive string) (*services.HiveServicesReport, error) {
	b.mu.RLock()
	exec := b.execs[hive]
	b.mu.RUnlock()
	return services.InspectAll(ctx, hive, exec), nil
}

func (b *backend) InspectSystemdUnit(ctx context.Context, hive, unit string) (services.SystemdUnit, error) {
	exec, err := b.execFor(hive)
	if err != nil {
		return services.SystemdUnit{}, err
	}
	return services.InspectSystemdUnit(ctx, exec, unit, 30)
}

func (b *backend) ControlSystemdService(ctx context.Context, hive, unit string, action services.ServiceAction, confirmed bool) error {
	if !confirmed {
		return services.ErrConfirmationRequired
	}
	exec, err := b.execFor(hive)
	if err != nil {
		return err
	}
	return services.ControlSystemdService(ctx, exec, unit, action, confirmed)
}

func (b *backend) FetchDockerLogs(ctx context.Context, hive, container string, tail int) (string, error) {
	exec, err := b.execFor(hive)
	if err != nil {
		return "", err
	}
	return services.FetchDockerLogs(ctx, exec, container, tail)
}

func (b *backend) FetchLogs(ctx context.Context, hive string, source logs.SourceKind, target string, tail int) (*logs.Result, error) {
	exec, err := b.execFor(hive)
	if err != nil {
		return nil, err
	}
	return logs.AdapterFor(source).Fetch(ctx, exec, target, tail)
}

func (b *backend) StartSwarmReview(mode swarm.Mode, bees []agents.Bee, onProgress func(swarm.Snapshot)) *swarm.Coordinator {
	b.mu.RLock()
	execCopy := make(map[string]hosts.Execution, len(b.execs))
	for k, v := range b.execs {
		execCopy[k] = v
	}
	b.mu.RUnlock()
	reviewer := swarm.NewOpenClawReviewer(execCopy)
	return swarm.NewCoordinator(mode, bees, reviewer, swarm.DefaultConcurrency, onProgress)
}

func (b *backend) ExportHoneyReport(ctx context.Context, inv *agents.Inventory, path string) (string, error) {
	b.mu.RLock()
	cfg := b.cfg
	execCopy := make(map[string]hosts.Execution, len(b.execs))
	for k, v := range b.execs {
		execCopy[k] = v
	}
	b.mu.RUnlock()
	hr := report.Collect(ctx, cfg, inv, execCopy)
	return report.Export(hr, path)
}

func (b *backend) GetConfig() config.Config {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.cfg
}

func (b *backend) ConfigPath() string {
	return b.cfgPath
}

func (b *backend) TestHive(ctx context.Context, h config.Hive) tui.HiveTestResult {
	if err := h.Validate(); err != nil {
		return tui.HiveTestResult{Hive: h.Name, Err: err.Error()}
	}
	exec, err := hosts.ForHive(h)
	if err != nil {
		return tui.HiveTestResult{Hive: h.Name, Err: err.Error()}
	}
	pr := hosts.Probe(ctx, exec)
	if !pr.Reachable {
		errMsg := "host unreachable"
		if pr.Err != nil {
			errMsg = pr.Err.Error()
		}
		return tui.HiveTestResult{Hive: h.Name, Reachable: false, Err: errMsg}
	}
	ver, vErr := openclaw.NewClient(exec).Version(ctx)
	if vErr != nil {
		return tui.HiveTestResult{
			Hive:            h.Name,
			Reachable:       true,
			OpenClawPresent: false,
			Err:             vErr.Error(),
		}
	}
	return tui.HiveTestResult{
		Hive:            h.Name,
		Reachable:       true,
		OpenClawPresent: true,
		OpenClawVersion: ver,
	}
}

func (b *backend) SaveConfig(cfg config.Config) error {
	if b.cfgPath == "" {
		return errors.New("no config path configured")
	}
	if err := config.Save(b.cfgPath, cfg); err != nil {
		return err
	}
	b.mu.Lock()
	b.cfg = cfg
	b.scanner = discovery.FromConfig(cfg)
	b.execs = buildExecMap(cfg)
	b.mu.Unlock()
	return nil
}
