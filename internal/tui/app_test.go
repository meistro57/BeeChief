package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
)

func TestNewHasSections(t *testing.T) {
	m := New()
	if len(m.sections) != len(DefaultSections) {
		t.Fatalf("sections: got %d, want %d", len(m.sections), len(DefaultSections))
	}
}

func TestViewRendersTitleAndMenu(t *testing.T) {
	out := New().View()
	if !strings.Contains(out, "BeeChief") {
		t.Errorf("view missing title: %q", out)
	}
	if !strings.Contains(out, "Swarm Overview") {
		t.Errorf("view missing first section: %q", out)
	}
}

func TestNavigationBounds(t *testing.T) {
	m := New()

	// Up at the top must clamp, not wrap or panic.
	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := mm.(Model).cursor; got != 0 {
		t.Errorf("cursor after up at top: got %d, want 0", got)
	}

	// Down past the end must clamp.
	for i := 0; i < len(DefaultSections)+5; i++ {
		mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	if got := mm.(Model).cursor; got != len(DefaultSections)-1 {
		t.Errorf("cursor after many downs: got %d, want %d", got, len(DefaultSections)-1)
	}
}

func TestEnterDoesNotPanic(t *testing.T) {
	m := New()
	mm, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Errorf("enter should not quit yet, got cmd")
	}
	if mm.(Model).quitting {
		t.Errorf("enter should not set quitting")
	}
}

func TestQuitKeys(t *testing.T) {
	for _, k := range []string{"q", "esc", "ctrl+c"} {
		m := New()
		var msg tea.KeyMsg
		switch k {
		case "q":
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+c":
			msg = tea.KeyMsg{Type: tea.KeyCtrlC}
		}
		mm, _ := m.Update(msg)
		if !mm.(Model).quitting {
			t.Errorf("key %q did not quit", k)
		}
		if mm.(Model).View() != "" {
			t.Errorf("key %q: quitting view should be empty", k)
		}
	}
}

func TestWindowSizeIgnoredSafely(t *testing.T) {
	m := New()
	mm, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if mm.(Model).width != 80 || mm.(Model).height != 24 {
		t.Errorf("window size not recorded")
	}
}

func fakeInventory() *agents.Inventory {
	inv := agents.NewInventory()
	inv.Record(agents.HiveStatus{Hive: "alpha"}, []agents.Bee{{ID: "one"}, {ID: "two"}})
	inv.Record(agents.HiveStatus{Hive: "beta", Err: errors.New("unreachable")}, nil)
	return inv
}

func TestScannerRunsOnInitAndRendersHives(t *testing.T) {
	calls := 0
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		calls++
		return fakeInventory()
	}))
	if !strings.Contains(m.View(), "Scanning") {
		t.Errorf("expected scanning indicator before first result")
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init should start a scan")
	}
	mm, _ := m.Update(cmd())
	out := mm.(Model).View()
	if calls != 1 {
		t.Errorf("scan calls: %d", calls)
	}
	for _, want := range []string{"Bees discovered: 2", "alpha: 2 bees", "beta: unreachable", "✗"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestRescanKey(t *testing.T) {
	calls := 0
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		calls++
		return fakeInventory()
	}))
	mm, _ := m.Update(m.Init()())

	r := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}
	mm, cmd := mm.(Model).Update(r)
	if cmd == nil {
		t.Fatal("r should trigger a rescan")
	}
	// A second r while scanning is ignored.
	if _, again := mm.(Model).Update(r); again != nil {
		t.Error("rescan while scanning should be ignored")
	}
	mm.(Model).Update(cmd())
	if calls != 2 {
		t.Errorf("scan calls: got %d, want 2", calls)
	}
}

func TestNoScannerIsInert(t *testing.T) {
	m := New()
	if m.Init() != nil {
		t.Error("Init without scanner should do nothing")
	}
	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")}); cmd != nil {
		t.Error("r without scanner should do nothing")
	}
}

type fakeOps struct {
	cfg           config.Config
	restartedUnit string
}

func (f *fakeOps) ListSkills(_ context.Context, bee agents.Bee) (*openclaw.SkillListReport, error) {
	return &openclaw.SkillListReport{
		AgentID:   bee.ID,
		RawOutput: `{"skills":[{"name":"github"}]}`,
		Skills: []openclaw.Skill{
			{Name: "github", Description: "GitHub CLI skill", Eligible: true},
			{Name: "1password", Description: "1Password skill", Eligible: false, Missing: openclaw.SkillMissing{Bins: []string{"op"}}},
		},
	}, nil
}

