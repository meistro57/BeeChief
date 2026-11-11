// Package tui renders BeeChief's terminal interface.
//
// It consumes plain Go data and must not contain shell-command logic or
// OpenClaw specifics (roadmap: "Separate UI from operations").
package tui

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/diagnostics"
	"github.com/meistro57/beechief/internal/logs"
	"github.com/meistro57/beechief/internal/openclaw"
	"github.com/meistro57/beechief/internal/services"
	"github.com/meistro57/beechief/internal/swarm"
)

// Section is one navigable area of the main menu.
type Section struct {
	Title string
	Blurb string
}

// DefaultSections returns the navigation sections defined by the roadmap.
var DefaultSections = []Section{
	{"Swarm Overview", "Fleet-wide view of every discovered Bee"},
	{"Bees", "Individual OpenClaw agents, by Hive"},
	{"Comb / Skills", "Skills installed on each Bee"},
	{"Self Review", "Structured agent self-review"},
	{"Troubleshooting", "Isolate host, service, and model failures"},
	{"Services", "systemd, Docker, Ollama status"},
	{"Logs", "Unified log browser"},
	{"Settings", "Hives and preferences"},
}

// BeeDetailActions lists the actions available on the Bee detail screen (#4).
var BeeDetailActions = []string{
	"Agent status",
	"Visible skills",
	"Skill check",
	"Self-review skills",
	"Workshop proposals",
	"Curator / learning status",
	"Model status",
	"Send custom instruction",
}

// Screen identifies the active view in the TUI.
type Screen int

const (
	screenRoot Screen = iota
	screenSwarmOverview
	screenBees
	screenBeeDetail
	screenComb
	screenSelfReview
	screenTroubleshooting
	screenServices
	screenLogs
	screenSettings
)

type inputMode int

const (
	inputNone inputMode = iota
	inputFilterBees
	inputFilterSkills
	inputInstruction
	inputLogSearch
	inputLogTarget
	inputWizardField
)

