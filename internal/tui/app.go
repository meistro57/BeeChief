// Package tui renders BeeChief's terminal interface.
//
// It consumes plain Go data and must not contain shell-command logic or
// OpenClaw specifics (roadmap: "Separate UI from operations").
package tui

import (
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
)

// Model is the root Bubble Tea model.
type Model struct {
	sections  []Section
	cursor    int
	width     int
	height    int
	inventory *agents.Inventory
	quitting  bool
}

// New returns a root model with the default placeholder navigation.
func New() Model {
	return Model{
		sections:  DefaultSections,
		inventory: agents.NewInventory(),
	}
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
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
	b.WriteString(fmt.Sprintf("Bees discovered: %d\n\n", m.inventory.Total()))

	for i, s := range m.sections {
		if i == m.cursor {
			b.WriteString(cursorStyle.Render("> " + s.Title))
		} else {
			b.WriteString(plainStyle.Render("  " + s.Title))
		}
		b.WriteString("\n")
	}

	b.WriteString("\n")
	b.WriteString(helpStyle.Render("↑/↓ move · enter select · q quit"))
	return b.String()
}