func (f *fakeOps) CheckSkills(_ context.Context, bee agents.Bee) (*openclaw.SkillCheckReport, error) {
	return &openclaw.SkillCheckReport{
		AgentID: bee.ID,
		Summary: openclaw.SkillCheckSummary{Total: 2, Eligible: 1, MissingRequirements: 1},
	}, nil
}

func (f *fakeOps) SelfReview(_ context.Context, bee agents.Bee) (*openclaw.SelfReviewReport, error) {
	return &openclaw.SelfReviewReport{
		AgentID:   bee.ID,
		Summary:   "Reviewed skills for " + bee.ID,
		Proposals: []openclaw.WorkshopProposal{{Name: "auto-deploy", Description: "Deploy helper"}},
	}, nil
}

func (f *fakeOps) ListWorkshop(_ context.Context, bee agents.Bee) (*openclaw.WorkshopListReport, error) {
	return &openclaw.WorkshopListReport{
		AgentID:   bee.ID,
		Mode:      "auto",
		Root:      "/srv/workshop",
		Proposals: []openclaw.WorkshopProposal{{Name: "auto-deploy", Description: "Deploy helper", UseCount: 2}},
	}, nil
}

func (f *fakeOps) ShowWorkshopProposal(_ context.Context, _ agents.Bee, name string) (*openclaw.WorkshopProposal, error) {
	return &openclaw.WorkshopProposal{Name: name, Content: "# Auto Deploy\nSteps..."}, nil
}

func (f *fakeOps) ModelsStatus(_ context.Context, _ agents.Bee) (*openclaw.ModelStatusReport, error) {
	return &openclaw.ModelStatusReport{
		DefaultModel: "openrouter/auto",
		Allowed:      []string{"openrouter/auto"},
	}, nil
}

func (f *fakeOps) SendInstruction(_ context.Context, _ agents.Bee, message string) (string, error) {
	return "Ack: " + message, nil
}

func (f *fakeOps) RunHiveHealth(_ context.Context, hive string) (*diagnostics.HiveHealthReport, error) {
	return &diagnostics.HiveHealthReport{
		Hive: hive,
		Checks: []diagnostics.CheckResult{
			{Name: "Hive reachability", State: diagnostics.StatePass, Summary: "reachable"},
			{Name: "Disk free space", State: diagnostics.StateWarning, Summary: "88% used", Detail: "/dev/sda1"},
		},
	}, nil
}

func (f *fakeOps) InspectServices(_ context.Context, hive string) (*services.HiveServicesReport, error) {
	return &services.HiveServicesReport{
		Hive: hive,
		Systemd: services.SystemdReport{
			Available:   true,
			SystemState: "running",
			Units:       []services.SystemdUnit{{Name: "openclaw.service", ActiveState: "active", SubState: "running", Description: "Gateway"}},
		},
		Docker: services.DockerReport{
			Available:  true,
			Version:    "27.0",
			Containers: []services.DockerContainer{{Name: "oc-box", Image: "oc:latest", State: "running", Status: "Up 1h"}},
		},
		Models: services.ModelTroubleshootingReport{
			ConfiguredDefault: "ollama/llama3:8b",
			OllamaAvailable:   true,
			OllamaReachable:   true,
			Mismatches:        []string{"configured Ollama model \"ollama/llama3:8b\" is not present in `ollama list`"},
		},
	}, nil
}

func (f *fakeOps) InspectSystemdUnit(_ context.Context, _, unit string) (services.SystemdUnit, error) {
	return services.SystemdUnit{Name: unit, EnabledState: "enabled", Details: "Active: active", RecentLogs: "started ok"}, nil
}

func (f *fakeOps) ControlSystemdService(_ context.Context, _, unit string, _ services.ServiceAction, confirmed bool) error {
	if !confirmed {
		return services.ErrConfirmationRequired
	}
	f.restartedUnit = unit
	return nil
}

func (f *fakeOps) FetchDockerLogs(_ context.Context, _, container string, _ int) (string, error) {
	return "logs from " + container, nil
}

func (f *fakeOps) FetchLogs(_ context.Context, _ string, source logs.SourceKind, target string, _ int) (*logs.Result, error) {
	return &logs.Result{
		Source:  source,
		Target:  target,
		Command: logs.CommandString(source, target, 100),
		Entries: []logs.Entry{
			{Line: "INFO boot ok", Level: logs.LevelInfo},
			{Line: "ERROR bad token", Level: logs.LevelError},
		},
	}, nil
}

