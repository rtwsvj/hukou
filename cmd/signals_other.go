//go:build !unix

package cmd

import "os"

// interruptSignals are the signals that cancel a running `hukou up`. Off unix we
// portably watch only os.Interrupt.
func interruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
