package ui

import (
	"os"

	"github.com/charmbracelet/x/term"
)

// IsTerminal reports whether the given file descriptor is attached to an
// interactive terminal. os.ModeCharDevice alone is not sufficient: character
// devices include /dev/null, which passes a naive check but hangs any raw-mode
// reader (bubbletea receives EOF and stops without delivering a quit event).
// The x/term ioctl check distinguishes real terminals from all other
// character devices.
func IsTerminal(f *os.File) bool {
	if f == nil {
		return false
	}
	return term.IsTerminal(f.Fd())
}