var (
	titleStyle   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5B841"))
	taglineStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#67E8F9"))
	headerStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#F5B841"))
	helpStyle    = lipgloss.NewStyle().Faint(true)
	cursorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#F5B841")).Bold(true)
	plainStyle   = lipgloss.NewStyle()
	okStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	warnStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	failStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// ScanFunc produces a fresh Inventory. The TUI calls it asynchronously and
// never learns how discovery works.
type ScanFunc func(ctx context.Context) *agents.Inventory

// Option configures a Model.
type Option func(*Model)

// WithScanner enables discovery: a scan runs on startup and "r" rescans.
func WithScanner(scan ScanFunc) Option {
	return func(m *Model) { m.scan = scan }
}

// WithNoColor enables or disables accessible plain-text rendering without
// ANSI color styling (also enabled automatically when $NO_COLOR is set).
func WithNoColor(noColor bool) Option {
	return func(m *Model) { m.noColor = noColor }
}

// Async tea.Msg payloads.
type (
	inventoryMsg struct{ inv *agents.Inventory }
	skillListMsg struct {
		target string
		rep    *openclaw.SkillListReport
		err    error
	}
	skillCheckMsg struct {
		target string
		rep    *openclaw.SkillCheckReport
		err    error
	}
	selfReviewMsg struct {
		target string
		rep    *openclaw.SelfReviewReport
		err    error
	}
	workshopListMsg struct {
		target string
		rep    *openclaw.WorkshopListReport
		err    error
	}
	workshopShowMsg struct {
		target string
		prop   *openclaw.WorkshopProposal
		err    error
	}
	modelStatusMsg struct {
		target string
		rep    *openclaw.ModelStatusReport
		err    error
	}
	instructionMsg struct {
		target, reply string
		err           error
	}
	hiveHealthMsg struct {
		target string
		rep    *diagnostics.HiveHealthReport
		err    error
	}
	servicesMsg struct {
		target string
		rep    *services.HiveServicesReport
		err    error
	}
	unitDetailMsg struct {
		target string
		unit   services.SystemdUnit
		err    error
	}
	serviceControlMsg struct {
		target, unit string
		action       services.ServiceAction
		err          error
	}
	dockerLogsMsg struct {
		target, container, logs string
		err                     error
	}
	logsResultMsg struct {
		target string
		source logs.SourceKind
		res    *logs.Result
		err    error
	}
	logFollowTickMsg struct{ seq int }
	swarmProgressMsg struct {
		runID int64
		snap  swarm.Snapshot
	}
	honeyExportMsg struct {
		path string
		err  error
	}
	hiveTestMsg struct{ res HiveTestResult }
)

// wizardForm holds in-progress fields for adding or editing a Hive (#12).
type wizardForm struct {
	active   bool
	editing  string // empty when adding a new Hive, original name when editing
	fieldIdx int    // 0:Name, 1:Type, 2:Host, 3:User, 4:Port, 5:Label
	fields   [6]string
}

var wizardFieldLabels = [6]string{
	"Hive display name",
	"Type (local or ssh)",
	"SSH hostname / alias",
	"SSH user (optional)",
	"SSH port (optional)",
	"Label (optional)",
}

// Model is the root Bubble Tea model.
type Model struct {
	sections  []Section
	cursor    int
	width     int
	height    int
	inventory *agents.Inventory
	scan      ScanFunc
	ops       Ops
	scanning  bool
	quitting  bool

	// Navigation & selection state
	screen       Screen
	prevScreen   Screen
	hiveIdx      int
	beeCursor    int
	selectedBee  *agents.Bee
	filterQuery  string
	input        inputMode
	inputBuffer  string
	statusBanner string
	busy         bool

	// Bee detail screen (#4)
	detailCursor int
	detailOutput string

	// Comb / Skills screen (#5)
	skillList        *openclaw.SkillListReport
	skillCheck       *openclaw.SkillCheckReport
	skillCursor      int
	skillFilterQuery string
	showRawComb      bool

	// Self-Review, Workshop, & Swarm Review (#6, #10)
	selfReview     *openclaw.SelfReviewReport
	workshopList   *openclaw.WorkshopListReport
	activeProposal *openclaw.WorkshopProposal
	proposalCursor int
	swarmCoord     *swarm.Coordinator
	swarmSnap      *swarm.Snapshot
	swarmStream    *swarmStream
	swarmRunID     int64
	swarmCursor    int

	// Troubleshooting / Hive Health & Honey Report (#7, #11)
	healthReport *diagnostics.HiveHealthReport
	healthCursor int
	sortSeverity bool
	exportPath   string

	// Services & Model Troubleshooting (#8)
	servicesTab    int // 0: systemd, 1: Docker, 2: Models & Ollama
	servicesReport *services.HiveServicesReport
	serviceCursor  int
	selectedUnit   *services.SystemdUnit
	dockerLogText  string
	pendingAction  services.ServiceAction
	pendingUnit    string

	// Unified Log Browser (#9)
	logSourceIdx int
	logTarget    string
	logFilter    logs.Filter
	logResult    *logs.Result
	logScroll    int
	logFollowing bool
	logFollowSeq int
	showLogCmd   bool

	// Hive Configuration Wizard (#12)
	cfg          config.Config
	settingsIdx  int
	wizard       wizardForm
	confirmDel   string
	lastHiveTest *HiveTestResult

	// Accessibility & terminal adaptation (v0.5)
	noColor bool
}

type swarmStream struct {
	runID  int64
	ch     chan swarm.Snapshot
	mu     sync.Mutex
	closed bool
}

func newSwarmStream(runID int64, capacity int) *swarmStream {
	if capacity < 1 {
		capacity = 1
	}
	return &swarmStream{runID: runID, ch: make(chan swarm.Snapshot, capacity)}
}

func (s *swarmStream) send(snap swarm.Snapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.ch <- snap:
	default:
	}
}

func (s *swarmStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	close(s.ch)
}

func (s *swarmStream) wait() tea.Cmd {
	if s == nil {
		return nil
	}
	return func() tea.Msg {
		snap, ok := <-s.ch
		if !ok {
			return nil
		}
		return swarmProgressMsg{runID: s.runID, snap: snap}
	}
}

// New returns a root model with the default navigation.
func New(opts ...Option) Model {
	m := Model{
		sections:  DefaultSections,
		inventory: agents.NewInventory(),
		cfg:       config.Default(),
		noColor:   os.Getenv("NO_COLOR") != "" || strings.EqualFold(os.Getenv("TERM"), "dumb"),
	}
	for _, opt := range opts {
		opt(&m)
	}
	if m.ops != nil {
		m.cfg = m.ops.GetConfig()
	}
	m.scanning = m.scan != nil
	return m
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	if m.scan == nil {
		return nil
	}
	return m.scanCmd()
}

func (m Model) scanCmd() tea.Cmd {
	scan := m.scan
	return func() tea.Msg { return inventoryMsg{inv: scan(context.Background())} }
}

func (m Model) activeBeeKey() string {
	if m.selectedBee != nil {
		return m.selectedBee.Key()
	}
	all := m.inventory.AllBees()
	if len(all) > 0 {
		return all[0].Key()
	}
	return ""
}

func (m Model) matchesActiveBee(target string) bool {
	return target == "" || target == m.activeBeeKey()
}

