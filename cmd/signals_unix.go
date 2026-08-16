//go:build unix

package cmd

import (
	"os"
	"syscall"
)

// interruptSignals are the signals that cancel a running `hukou up`. On unix we
// watch both SIGINT (terminal Ctrl-C) and SIGTERM (orchestrators, `kill`).
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
