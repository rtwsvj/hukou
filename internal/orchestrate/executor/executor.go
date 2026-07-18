// Package executor is the single place in hukou allowed to launch a manager's
// upgrade subprocess. It wraps os/exec with streamed, prefixed output, a
// per-manager timeout, and context cancellation, and it implements
// orchestrate.StepExecutor so the rest of the program consumes it through that
// interface. Because no other package (and, in particular, neither the
// orchestrate package nor the dry-run call chain) imports this one, the absence
// of command execution on the dry-run path is a structural, statically checkable
// property — see internal/orchestrate/executor_boundary_test.go.
package executor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"

	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// DefaultTimeout bounds a single manager's whole run (all of its commands). A
// hung manager is killed once this elapses and reported as StatusTimeout.
const DefaultTimeout = 15 * time.Minute

// DefaultTermGrace is how long a cancelled/timed-out manager's process group
// gets between the graceful SIGTERM and the follow-up group SIGKILL.
const DefaultTermGrace = 5 * time.Second

// defaultKillGrace is how long Wait may keep draining a killed process's pipes
// before the descriptors are force-closed, so a lingering grandchild that
// inherited stdout cannot wedge the run indefinitely.
const defaultKillGrace = 10 * time.Second

// Executor runs manager commands as real subprocesses. The zero value is not
// usable; construct it with New so writers are non-nil.
type Executor struct {
	// Timeout bounds each manager's whole run; <= 0 uses DefaultTimeout.
	Timeout time.Duration
	// TermGrace is the SIGTERM -> SIGKILL escalation window applied to the
	// manager's whole process group on timeout/cancel; <= 0 uses
	// DefaultTermGrace. POSIX only; elsewhere cancellation kills the direct
	// child (see setupProcessControl).
	TermGrace time.Duration
	// KillGrace bounds pipe draining after a kill; <= 0 uses defaultKillGrace.
	KillGrace time.Duration

	stdout io.Writer
	stderr io.Writer
}

// New builds an Executor streaming manager output to stdout/stderr with the
// default per-manager timeout. nil writers fall back to io.Discard so a caller
// can never nil-panic mid-stream.
func New(stdout, stderr io.Writer) *Executor {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	return &Executor{Timeout: DefaultTimeout, stdout: stdout, stderr: stderr}
}

// compile-time proof the Executor satisfies the constrained execution seam.
var _ orchestrate.StepExecutor = (*Executor)(nil)

// RunManager runs the manager's commands in registry order. It stops at the
// first command that fails (matching the `&&` chaining the registry encodes),
// enforces a single per-manager timeout across all commands, and honors ctx
// cancellation between and during commands. Output from every command streams
// through with a "[name] " prefix on each line.
func (e *Executor) RunManager(ctx context.Context, name string, commands [][]string) orchestrate.StepResult {
	timeout := e.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	// One deadline covers the whole manager, not each command, so a manager that
	// dribbles across many slow steps still cannot exceed its budget.
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	res := orchestrate.StepResult{Name: name, Status: orchestrate.StatusOK}

	for _, argv := range commands {
		if len(argv) == 0 {
			continue
		}
		if err := runCtx.Err(); err != nil {
			res.Status = classifyCtx(runCtx, ctx)
			res.Err = err
			res.ExitCode = -1
			res.Duration = time.Since(start)
			return res
		}
		code, err := e.runOne(runCtx, name, argv)
		res.ExitCode = code
		if err != nil {
			res.Status = classifyCtx(runCtx, ctx)
			if res.Status == orchestrate.StatusOK {
				res.Status = orchestrate.StatusFailed
			}
			res.Err = fmt.Errorf("%s: %s: %w", name, argv[0], err)
			res.Duration = time.Since(start)
			return res
		}
	}
	res.Duration = time.Since(start)
	return res
}

