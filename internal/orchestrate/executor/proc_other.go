//go:build !unix

package executor

import (
	"os/exec"
	"time"
)

// setupProcessControl is the non-POSIX fallback: no process groups, so context
// cancellation keeps exec.CommandContext's default behavior of killing the
// direct child only. afterReap is a no-op.
func setupProcessControl(_ *exec.Cmd, _ time.Duration) (afterReap func()) {
	return func() {}
}
