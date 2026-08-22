//go:build unix

package cmd

import (
	"os"
	"syscall"
)

// interruptSignals are the signals that cancel a running `hukou up`: SIGINT
// (terminal Ctrl-C), SIGTERM (orchestrators, `kill`), and SIGHUP (terminal
// closed or multiplexer detached). Each cancels the NotifyContext run context,
// which drives the executor's group terminate + snapshot fallthrough.
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM, syscall.SIGHUP}
}