func (m Model) matchesActiveHive(target string) bool {
	return target == "" || target == m.activeHive()
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case inventoryMsg:
		m.scanning = false
		if msg.inv != nil {
			m.inventory = msg.inv
			m.syncSelectionAfterScan()
		}
		return m, nil

	case skillListMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Skills list failed: %v", msg.err)
		} else {
			m.skillList = msg.rep
			m.skillCursor = 0
			m.statusBanner = fmt.Sprintf("Loaded %d skills", len(msg.rep.Skills))
		}
		return m, nil

	case skillCheckMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Skill check failed: %v", msg.err)
		} else {
			m.skillCheck = msg.rep
			m.statusBanner = fmt.Sprintf("Skill check complete: %d ready, %d missing requirements",
				msg.rep.Summary.Eligible, msg.rep.Summary.MissingRequirements)
		}
		return m, nil

	case selfReviewMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Self-review failed: %v", msg.err)
		} else {
			m.selfReview = msg.rep
			if len(msg.rep.Proposals) > 0 && m.workshopList == nil {
				m.workshopList = &openclaw.WorkshopListReport{
					AgentID:   msg.rep.AgentID,
					Proposals: msg.rep.Proposals,
				}
			}
			m.statusBanner = "Self-review completed (proposals are read-only; none auto-applied)"
		}
		return m, nil

	case workshopListMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Workshop query failed: %v", msg.err)
			m.detailOutput = m.statusBanner
		} else {
			m.workshopList = msg.rep
			m.proposalCursor = 0
			m.statusBanner = fmt.Sprintf("Workshop (%s): %d proposal(s)/learned skill(s)",
				firstOr(msg.rep.Mode, "auto"), len(msg.rep.Proposals))
			m.detailOutput = fmt.Sprintf("Curator mode: %s\nWorkshop root: %s\nLearned/proposed skills: %d",
				firstOr(msg.rep.Mode, "auto"), firstOr(msg.rep.Root, "default"), len(msg.rep.Proposals))
		}
		return m, nil

	case workshopShowMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Proposal inspect failed: %v", msg.err)
		} else {
			m.activeProposal = msg.prop
			m.statusBanner = "Loaded proposal " + msg.prop.Name
		}
		return m, nil

	case modelStatusMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Model status failed: %v", msg.err)
			m.detailOutput = m.statusBanner
		} else {
			rep := msg.rep
			var lines []string
			lines = append(lines, fmt.Sprintf("Default model: %s", firstOr(rep.DefaultModel, "none")))
			if len(rep.Allowed) > 0 {
				lines = append(lines, "Allowed: "+strings.Join(rep.Allowed, ", "))
			}
			if len(rep.MissingProvidersInUse) > 0 {
				lines = append(lines, "Missing providers: "+strings.Join(rep.MissingProvidersInUse, ", "))
			}
			if len(rep.ModelRouteIssues) > 0 {
				lines = append(lines, "Route issues: "+strings.Join(rep.ModelRouteIssues, "; "))
			}
			m.detailOutput = strings.Join(lines, "\n")
			m.statusBanner = "Model status loaded"
		}
		return m, nil

	case instructionMsg:
		if !m.matchesActiveBee(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Instruction failed: %v", msg.err)
			m.detailOutput = m.statusBanner
		} else {
			m.detailOutput = "Reply:\n" + msg.reply
			m.statusBanner = "Custom instruction delivered"
		}
		return m, nil

	case hiveHealthMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Hive Health failed: %v", msg.err)
		} else {
			m.healthReport = msg.rep
			m.healthCursor = 0
			p, w, f, u := msg.rep.Counts()
			m.statusBanner = fmt.Sprintf("Hive Health (%s): %d pass, %d warn, %d fail, %d unavailable",
				msg.rep.Hive, p, w, f, u)
		}
		return m, nil

	case servicesMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Service inspection failed: %v", msg.err)
		} else {
			m.servicesReport = msg.rep
			m.serviceCursor = 0
			m.statusBanner = fmt.Sprintf("Inspected systemd, Docker, and models on %s", msg.rep.Hive)
		}
		return m, nil

	case unitDetailMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Unit inspection failed: %v", msg.err)
		} else {
			u := msg.unit
			m.selectedUnit = &u
			m.statusBanner = fmt.Sprintf("Loaded unit details for %s (enabled=%s)", u.Name, firstOr(u.EnabledState, "unknown"))
		}
		return m, nil

	case serviceControlMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Service %s on %s failed: %v", msg.action, msg.unit, msg.err)
		} else {
			m.statusBanner = fmt.Sprintf("Confirmed and executed: systemctl %s %s", msg.action, msg.unit)
			return m, m.inspectServicesCmd()
		}
		return m, nil

	case dockerLogsMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Docker logs failed: %v", msg.err)
		} else {
			m.dockerLogText = msg.logs
			m.statusBanner = "Loaded logs for container " + msg.container
		}
		return m, nil

	case logsResultMsg:
		if !m.matchesActiveHive(msg.target) {
			return m, nil
		}
		if msg.source != "" && msg.source != logs.AllSources[m.logSourceIdx] {
			return m, nil
		}
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Log fetch error: %v", msg.err)
		} else {
			m.logResult = msg.res
			m.statusBanner = fmt.Sprintf("Loaded %d lines (%s)", len(msg.res.Entries), msg.res.Command)
		}
		if m.logFollowing && m.screen == screenLogs {
			seq := m.logFollowSeq
			return m, tea.Tick(1500*time.Millisecond, func(time.Time) tea.Msg {
				return logFollowTickMsg{seq: seq}
			})
		}
		return m, nil

	case logFollowTickMsg:
		if m.logFollowing && m.screen == screenLogs && msg.seq == m.logFollowSeq {
			return m, m.fetchLogsCmd()
		}
		return m, nil

	case swarmProgressMsg:
		if msg.runID != 0 && msg.runID != m.swarmRunID {
			return m, nil
		}
		if m.swarmSnap != nil && m.swarmSnap.Done && !msg.snap.Done {
			return m, nil
		}
		snap := msg.snap
		m.swarmSnap = &snap
		m.busy = !snap.Done
		if snap.Done {
			m.swarmStream = nil
			m.statusBanner = fmt.Sprintf("Swarm Review finished: %d complete, %d failed, %d skipped, %d proposals",
				snap.Summary.Completed, snap.Summary.Failed, snap.Summary.Skipped, snap.Summary.TotalProposals)
			return m, nil
		}
		if m.swarmStream != nil {
			return m, m.swarmStream.wait()
		}
		return m, nil

	case honeyExportMsg:
		m.busy = false
		if msg.err != nil {
			m.statusBanner = fmt.Sprintf("Honey Report export failed: %v", msg.err)
		} else {
			m.exportPath = msg.path
			m.statusBanner = "Exported Honey Report to " + msg.path
		}
		return m, nil

	case hiveTestMsg:
		m.busy = false
		res := msg.res
		m.lastHiveTest = &res
		if !res.Reachable {
			m.statusBanner = fmt.Sprintf("Test %s: UNREACHABLE (%s)", res.Hive, res.Err)
		} else if !res.OpenClawPresent {
			m.statusBanner = fmt.Sprintf("Test %s: SSH/host reachable, but OpenClaw missing (%s)", res.Hive, res.Err)
		} else {
			m.statusBanner = fmt.Sprintf("Test %s: Reachable + %s", res.Hive, res.OpenClawVersion)
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.input != inputNone {
			return m.handleTextInput(msg)
		}
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleTextInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC:
		m.quitting = true
		return m, tea.Quit
	case tea.KeyEsc:
		mode := m.input
		m.input = inputNone
		m.inputBuffer = ""
		if mode == inputWizardField {
			m.wizard.active = false
		}
		return m, nil
	case tea.KeyEnter:
		val := strings.TrimSpace(m.inputBuffer)
		mode := m.input
		m.input = inputNone
		m.inputBuffer = ""
		switch mode {
		case inputFilterBees:
			m.filterQuery = val
			m.beeCursor = 0
		case inputFilterSkills:
			m.skillFilterQuery = val
			m.skillCursor = 0
		case inputInstruction:
			bee := m.activeBee()
			if bee != nil && val != "" && m.ops != nil {
				m.busy = true
				m.statusBanner = "Sending instruction to " + bee.DisplayName() + "…"
				target := *bee
				beeKey := target.Key()
				ops := m.ops
				return m, func() tea.Msg {
					reply, err := ops.SendInstruction(context.Background(), target, val)
					return instructionMsg{target: beeKey, reply: reply, err: err}
				}
			} else if val != "" {
				m.detailOutput = "Queued instruction: " + val
			}
		case inputLogSearch:
			m.logFilter.Search = val
		case inputLogTarget:
			m.logTarget = val
			return m, m.fetchLogsCmd()
		case inputWizardField:
			m.wizard.fields[m.wizard.fieldIdx] = val
		}
		return m, nil
	case tea.KeyBackspace, tea.KeyDelete:
		if len(m.inputBuffer) > 0 {
			runes := []rune(m.inputBuffer)
			m.inputBuffer = string(runes[:len(runes)-1])
		}
		if m.input == inputFilterBees {
			m.filterQuery = m.inputBuffer
			m.beeCursor = 0
		} else if m.input == inputFilterSkills {
			m.skillFilterQuery = m.inputBuffer
			m.skillCursor = 0
		}
		return m, nil
	default:
		if len(msg.Runes) > 0 {
			m.inputBuffer += string(msg.Runes)
			if m.input == inputFilterBees {
				m.filterQuery = m.inputBuffer
				m.beeCursor = 0
			} else if m.input == inputFilterSkills {
				m.skillFilterQuery = m.inputBuffer
				m.skillCursor = 0
			}
		}
		return m, nil
	}
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()
	if key == "ctrl+c" {
		m.quitting = true
		return m, tea.Quit
	}

	// Global rescan key 'r' when not intercepted by a sub-dialog.
	if key == "r" && m.pendingAction == "" && m.confirmDel == "" && !m.wizard.active {
		if m.scan != nil && !m.scanning {
			m.scanning = true
			return m, m.scanCmd()
		}
	}

	switch m.screen {
	case screenRoot:
		return m.updateRoot(key)
	case screenSwarmOverview, screenBees:
		return m.updateSwarmOrBees(key)
	case screenBeeDetail:
		return m.updateBeeDetail(key)
	case screenComb:
		return m.updateComb(key)
	case screenSelfReview:
		return m.updateSelfReview(key)
	case screenTroubleshooting:
		return m.updateTroubleshooting(key)
	case screenServices:
		return m.updateServices(key)
	case screenLogs:
		return m.updateLogs(key)
	case screenSettings:
		return m.updateSettings(key)
	}
	return m, nil
}

