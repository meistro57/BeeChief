// Package tui renders BeeChief's terminal interface.
//
// It consumes plain Go data and must not contain shell-command logic or
// OpenClaw specifics (roadmap: "Separate UI from operations").
package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/meistro57/beechief/internal/agents"
)

// Section is one navigable area of the main menu.
type Section struct {
	Title string
	Blurb string
}

// DefaultSections returns the placeholder navigation defined by issue #1.
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

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220"))
	helpStyle   = lipgloss.NewStyle().Faint(true)
	cursorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	plainStyle  = lipgloss.NewStyle()
	okStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	failStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
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

// inventoryMsg delivers a completed scan to Update.
type inventoryMsg struct{ inv *agents.Inventory }

// Model is the root Bubble Tea model.
type Model struct {
	sections  []Section
	cursor    int
	width     int
	height    int
	inventory *agents.Inventory
	scan      ScanFunc
	scanning  bool
	quitting  bool
}

// New returns a root model with the default placeholder navigation.
func New(opts ...Option) Model {
	m := Model{
		sections:  DefaultSections,
		inventory: agents.NewInventory(),
	}
	for _, opt := range opts {
		opt(&m)
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

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case inventoryMsg:
		m.scanning = false
		if msg.inv != nil {
			m.inventory = msg.inv
		}
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
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
		case "r":
			if m.scan != nil && !m.scanning {
				m.scanning = true
				return m, m.scanCmd()
			}
		case "enter":
			// Navigation targets are implemented by later issues (#4+).
			// Enter is intentionally a no-op for now and must not panic.
		}
	}
	return m, nil
}

// View implements tea.Model.
func (m Model) View() string {
	if m.quitting {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("BeeChief 🐝"))
	b.WriteString("\n")
	b.WriteString(fmt.Sprintf("Bees discovered: %d\n", m.inventory.Total()))
	b.WriteString(m.hiveSummary())
	b.WriteString("\n")

	for i, s := range m.sections {
		if i == m.cursor {
			b.WriteString(cursorStyle.Render("> " + s.Title))
		} else {
			b.WriteString(plainStyle.Render("  " + s.Title))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move · enter select · r rescan · q quit"))
	return b.String()
}

// hiveSummary renders one line per Hive, marking failed Hives visibly.
func (m Model) hiveSummary() string {
	var b strings.Builder
	if m.scanning {
		b.WriteString(helpStyle.Render("Scanning hives…"))
		b.WriteString("\n")
	}
	for _, h := range m.inventory.Hives {
		st := m.inventory.Status[h]
		if st.OK() {
			b.WriteString(okStyle.Render(fmt.Sprintf("  ✓ %s: %d bees", h, len(m.inventory.ByHive[h]))))
		} else {
			b.WriteString(failStyle.Render(fmt.Sprintf("  ✗ %s: %v", h, st.Err)))
		}
		b.WriteString("\n")
	}
	return b.String()
}