func (f *fakeOps) StartSwarmReview(mode swarm.Mode, bees []agents.Bee, onProgress func(swarm.Snapshot)) *swarm.Coordinator {
	return swarm.NewCoordinator(mode, bees, func(_ context.Context, b agents.Bee) (*openclaw.SelfReviewReport, error) {
		return f.SelfReview(context.Background(), b)
	}, 2, onProgress)
}

func (f *fakeOps) ExportHoneyReport(_ context.Context, _ *agents.Inventory, _ string) (string, error) {
	return "/tmp/honey-report-test.md", nil
}

func (f *fakeOps) GetConfig() config.Config { return f.cfg }
func (f *fakeOps) ConfigPath() string       { return "/tmp/hosts.yaml" }

func (f *fakeOps) TestHive(_ context.Context, h config.Hive) HiveTestResult {
	return HiveTestResult{Hive: h.Name, Reachable: true, OpenClawPresent: true, OpenClawVersion: "OpenClaw 2026.10.5"}
}

func (f *fakeOps) SaveConfig(cfg config.Config) error {
	f.cfg = cfg
	return nil
}

func runeKey(r string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(r)}
}

func runCmdUntilIdle(t *testing.T, mm tea.Model, cmd tea.Cmd) tea.Model {
	t.Helper()
	for i := 0; cmd != nil && i < 100; i++ {
		msg := cmd()
		if msg == nil {
			return mm
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				if c != nil {
					mm = runCmdUntilIdle(t, mm, c)
				}
			}
			return mm
		}
		mm, cmd = mm.Update(msg)
	}
	return mm
}

func TestSwarmOverviewAndBeeDetailFlow(t *testing.T) {
	ops := &fakeOps{cfg: config.Config{Hives: []config.Hive{{Name: "alpha", Type: config.TypeLocal}}}}
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		return fakeInventory()
	}), WithOps(ops))
	mm, _ := m.Update(m.Init()())

	// Enter Swarm Overview (section 0)
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	view := mm.(Model).View()
	for _, want := range []string{"Swarm Overview", "Total Hives: 2", "Total Bees: 2", "alpha [online]", "beta [offline]"} {
		if !strings.Contains(view, want) {
			t.Errorf("Swarm Overview missing %q:\n%s", want, view)
		}
	}

	// Filter Bees with '/'
	mm, _ = mm.(Model).Update(runeKey("/"))
	mm, _ = mm.(Model).Update(runeKey("two"))
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := len(mm.(Model).filteredBees()); got != 1 || mm.(Model).filteredBees()[0].ID != "two" {
		t.Fatalf("filtered bees: %+v", mm.(Model).filteredBees())
	}

	// Enter Bee Detail for "two"
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	detailView := mm.(Model).View()
	for _, act := range BeeDetailActions {
		if !strings.Contains(detailView, act) {
			t.Errorf("Bee Detail view missing action %q:\n%s", act, detailView)
		}
	}

	// Run "Visible skills" (action index 1) from Bee Detail -> transitions to Comb screen
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	var cmd tea.Cmd
	mm, cmd = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected Visible skills action to return async cmd")
	}
	mm, _ = mm.(Model).Update(cmd())
	combView := mm.(Model).View()
	for _, want := range []string{"Comb / Skills Inspection", "github", "1password", "bins: op"} {
		if !strings.Contains(combView, want) {
			t.Errorf("Comb view missing %q:\n%s", want, combView)
		}
	}
}

func TestSelfReviewWorkshopAndSwarmReviewFlow(t *testing.T) {
	ops := &fakeOps{}
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		return fakeInventory()
	}), WithOps(ops))
	mm, _ := m.Update(m.Init()())

	// Navigate to section 3 ("Self Review")
	for i := 0; i < 3; i++ {
		mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	var cmd tea.Cmd
	mm, cmd = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		mm, _ = mm.(Model).Update(cmd())
	}
	// Run single-Bee self-review ('s')
	mm, cmd = mm.(Model).Update(runeKey("s"))
	if cmd == nil {
		t.Fatal("expected self-review cmd")
	}
	mm, _ = mm.(Model).Update(cmd())
	if !strings.Contains(mm.(Model).View(), "Reviewed skills for one") {
		t.Errorf("self-review output not rendered:\n%s", mm.(Model).View())
	}

	// Inspect Workshop proposal ('enter')
	mm, cmd = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected show proposal cmd")
	}
	mm, _ = mm.(Model).Update(cmd())
	if !strings.Contains(mm.(Model).View(), "# Auto Deploy") {
		t.Errorf("proposal detail not rendered:\n%s", mm.(Model).View())
	}
	// Close proposal detail ('esc')
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Launch Swarm Review across all reachable Bees ('3')
	mm, cmd = mm.(Model).Update(runeKey("3"))
	if cmd == nil {
		t.Fatal("expected swarm review cmd")
	}
	mm = runCmdUntilIdle(t, mm, cmd)
	if !strings.Contains(mm.(Model).View(), "Swarm Review (all_reachable) — total=2 complete=2") {
		t.Errorf("swarm review summary missing:\n%s", mm.(Model).View())
	}
}