func (m Model) updateRoot(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "esc":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(m.sections)-1 {
			m.cursor++
		}
	case "/":
		m.screen = screenSwarmOverview
		m.input = inputFilterBees
		m.inputBuffer = m.filterQuery
	case "enter":
		return m.enterSection(m.cursor)
	}
	return m, nil
}

func (m Model) enterSection(idx int) (tea.Model, tea.Cmd) {
	m.statusBanner = ""
	switch idx {
	case 0:
		m.screen = screenSwarmOverview
		m.beeCursor = 0
		return m, nil
	case 1:
		m.screen = screenBees
		m.beeCursor = 0
		return m, nil
	case 2:
		m.screen = screenComb
		m.prevScreen = screenRoot
		return m, m.loadSkillsCmd()
	case 3:
		m.screen = screenSelfReview
		m.prevScreen = screenRoot
		return m, m.loadWorkshopCmd()
	case 4:
		m.screen = screenTroubleshooting
		m.prevScreen = screenRoot
		return m, m.runHiveHealthCmd()
	case 5:
		m.screen = screenServices
		m.prevScreen = screenRoot
		return m, m.inspectServicesCmd()
	case 6:
		m.screen = screenLogs
		m.prevScreen = screenRoot
		return m, m.fetchLogsCmd()
	case 7:
		m.screen = screenSettings
		m.prevScreen = screenRoot
		if m.ops != nil {
			m.cfg = m.ops.GetConfig()
		}
		return m, nil
	}
	return m, nil
}

