// Package app wires BeeChief's domain packages to the TUI.
//
// It owns startup and shutdown; it contains no rendering and no shell logic.
package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/agents"
	"github.com/meistro57/beechief/internal/config"
	"github.com/meistro57/beechief/internal/discovery"
	"github.com/meistro57/beechief/internal/tui"
)

// Run starts the BeeChief terminal UI.
//
// A fresh install with no configured Hives must still start cleanly and
// present the menu. A config file that exists but is invalid is an error.
func Run() error {
	cfg, err := config.LoadOrDefault(config.DefaultPath())
	if err != nil {
		return err
	}
	scanner := discovery.FromConfig(cfg)
	scan := func(ctx context.Context) *agents.Inventory { return scanner.Rescan(ctx) }

	p := tea.NewProgram(tui.New(tui.WithScanner(scan)), tea.WithAltScreen())
	_, err = p.Run()
	return err
}
