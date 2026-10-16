package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