func (m Model) updateSwarmOrBees(key string) (tea.Model, tea.Cmd) {
	bees := m.filteredBees()
	switch key {
	case "esc", "b":
		if m.filterQuery != "" {
			m.filterQuery = ""
			m.beeCursor = 0
			return m, nil
		}
		m.screen = screenRoot
		return m, nil
	case "q":
		m.quitting = true
		return m, tea.Quit
	case "up", "k":
		if m.beeCursor > 0 {
			m.beeCursor--
		}
	case "down", "j":
		if m.beeCursor < len(bees)-1 {
			m.beeCursor++
		}
	case "/":
		m.input = inputFilterBees
		m.inputBuffer = m.filterQuery
	case "e":
		return m, m.exportHoneyReportCmd()
	case "enter":
		if m.beeCursor >= 0 && m.beeCursor < len(bees) {
			b := bees[m.beeCursor]
			m.selectedBee = &b
			m.busy = false
			m.prevScreen = m.screen
			m.screen = screenBeeDetail
			m.detailCursor = 0
			m.detailOutput = ""
		}
	}
	return m, nil
}

func (m Model) updateBeeDetail(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "b":
		if m.prevScreen != screenRoot {
			m.screen = m.prevScreen
		} else {
			m.screen = screenSwarmOverview
		}
		return m, nil
	case "up", "k":
		if m.detailCursor > 0 {
			m.detailCursor--
		}
	case "down", "j":
		if m.detailCursor < len(BeeDetailActions)-1 {
			m.detailCursor++
		}
	case "enter":
		bee := m.activeBee()
		if bee == nil {
			return m, nil
		}
		switch m.detailCursor {
		case 0: // Agent status
			st := m.inventory.Status[bee.Hive]
			hiveState := "online"
			if !st.OK() {
				hiveState = fmt.Sprintf("offline (%v)", st.Err)
			}
			m.detailOutput = fmt.Sprintf("Agent %s on Hive %s (%s)\nModel: %s\nWorkspace: %s\nStatus: %s",
				bee.DisplayName(), bee.Hive, hiveState,
				firstOr(bee.Runtime, "default"),
				firstOr(bee.Workspace, "n/a"),
				firstOr(bee.Status, "ready"))
		case 1: // Visible skills
			m.prevScreen = screenBeeDetail
			m.screen = screenComb
			return m, m.loadSkillsCmd()
		case 2: // Skill check
			m.prevScreen = screenBeeDetail
			m.screen = screenComb
			return m, m.checkSkillsCmd()
		case 3: // Self-review skills
			m.prevScreen = screenBeeDetail
			m.screen = screenSelfReview
			return m, m.selfReviewCmd()
		case 4: // Workshop proposals
			m.prevScreen = screenBeeDetail
			m.screen = screenSelfReview
			return m, m.loadWorkshopCmd()
		case 5: // Curator / learning status
			return m, m.loadWorkshopCmd()
		case 6: // Model status
			return m, m.loadModelStatusCmd()
		case 7: // Send custom instruction
			m.input = inputInstruction
			m.inputBuffer = ""
		}
	}
	return m, nil
}