func TestTroubleshootingServicesLogsAndWizardFlows(t *testing.T) {
	ops := &fakeOps{}
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		return fakeInventory()
	}), WithOps(ops))
	mm, _ := m.Update(m.Init()())

	// Section 4: Troubleshooting (Hive Health + Honey Report)
	mModel := mm.(Model)
	mModel.cursor = 4
	var cmd tea.Cmd
	mm, cmd = mModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, _ = mm.(Model).Update(cmd())
	if !strings.Contains(mm.(Model).View(), "Disk free space") {
		t.Errorf("Hive Health missing Disk free space:\n%s", mm.(Model).View())
	}
	// Export Honey Report ('e')
	mm, cmd = mm.(Model).Update(runeKey("e"))
	mm, _ = mm.(Model).Update(cmd())
	if !strings.Contains(mm.(Model).View(), "/tmp/honey-report-test.md") {
		t.Errorf("Honey Report path missing:\n%s", mm.(Model).View())
	}
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Section 5: Services (systemd confirmation dialog + Docker + Models)
	mModel = mm.(Model)
	mModel.cursor = 5
	mm, cmd = mModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, _ = mm.(Model).Update(cmd())
	// Press 'R' to request restart -> must ask for confirmation first
	mm, _ = mm.(Model).Update(runeKey("R"))
	if ops.restartedUnit != "" || !strings.Contains(mm.(Model).View(), "CONFIRMATION REQUIRED") {
		t.Fatalf("expected confirmation prompt before service restart:\n%s", mm.(Model).View())
	}
	mm, cmd = mm.(Model).Update(runeKey("y"))
	mm, _ = mm.(Model).Update(cmd())
	if ops.restartedUnit != "openclaw.service" {
		t.Errorf("expected openclaw.service restart after 'y', got %q", ops.restartedUnit)
	}
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Section 6: Unified Log Browser
	mModel = mm.(Model)
	mModel.cursor = 6
	mm, cmd = mModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, _ = mm.(Model).Update(cmd())
	mm, _ = mm.(Model).Update(runeKey("e")) // errors-only filter
	logView := mm.(Model).View()
	if !strings.Contains(logView, "ERROR bad token") || strings.Contains(logView, "INFO boot ok") {
		t.Errorf("errors-only filter did not filter INFO line:\n%s", logView)
	}
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})

	// Section 7: Settings (Hive Configuration Wizard)
	mModel = mm.(Model)
	mModel.cursor = 7
	mm, _ = mModel.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm, _ = mm.(Model).Update(runeKey("l"))   // add local Hive preset
	mm, cmd = mm.(Model).Update(runeKey("t")) // test connection
	mm, _ = mm.(Model).Update(cmd())
	if !strings.Contains(mm.(Model).View(), "OpenClaw 2026.10.5") {
		t.Errorf("connection test result missing:\n%s", mm.(Model).View())
	}
	mm, _ = mm.(Model).Update(runeKey("s")) // save & rescan
	if len(ops.cfg.Hives) != 1 || ops.cfg.Hives[0].Name != "local" {
		t.Errorf("expected local Hive saved via wizard, got %+v", ops.cfg.Hives)
	}
}

func TestAccessibilityNoColorTerminalSizeAndLargeSwarmWindowing(t *testing.T) {
	// Build a large swarm of 30 Bees to test viewport windowing
	largeInv := agents.NewInventory()
	var manyBees []agents.Bee
	for i := 0; i < 30; i++ {
		manyBees = append(manyBees, agents.Bee{ID: "bee-" + string(rune('a'+(i%26))) + "-" + string(rune('0'+(i/10)))})
	}
	largeInv.Record(agents.HiveStatus{Hive: "mega-hive"}, manyBees)

	m := New(
		WithScanner(func(context.Context) *agents.Inventory { return largeInv }),
		WithNoColor(true),
	)
	mm, _ := m.Update(m.Init()())

	// Compact terminal size (<50x12) shows resize notice without panicking
	mm, _ = mm.(Model).Update(tea.WindowSizeMsg{Width: 42, Height: 10})
	if !strings.Contains(mm.(Model).View(), "Compact terminal 42x10") {
		t.Errorf("expected compact terminal notice, got:\n%s", mm.(Model).View())
	}

	// Normal terminal size (80x24) -> Swarm Overview shows windowed slice [showing 1–10 of 30]
	mm, _ = mm.(Model).Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	mm, _ = mm.(Model).Update(tea.KeyMsg{Type: tea.KeyEnter}) // enter Swarm Overview
	view := mm.(Model).View()
	if !strings.Contains(view, "[showing 1–10 of 30]") {
		t.Errorf("expected large-swarm pagination indicator in view:\n%s", view)
	}
}

