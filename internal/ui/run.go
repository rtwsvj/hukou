// Package ui implements the full-screen interactive menu opened by a bare
// `hukou` invocation (docs/specs/interactive-menu.md). Rendering is plain
// monochrome text by design (ADR-0009): no colors, borders, or styling
// libraries. bubbletea is confined to this package; the model only
// navigates, selects, and quits.
package ui

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// ErrQuit is returned by Run when the user leaves the menu via q, Esc, or
// ctrl+c without selecting an item.
var ErrQuit = errors.New("menu quit")

// Item is one entry of the interactive menu.
type Item struct {
	// Key is the menu key reported on selection, e.g. "1", "2", "q".
	Key string
	// Label is the localized display label.
	Label string
	// Run is executed when the item is selected. Run is NOT invoked by
	// ui.Run: the caller executes it (typically wrapped in
	// tea.ExecProcess) so external output streams naturally and the menu
	// redraws afterwards.
	Run func(stdout, stderr io.Writer) error
}

// Chrome strings rendered directly by this package. They are routed through
// i18n so the central catalog pass can localize them; missing entries fall
// back to the English originals.
const (
	selectHint = "↑/↓ move"
	execHint   = "Enter select"
	quitHint   = "q quit"
)

// model holds all menu state. It never executes items.
type model struct {
	title  string
	items  []Item
	cursor int
	notice string // last message line, e.g. after returning from an action
	choice string // Key of the selected item; empty until Enter
	quit   bool   // set when the user quits via q/Esc/ctrl+c
}

func newModel(title string, items []Item, notice string) model {
	return model{title: title, items: items, notice: notice}
}

// Init implements tea.Model.
func (m model) Init() tea.Cmd { return nil }

// Update implements tea.Model. Navigation keys move the cursor with wrap at
// both ends; Enter records the selection; q/Esc/ctrl+c record the quit.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	key, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch key.String() {
	case "up", "k":
		m.cursor = (m.cursor - 1 + len(m.items)) % len(m.items)
	case "down", "j":
		m.cursor = (m.cursor + 1) % len(m.items)
	case "enter":
		m.choice = m.items[m.cursor].Key
		return m, tea.Quit
	case "q", "esc", "ctrl+c":
		m.quit = true
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model with plain monochrome rendering:
//
//	title
//
//	  [1] first label
//	> [2] second label
//
//	↑/↓ move · Enter select · q quit
func (m model) View() string {
	var b strings.Builder
	b.WriteString(m.title)
	b.WriteString("\n\n")
	for i, item := range m.items {
		if i == m.cursor {
			b.WriteString("> ")
		} else {
			b.WriteString("  ")
		}
		fmt.Fprintf(&b, "[%s] %s\n", item.Key, item.Label)
	}
	if m.notice != "" {
		b.WriteString("\n")
		b.WriteString(m.notice)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(i18n.T(selectHint) + " · " + i18n.T(execHint) + " · " + i18n.T(quitHint))
	return b.String()
}

// outcome maps final model state to the Run contract.
func (m model) outcome() (string, error) {
	if m.quit {
		return "", ErrQuit
	}
	return m.choice, nil
}

// Run starts the interactive menu and blocks until the user selects an item
// or quits. It is SINGLE-PASS: it returns the selected item's key as soon as
// Enter is pressed (the caller executes Item.Run itself — see Item.Run), or
// ("", ErrQuit) on q/Esc/ctrl+c. The menu loop lives in the caller, which
// re-invokes Run after each action completes (passing any result message as
// notice to surface it above the help footer).
//
// A non-TTY stdin fails closed before the terminal program starts. The
// check is a real terminal ioctl, not ModeCharDevice: /dev/null is a
// character device too, and handing it to a raw-mode reader hangs the menu
// (EOF arrives without a quit event).
func Run(stdin io.Reader, stdout, stderr io.Writer, title string, items []Item) (selected string, err error) {
	if len(items) == 0 {
		return "", fmt.Errorf("ui: %s", i18n.T("menu has no items"))
	}
	f, ok := stdin.(*os.File)
	if !ok || !isTTY(f) {
		return "", fmt.Errorf("ui: %s", i18n.T("interactive menu requires a terminal (TTY) on stdin"))
	}
	p := tea.NewProgram(newModel(title, items, ""), tea.WithInput(stdin), tea.WithOutput(stdout))
	final, err := p.Run()
	if err != nil {
		return "", fmt.Errorf("ui: %w", err)
	}
	return final.(model).outcome()
}

// isTTY reports whether f refers to an interactive terminal. A plain
// ModeCharDevice check is not enough — /dev/null is a character device too,
// and feeding it to a raw-mode reader hangs the menu (EOF arrives without a
// quit event).
func isTTY(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}