func (m Model) updateComb(key string) (tea.Model, tea.Cmd) {
	skills := m.filteredSkills()
	switch key {
	case "esc", "b":
		if m.skillFilterQuery != "" {
			m.skillFilterQuery = ""
			m.skillCursor = 0
			return m, nil
		}
		if m.prevScreen == screenBeeDetail {
			m.screen = screenBeeDetail
		} else {
			m.screen = screenRoot
		}
		return m, nil
	case "[", "]":
		m.cycleActiveBee(key == "]")
		return m, m.loadSkillsCmd()
	case "up", "k":
		if m.skillCursor > 0 {
			m.skillCursor--
		}
	case "down", "j":
		if m.skillCursor < len(skills)-1 {
			m.skillCursor++
		}
	case "/":
		m.input = inputFilterSkills
		m.inputBuffer = m.skillFilterQuery
	case "c":
		return m, m.checkSkillsCmd()
	case "l":
		return m, m.loadSkillsCmd()
	case "v":
		m.showRawComb = !m.showRawComb
	}
	return m, nil
}

func (m Model) updateSelfReview(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "b":
		if m.activeProposal != nil {
			m.activeProposal = nil
			return m, nil
		}
		if m.prevScreen == screenBeeDetail {
			m.screen = screenBeeDetail
		} else {
			m.screen = screenRoot
		}
		return m, nil
	case "[", "]":
		m.cycleActiveBee(key == "]")
		return m, m.loadWorkshopCmd()
	case "up", "k":
		if m.proposalCursor > 0 {
			m.proposalCursor--
		}
		if m.swarmCursor > 0 {
			m.swarmCursor--
		}
	case "down", "j":
		if m.workshopList != nil && m.proposalCursor < len(m.workshopList.Proposals)-1 {
			m.proposalCursor++
		}
		if m.swarmSnap != nil && m.swarmCursor < len(m.swarmSnap.Items)-1 {
			m.swarmCursor++
		}
	case "s":
		return m, m.selfReviewCmd()
	case "w":
		return m, m.loadWorkshopCmd()
	case "enter":
		if m.workshopList != nil && m.proposalCursor >= 0 && m.proposalCursor < len(m.workshopList.Proposals) {
			prop := m.workshopList.Proposals[m.proposalCursor]
			return m, m.showProposalCmd(prop.Name)
		}
	case "1":
		return m.startSwarmReview(swarm.ModeSelectedBee)
	case "2":
		return m.startSwarmReview(swarm.ModeSelectedHive)
	case "3":
		return m.startSwarmReview(swarm.ModeAllReachable)
	case "x":
		if m.swarmCoord != nil && (m.swarmSnap == nil || !m.swarmSnap.Done) {
			m.swarmCoord.Cancel()
			snap := m.swarmCoord.Snapshot()
			snap.Done = true
			m.swarmSnap = &snap
			m.swarmStream = nil
			m.busy = false
			m.statusBanner = "Swarm Review canceled; completed Bee reviews preserved"
		}
	}
	return m, nil
}

