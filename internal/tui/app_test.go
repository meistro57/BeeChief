package tui

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/meistro57/beechief/internal/agents"
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
