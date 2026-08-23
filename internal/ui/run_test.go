package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/rtwsvj/hukou/internal/i18n"
)

func testItems() []Item {
	return []Item{
		{Key: "1", Label: "first"},
		{Key: "2", Label: "second"},
		{Key: "3", Label: "third"},
	}
}

func keyPress(t *testing.T, s string) tea.Msg {
	t.Helper()
	k := tea.KeyMsg{}
	switch s {
	case "up":
		k = tea.KeyMsg{Type: tea.KeyUp}
	case "down":
		k = tea.KeyMsg{Type: tea.KeyDown}
	case "enter":
		k = tea.KeyMsg{Type: tea.KeyEnter}
	case "esc":
		k = tea.KeyMsg{Type: tea.KeyEsc}
	case "ctrl+c":
		k = tea.KeyMsg{Type: tea.KeyCtrlC}
	default:
		for _, r := range s {
			k = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
		}
	}
	return k
}

// send feeds one key to the model and returns the updated concrete model.
func send(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func TestNavigationWrapsAtEnds(t *testing.T) {
	m := newModel("title", testItems(), "")

	// up from index 0 wraps to the last item.
	if m = send(m, keyPress(t, "up")); m.cursor != 2 {
		t.Fatalf("up from 0: cursor = %d, want 2 (wrapped to last)", m.cursor)
	}

	// down wraps back to 0.
	if m = send(m, keyPress(t, "down")); m.cursor != 0 {
		t.Fatalf("down from last: cursor = %d, want 0", m.cursor)
	}

	// down twice reaches the end.
	m = send(m, keyPress(t, "down"))
	m = send(m, keyPress(t, "down"))
	if m.cursor != 2 {
		t.Fatalf("two downs: cursor = %d, want 2", m.cursor)
	}

	// down again wraps to 0.
	if m = send(m, keyPress(t, "down")); m.cursor != 0 {
		t.Fatalf("down past last: cursor = %d, want 0", m.cursor)
	}

	// j/k aliases behave identically.
	m = newModel("title", testItems(), "")
	m = send(m, keyPress(t, "j"))
	m = send(m, keyPress(t, "k"))
	if m.cursor != 0 {
		t.Fatalf("j then k: cursor = %d, want 0", m.cursor)
	}
	if m = send(m, keyPress(t, "k")); m.cursor != 2 {
		t.Fatalf("k from 0: cursor = %d, want 2 (wrapped)", m.cursor)
	}
}

func TestEnterReturnsSelectedKey(t *testing.T) {
	m := newModel("title", testItems(), "")
	m = send(m, keyPress(t, "down")) // cursor on item 2
	got, cmd := m.Update(keyPress(t, "enter"))
	if cmd == nil {
		t.Fatal("enter must produce a quit command")
	}
	sel, err := got.(model).outcome()
	if err != nil {
		t.Fatalf("outcome: unexpected error %v", err)
	}
	if sel != "2" {
		t.Fatalf("selected = %q, want %q", sel, "2")
	}
}

func TestQuitKeysReturnErrQuit(t *testing.T) {
	for _, key := range []string{"q", "esc", "ctrl+c"} {
		m := newModel("title", testItems(), "")
		got, cmd := m.Update(keyPress(t, key))
		if cmd == nil {
			t.Fatalf("%s: expected quit command", key)
		}
		sel, err := got.(model).outcome()
		if !errors.Is(err, ErrQuit) {
			t.Fatalf("%s: outcome error = %v, want ErrQuit", key, err)
		}
		if sel != "" {
			t.Fatalf("%s: selected = %q, want empty", key, sel)
		}
	}
}

func TestViewContainsLabelsAndCursorMarker(t *testing.T) {
	m := newModel("Menu title", testItems(), "done: ok")
	m.cursor = 1
	view := m.View()
	for _, label := range []string{"[1] first", "[2] second", "[3] third"} {
		if !strings.Contains(view, label) {
			t.Errorf("View missing %q in:\n%s", label, view)
		}
	}
	if !strings.Contains(view, "> [2] second") {
		t.Errorf("View missing cursor marker on active row:\n%s", view)
	}
	if strings.Count(view, "> ") != 1 {
		t.Errorf("View shows %d cursor markers, want exactly 1", strings.Count(view, "> "))
	}
	for _, hint := range []string{i18n.T(selectHint), i18n.T(execHint), i18n.T(quitHint)} {
		if !strings.Contains(view, hint) {
			t.Errorf("View missing help hint %q:\n%s", hint, view)
		}
	}
	if !strings.Contains(view, "done: ok") {
		t.Errorf("View missing notice line:\n%s", view)
	}
	if strings.Contains(view, "\x1b[") {
		t.Errorf("View contains ANSI escape sequences; rendering must be plain monochrome:\n%q", view)
	}
}

func TestRunRejectsEmptyItems(t *testing.T) {
	var out bytes.Buffer
	_, err := Run(bytes.NewReader(nil), &out, &out, "title", nil)
	if err == nil {
		t.Fatal("Run with zero items must fail upfront")
	}
}

func TestRunRejectsNonTTYStdin(t *testing.T) {
	var out bytes.Buffer
	_, err := Run(bytes.NewReader([]byte("\n")), &out, &out, "title", testItems())
	if err == nil {
		t.Fatal("Run with non-TTY stdin must fail closed before starting the program")
	}
}
