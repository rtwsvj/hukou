//go:build unix

package executor

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// termKillGrace is the grace period between SIGTERM and SIGKILL to the
// manager's process group on cancellation or timeout: long enough for a
// manager to shut down cleanly, short enough that a hung run still dies
// promptly.
const termKillGrace = 2 * time.Second

// configureProc places the manager command in its own process group and arms
// CommandContext's Cancel hook with group-wide termination, so a timeout or
// an interrupt (signal.NotifyContext cancelling the run context) kills the
// manager AND its descendants — brew's curl, say — instead of orphaning them.
func configureProc(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return terminateProcGroup(cmd.Process.Pid)
	}
}

// terminateProcGroup sends SIGTERM to the manager's whole process group, then
// escalates to SIGKILL after termKillGrace. A group that is already gone
// (ESRCH) is success, not an error; the escalation timer ignores errors for
// the same reason.
func terminateProcGroup(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	time.AfterFunc(termKillGrace, func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})
	return nil
}
