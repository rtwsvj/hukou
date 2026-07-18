//go:build unix

package executor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// setupProcessControl places the child in its own process group and replaces
// the default context-cancel kill with a group-wide two-phase protocol: on
// cancellation/timeout the whole group first receives SIGTERM (letting managers
// and their children exit cleanly), then SIGKILL after termGrace. Managers
// routinely spawn helpers (git, curl, compilers); killing only the direct child
// would orphan those grandchildren, keep them running, and possibly wedge the
// output pipes.
//
// The returned finishKill must be called after cmd.Wait returns: if the
// escalation timer is still pending it is stopped and the group SIGKILL is
// delivered immediately instead, so a TERM-ignoring grandchild is dead by the
// time RunManager returns (and no timer outlives the call). While any group
// member survives, the pgid cannot be recycled, so the signal stays targeted.
func setupProcessControl(cmd *exec.Cmd, termGrace time.Duration) (finishKill func()) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	var mu sync.Mutex
	var killTimer *time.Timer
	var pgid int

	cmd.Cancel = func() error {
		p := cmd.Process
		if p == nil {
			return os.ErrProcessDone
		}
		mu.Lock()
		pgid = p.Pid // with Setpgid the child leads a group of its own pid
		termErr := syscall.Kill(-pgid, syscall.SIGTERM)
		killTimer = time.AfterFunc(termGrace, func() {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		})
		mu.Unlock()
		if termErr != nil {
			// The group signal failed (group already gone, or an exotic state);
			// fall back to killing the direct child so cancellation is never lost.
			return p.Kill()
		}
		return nil
	}

	return func() {
		mu.Lock()
		defer mu.Unlock()
		if killTimer != nil && killTimer.Stop() {
			// Wait returned before the escalation deadline: deliver the group
			// SIGKILL now so TERM-surviving descendants die with the manager.
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}
}