func (m Model) updateTroubleshooting(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "b":
		m.screen = screenRoot
		return m, nil
	case "[", "]":
		m.cycleActiveHive(key == "]")
		return m, m.runHiveHealthCmd()
	case "up", "k":
		if m.healthCursor > 0 {
			m.healthCursor--
		}
	case "down", "j":
		if m.healthReport != nil && m.healthCursor < len(m.healthReport.Checks)-1 {
			m.healthCursor++
		}
	case "s":
		m.sortSeverity = !m.sortSeverity
	case "h":
		return m, m.runHiveHealthCmd()
	case "e":
		return m, m.exportHoneyReportCmd()
	}
	return m, nil
}

func (m Model) updateServices(key string) (tea.Model, tea.Cmd) {
	// Handle explicit confirmation dialog for state-changing systemd actions (#8).
	if m.pendingAction != "" {
		switch key {
		case "y", "Y":
			act := m.pendingAction
			unit := m.pendingUnit
			m.pendingAction = ""
			m.pendingUnit = ""
			return m, m.controlServiceCmd(unit, act, true)
		case "n", "N", "esc":
			m.pendingAction = ""
			m.pendingUnit = ""
			m.statusBanner = "Canceled service action"
			return m, nil
		}
		return m, nil
	}

	switch key {
	case "esc", "b":
		m.screen = screenRoot
		return m, nil
	case "tab":
		m.servicesTab = (m.servicesTab + 1) % 3
		m.serviceCursor = 0
	case "[", "]":
		m.cycleActiveHive(key == "]")
		return m, m.inspectServicesCmd()
	case "up", "k":
		if m.serviceCursor > 0 {
			m.serviceCursor--
		}
	case "down", "j":
		maxIdx := m.currentServicesCount() - 1
		if m.serviceCursor < maxIdx {
			m.serviceCursor++
		}
	case "enter":
		if m.servicesReport == nil {
			return m, nil
		}
		switch m.servicesTab {
		case 0: // systemd unit details + logs
			units := m.servicesReport.Systemd.Units
			if m.serviceCursor >= 0 && m.serviceCursor < len(units) {
				return m, m.inspectUnitCmd(units[m.serviceCursor].Name)
			}
		case 1: // Docker container logs
			cs := m.servicesReport.Docker.Containers
			if m.serviceCursor >= 0 && m.serviceCursor < len(cs) {
				return m, m.fetchDockerLogsCmd(cs[m.serviceCursor].Name)
			}
		}
	case "S", "T", "R":
		if m.servicesTab == 0 && m.servicesReport != nil {
			units := m.servicesReport.Systemd.Units
			if m.serviceCursor >= 0 && m.serviceCursor < len(units) {
				m.pendingUnit = units[m.serviceCursor].Name
				switch key {
				case "S":
					m.pendingAction = services.ActionStart
				case "T":
					m.pendingAction = services.ActionStop
				case "R":
					m.pendingAction = services.ActionRestart
				}
			}
		}
	}
	return m, nil
}

func (m Model) updateLogs(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "b":
		if m.logFollowing {
			m.logFollowing = false
			m.logFollowSeq++
			m.statusBanner = "Stopped follow mode"
			return m, nil
		}
		m.screen = screenRoot
		return m, nil
	case "[", "]":
		m.cycleActiveHive(key == "]")
		return m, m.fetchLogsCmd()
	case "tab":
		m.logSourceIdx = (m.logSourceIdx + 1) % len(logs.AllSources)
		return m, m.fetchLogsCmd()
	case "t":
		m.input = inputLogTarget
		m.inputBuffer = m.logTarget
	case "/":
		m.input = inputLogSearch
		m.inputBuffer = m.logFilter.Search
	case "e":
		m.logFilter.ErrorsOnly = !m.logFilter.ErrorsOnly
		if m.logFilter.ErrorsOnly {
			m.logFilter.WarningsOnly = false
		}
	case "w":
		m.logFilter.WarningsOnly = !m.logFilter.WarningsOnly
		if m.logFilter.WarningsOnly {
			m.logFilter.ErrorsOnly = false
		}
	case "c":
		m.showLogCmd = !m.showLogCmd
	case "f":
		m.logFollowing = !m.logFollowing
		m.logFollowSeq++
		if m.logFollowing {
			m.statusBanner = "Follow mode active (press f or esc to stop)"
			return m, m.fetchLogsCmd()
		}
		m.statusBanner = "Follow mode stopped"
	case "up", "k":
		if m.logScroll > 0 {
			m.logScroll--
		}
	case "down", "j":
		m.logScroll++
	}
	return m, nil
}