func TestStaleBeeAndHiveResultsAreIgnored(t *testing.T) {
	m := New()
	inv := fakeInventory()
	m.inventory = inv
	bee := inv.ByHive["alpha"][0]
	m.selectedBee = &bee

	rep := &openclaw.SkillListReport{AgentID: bee.ID}
	mm, _ := m.Update(skillListMsg{target: "alpha/two", rep: rep})
	if mm.(Model).skillList != nil {
		t.Error("stale Bee result was applied")
	}
	mm, _ = mm.Update(skillListMsg{target: bee.Key(), rep: rep})
	if mm.(Model).skillList == nil {
		t.Error("current Bee result was ignored")
	}

	mm, _ = mm.Update(hiveHealthMsg{target: "beta", rep: &diagnostics.HiveHealthReport{Hive: "beta"}})
	if mm.(Model).healthReport != nil {
		t.Error("stale Hive result was applied")
	}
	mm, _ = mm.Update(hiveHealthMsg{target: "alpha", rep: &diagnostics.HiveHealthReport{Hive: "alpha"}})
	if mm.(Model).healthReport == nil {
		t.Error("current Hive result was ignored")
	}
}

func TestSwarmReviewStartsOnceAndBatchesProgress(t *testing.T) {
	ops := &fakeOps{}
	m := New(WithScanner(func(context.Context) *agents.Inventory {
		return fakeInventory()
	}), WithOps(ops))
	mm, _ := m.Update(m.Init()())
	model := mm.(Model)
	bee := model.inventory.ByHive["alpha"][0]
	model.selectedBee = &bee
	model.screen = screenSelfReview

	mm, cmd := model.Update(runeKey("3"))
	if cmd == nil {
		t.Fatal("expected a Swarm Review command")
	}
	if mm.(Model).swarmStream == nil {
		t.Error("expected a progress stream to be installed")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok || len(batch) < 2 {
		t.Fatalf("expected a batch of run and progress commands, got %T", cmd())
	}

	mm, again := mm.(Model).Update(runeKey("3"))
	if again != nil {
		t.Error("a second Swarm Review should be rejected while one is running")
	}
	if !strings.Contains(mm.(Model).View(), "already running") {
		t.Errorf("expected an already-running notice:\n%s", mm.(Model).View())
	}

	mm, _ = mm.(Model).Update(runeKey("x"))
	if mm.(Model).swarmSnap == nil || !mm.(Model).swarmSnap.Done {
		t.Error("cancel should mark the displayed snapshot as done")
	}
	if !strings.Contains(mm.(Model).View(), "canceled") {
		t.Errorf("expected a cancellation notice:\n%s", mm.(Model).View())
	}
}

func TestSwarmStreamDeliversAndClosesSafely(t *testing.T) {
	stream := newSwarmStream(7, 2)
	stream.send(swarm.Snapshot{})
	msg, ok := stream.wait()().(swarmProgressMsg)
	if !ok || msg.runID != 7 {
		t.Fatalf("unexpected progress message: %#v", msg)
	}
	stream.close()
	if stream.wait()() != nil {
		t.Error("closed stream should return a nil message")
	}
	stream.send(swarm.Snapshot{}) // must not panic after close
}

func TestSwarmProgressSchedulesNextWait(t *testing.T) {
	stream := newSwarmStream(3, 1)
	m := New()
	m.swarmStream = stream
	m.swarmRunID = 3
	mm, cmd := m.Update(swarmProgressMsg{runID: 3, snap: swarm.Snapshot{Summary: swarm.Summary{Total: 1}}})
	if cmd == nil {
		t.Error("a non-final progress update should schedule the next wait")
	}
	if _, stale := mm.(Model).Update(swarmProgressMsg{runID: 2, snap: swarm.Snapshot{}}); stale != nil {
		t.Error("progress from an old run should be ignored")
	}
}
