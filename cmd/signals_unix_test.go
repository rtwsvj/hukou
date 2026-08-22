//go:build unix

package cmd

import (
	"os"
	"syscall"
	"testing"
)

// TestInterruptSignalsIncludeSIGHUP: closing the terminal (or a multiplexer
// detaching) raises SIGHUP, and a running `hukou up` must treat it like
// Ctrl-C — cancelling the NotifyContext run context so the executor's Cancel
// hook terminates each manager's whole process group instead of orphaning it.
func TestInterruptSignalsIncludeSIGHUP(t *testing.T) {
	want := map[os.Signal]bool{
		os.Interrupt:    false,
		syscall.SIGTERM: false,
		syscall.SIGHUP:  false,
	}
	for _, got := range interruptSignals() {
		if _, ok := want[got]; !ok {
			t.Fatalf("interruptSignals() returned unexpected signal %v", got)
		}
		want[got] = true
	}
	for sig, seen := range want {
		if !seen {
			t.Fatalf("interruptSignals() = %v is missing %v; closing the terminal would orphan a running up", interruptSignals(), sig)
		}
	}
}