func (m Model) updateSettings(key string) (tea.Model, tea.Cmd) {
	if m.confirmDel != "" {
		switch key {
		case "y", "Y":
			name := m.confirmDel
			m.confirmDel = ""
			if err := m.cfg.RemoveHive(name); err != nil {
				m.statusBanner = fmt.Sprintf("Remove failed: %v", err)
				return m, nil
			}
			return m.saveConfigAndRescan("Removed Hive " + name)
		case "n", "N", "esc":
			m.confirmDel = ""
			m.statusBanner = "Canceled Hive removal"
			return m, nil
		}
		return m, nil
	}

	if m.wizard.active {
		switch key {
		case "esc":
			m.wizard.active = false
			return m, nil
		case "up", "k":
			if m.wizard.fieldIdx > 0 {
				m.wizard.fieldIdx--
			}
		case "down", "j", "tab":
			if m.wizard.fieldIdx < len(wizardFieldLabels)-1 {
				m.wizard.fieldIdx++
			}
		case "enter":
			m.input = inputWizardField
			m.inputBuffer = m.wizard.fields[m.wizard.fieldIdx]
		case "t":
			h, err := m.wizardHive()
			if err != nil {
				m.statusBanner = fmt.Sprintf("Invalid Hive settings: %v", err)
				return m, nil
			}
			return m, m.testHiveCmd(h)
		case "s", "ctrl+s":
			h, err := m.wizardHive()
			if err != nil {
				m.statusBanner = fmt.Sprintf("Cannot save: %v", err)
				return m, nil
			}
			if m.wizard.editing == "" {
				if err := m.cfg.AddHive(h); err != nil {
					m.statusBanner = fmt.Sprintf("Cannot add Hive: %v", err)
					return m, nil
				}
			} else {
				if err := m.cfg.UpdateHive(m.wizard.editing, h); err != nil {
					m.statusBanner = fmt.Sprintf("Cannot update Hive: %v", err)
					return m, nil
				}
			}
			m.wizard.active = false
			return m.saveConfigAndRescan("Saved Hive " + h.Name)
		}
		return m, nil
	}

	switch key {
	case "esc", "b":
		m.screen = screenRoot
		return m, nil
	case "up", "k":
		if m.settingsIdx > 0 {
			m.settingsIdx--
		}
	case "down", "j":
		if m.settingsIdx < len(m.cfg.Hives)-1 {
			m.settingsIdx++
		}
	case "a":
		m.wizard = wizardForm{
			active: true,
			fields: [6]string{"", "ssh", "", "", "22", ""},
		}
	case "l":
		m.wizard = wizardForm{
			active: true,
			fields: [6]string{"local", "local", "", "", "", "Local machine"},
		}
	case "e", "enter":
		if m.settingsIdx >= 0 && m.settingsIdx < len(m.cfg.Hives) {
			h := m.cfg.Hives[m.settingsIdx]
			portStr := ""
			if h.Port > 0 {
				portStr = strconv.Itoa(h.Port)
			}
			m.wizard = wizardForm{
				active:  true,
				editing: h.Name,
				fields:  [6]string{h.Name, string(h.Kind()), h.Host, h.User, portStr, h.Label},
			}
		}
	case "d":
		if m.settingsIdx >= 0 && m.settingsIdx < len(m.cfg.Hives) {
			m.confirmDel = m.cfg.Hives[m.settingsIdx].Name
		}
	case "t":
		if m.settingsIdx >= 0 && m.settingsIdx < len(m.cfg.Hives) {
			return m, m.testHiveCmd(m.cfg.Hives[m.settingsIdx])
		}
	}
	return m, nil
}

func (m Model) wizardHive() (config.Hive, error) {
	port := 0
	if pStr := strings.TrimSpace(m.wizard.fields[4]); pStr != "" {
		p, err := strconv.Atoi(pStr)
		if err != nil {
			return config.Hive{}, fmt.Errorf("invalid port %q", pStr)
		}
		port = p
	}
	h := config.Hive{
		Name:  strings.TrimSpace(m.wizard.fields[0]),
		Type:  config.HiveType(strings.ToLower(strings.TrimSpace(m.wizard.fields[1]))),
		Host:  strings.TrimSpace(m.wizard.fields[2]),
		User:  strings.TrimSpace(m.wizard.fields[3]),
		Port:  port,
		Label: strings.TrimSpace(m.wizard.fields[5]),
	}
	return h, h.Validate()
}

func (m Model) saveConfigAndRescan(msg string) (tea.Model, tea.Cmd) {
	if m.ops != nil {
		if err := m.ops.SaveConfig(m.cfg); err != nil {
			m.statusBanner = fmt.Sprintf("Save config failed: %v", err)
			return m, nil
		}
	}
	m.statusBanner = msg + " (rescanning Hives…)"
	if m.scan != nil && !m.scanning {
		m.scanning = true
		return m, m.scanCmd()
	}
	return m, nil
}