// runOne launches a single command and streams its output. It returns the
// process exit code (or -1 when the process never produced one) and a non-nil
// error when the command failed, timed out, or was cancelled. On POSIX the
// command runs in its own process group and cancellation/timeout kills the
// whole group (SIGTERM, then SIGKILL after TermGrace) so grandchildren spawned
// by a manager cannot outlive it.
//
// Output deliberately uses cmd.Stdout/cmd.Stderr writers rather than
// StdoutPipe: with writers, Wait keeps draining until the pipes reach EOF (or
// WaitDelay force-closes them), so a grandchild holding the inherited pipes
// keeps the direct child UN-reaped long enough for the pre-reap group-kill
// escalation to fire — the safety property the whole kill protocol rests on
// (see proc_unix.go). StdoutPipe would let Wait return at reap and strand such
// grandchildren.
func (e *Executor) runOne(ctx context.Context, name string, argv []string) (int, error) {
	grace := e.KillGrace
	if grace <= 0 {
		grace = defaultKillGrace
	}
	termGrace := e.TermGrace
	if termGrace <= 0 {
		termGrace = DefaultTermGrace
	}

	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// WaitDelay bounds how long Wait blocks draining pipes after the process
	// exits or is cancelled, so an inherited-stdout grandchild cannot wedge the
	// run indefinitely.
	cmd.WaitDelay = grace
	// Platform-specific: own process group + group-wide TERM->KILL cancel.
	afterReap := setupProcessControl(cmd, termGrace)

	// A shared mutex keeps stdout and stderr lines from interleaving mid-line,
	// even when both writers point at the same buffer.
	var mu sync.Mutex
	prefix := "[" + name + "] "
	outW := &lineWriter{mu: &mu, dst: e.stdout, prefix: prefix}
	errW := &lineWriter{mu: &mu, dst: e.stderr, prefix: prefix}
	cmd.Stdout = outW
	cmd.Stderr = errW

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	waitErr := cmd.Wait()
	// The child is reaped: under the shared lock, mark it so and cancel any
	// still-pending escalation — after this point nothing may signal the pgid
	// again (it could have been recycled). See proc_unix.go for the protocol.
	afterReap()
	// Wait has joined the internal copy goroutines; flush any trailing
	// unterminated line from each stream.
	outW.flush()
	errW.flush()

	// A manager that exited zero but left a descendant holding the pipes past
	// WaitDelay is a success with lingering output, not a failure.
	if errors.Is(waitErr, exec.ErrWaitDelay) && ctx.Err() == nil &&
		cmd.ProcessState != nil && cmd.ProcessState.ExitCode() == 0 {
		waitErr = nil
	}

	if ctx.Err() != nil {
		// The deadline or a parent cancellation ended the process. Report it as
		// timeout when it was our per-manager deadline; a cancelled parent still
		// surfaces as a failure with the context error.
		return exitCodeOf(waitErr), fmt.Errorf("%w", ctx.Err())
	}
	if waitErr != nil {
		return exitCodeOf(waitErr), waitErr
	}
	return 0, nil
}

// classifyCtx maps context state to a step status: a hit per-manager deadline is
// a timeout; a cancelled parent context is a failure; otherwise OK.
func classifyCtx(runCtx, parent context.Context) orchestrate.StepStatus {
	if parent.Err() != nil {
		return orchestrate.StatusFailed
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return orchestrate.StatusTimeout
	}
	if runCtx.Err() != nil {
		return orchestrate.StatusFailed
	}
	return orchestrate.StatusOK
}

// lineWriter forwards complete lines to dst with a prefix, holding mu per line
// so concurrent stdout/stderr streams stay line-atomic. os/exec writes to each
// instance from a single internal copy goroutine; flush is called only after
// Wait has joined those goroutines.
type lineWriter struct {
	mu     *sync.Mutex
	dst    io.Writer
	prefix string
	buf    []byte
}

// maxBufferedLine caps how much of an unterminated line is buffered before it
// is force-flushed, so a manager emitting a newline-free torrent cannot grow
// memory without bound.
const maxBufferedLine = 1 << 20

func (lw *lineWriter) Write(p []byte) (int, error) {
	lw.buf = append(lw.buf, p...)
	for {
		i := bytes.IndexByte(lw.buf, '\n')
		if i < 0 {
			break
		}
		lw.emit(lw.buf[:i])
		lw.buf = lw.buf[i+1:]
	}
	if len(lw.buf) > maxBufferedLine {
		lw.emit(lw.buf)
		lw.buf = lw.buf[:0]
	}
	return len(p), nil
}

// flush emits any trailing unterminated line.
func (lw *lineWriter) flush() {
	if len(lw.buf) == 0 {
		return
	}
	lw.emit(lw.buf)
	lw.buf = lw.buf[:0]
}

func (lw *lineWriter) emit(line []byte) {
	lw.mu.Lock()
	fmt.Fprintf(lw.dst, "%s%s\n", lw.prefix, line)
	lw.mu.Unlock()
}

// exitCodeOf extracts a process exit code from a Wait error; -1 when the error
// is not an ExitError (never started, killed before exit, etc.).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}
