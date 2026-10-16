// Package app wires BeeChief's domain packages to the TUI.
//
// It owns startup and shutdown; it contains no rendering and no shell logic.
package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/tui"
)

// Run starts the BeeChief terminal UI.
//
// A fresh install with no configured Hives must still start cleanly and
// present the menu.
func Run() error {
	p := tea.NewProgram(tui.New(), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
