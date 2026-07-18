//go:build unix

package executor

import (
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// killState serializes every group-signal decision for one manager subprocess
// between the escalation timer callback and the Wait-return (reap) path. The
// invariant that closes the timer/PGID race: once the direct child has been
// reaped, its pid (== the pgid, because of Setpgid) may in principle be
// recycled by the kernel, so NO code path signals -pgid after reaped is set.
// The callback and the reap path share one mutex; the callback checks reaped
// under the lock before signaling, and the reap path sets reaped under the
// same lock before stopping the timer.
type killState struct {
	mu     sync.Mutex
	pgid   int
	reaped bool // cmd.Wait returned; -pgid must never be signaled again
	timer  *time.Timer
}

// setupProcessControl places the child in its own process group and replaces
// the default context-cancel kill with a group-wide two-phase protocol: on
// cancellation/timeout the whole group first receives SIGTERM (letting
// managers and their children exit cleanly), then SIGKILL after termGrace.
// Managers routinely spawn helpers (git, curl, compilers); killing only the
// direct child would orphan those grandchildren.
//
// The returned afterReap MUST be called after cmd.Wait returns: under the
// shared lock it marks the child reaped and stops a still-pending escalation
// timer, so a late-firing callback can never signal a possibly-recycled pgid.
//
// Honest boundary of this protocol: signals are only ever sent while the
// direct child is un-reaped, which is what makes them safe (an un-reaped
// child — alive or zombie — pins its pid, so the pgid cannot be recycled).
// The cost is a corner case: a grandchild that ignores SIGTERM AND holds no
// reference to the inherited output pipes can outlive the manager if the
// direct child exits before the escalation deadline — Wait then returns
// early and the escalation is cancelled. Signaling after reap to cover that
// corner would reintroduce the recycled-pgid mis-kill risk; we choose the
// mis-kill-free side. (In practice manager helpers inherit stdout/stderr,
// which keeps Wait draining until WaitDelay and lets the pre-reap escalation
// fire; TestRunManager_TimeoutKillsWholeProcessTree exercises exactly that.)
func setupProcessControl(cmd *exec.Cmd, termGrace time.Duration) (afterReap func()) {
	st := &killState{}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	cmd.Cancel = func() error {
		p := cmd.Process
		if p == nil {
			return os.ErrProcessDone
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.reaped {
			// Cancel racing a normal exit: Wait already returned, nothing to
			// signal, and signaling would be unsafe.
			return os.ErrProcessDone
		}
		st.pgid = p.Pid // with Setpgid the child leads a group of its own pid
		termErr := syscall.Kill(-st.pgid, syscall.SIGTERM)
		st.timer = time.AfterFunc(termGrace, func() {
			st.mu.Lock()
			defer st.mu.Unlock()
			// The reap check is the race fix: a callback that lost the race
			// against Wait must not signal a pgid that may have been recycled.
			if st.reaped {
				return
			}
			_ = syscall.Kill(-st.pgid, syscall.SIGKILL)
		})
		if termErr != nil {
			// The group signal failed (exotic state); fall back to killing the
			// direct child so cancellation is never lost. Still pre-reap, so
			// targeting the child pid is safe.
			return p.Kill()
		}
		return nil
	}

	return func() {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.reaped = true
		if st.timer != nil {
			st.timer.Stop()
		}
	}
}
