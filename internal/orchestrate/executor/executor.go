// Package executor is the single place in hukou allowed to launch a manager's
// upgrade subprocess. It wraps os/exec with streamed, prefixed output and a
// per-manager timeout, and implements orchestrate.StepExecutor so the rest of
// the program consumes it through that interface.
//
// Execution model — deliberately plain and portable. Each command runs as
// exec.CommandContext(ctx, argv[0], argv[1:]...) with NO SysProcAttr: the
// manager stays in hukou's own foreground process group, so a terminal Ctrl-C
// reaches it naturally (no manual signal forwarding), and cancellation/timeout
// is delivered by CommandContext killing the direct child. There is no process
// group, no two-phase SIGTERM/SIGKILL escalation, and no reap bookkeeping.
//
// Known limitation, stated honestly: a timeout or cancel kills only the DIRECT
// child. A manager that spawns a detached grandchild (a backgrounded daemon,
// say) can leave that grandchild running — exactly as it would if you ran the
// manager's upgrade command directly in your shell. hukou does not chase the
// process tree.
//
// The executor is the only package in the tree that launches a subprocess; a
// repo-wide go/ast fence (internal/orchestrate/execution_fence_test.go) asserts
// no other non-test file uses an execution primitive.
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

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/orchestrate"
)

// DefaultTimeout bounds a single manager's whole run (all of its commands). A
// hung manager is killed once this elapses and reported as StatusTimeout.
const DefaultTimeout = 15 * time.Minute

// waitDrainGrace bounds how long Wait keeps draining a finished (or killed)
// command's inherited pipes before the descriptors are force-closed. This is
// plain os/exec I/O bookkeeping, not process management: by the time it
// matters the direct child is already gone, and any detached grandchild that
// kept an output pipe open is deliberately left alone (see the package doc's
// known limitation). Without it, such a grandchild could wedge Wait forever.
const waitDrainGrace = 10 * time.Second

// Executor runs manager commands as real subprocesses. The zero value is not
// usable; construct it with New so writers are non-nil.
type Executor struct {
	// Timeout bounds each manager's whole run; <= 0 uses DefaultTimeout.
	Timeout time.Duration

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
			res.Err = i18n.Wrapf("%s: %s: %w", err, name, argv[0])
			res.Duration = time.Since(start)
			return res
		}
	}
	res.Duration = time.Since(start)
	return res
}

// runOne launches a single command and streams its output. It returns the
// process exit code (or -1 when the process never produced one) and a non-nil
// error when the command failed, timed out, or was cancelled. Cancellation and
// timeout are delivered by exec.CommandContext, which kills the direct child;
// no process group is created and no descendant is chased.
func (e *Executor) runOne(ctx context.Context, name string, argv []string) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// WaitDelay bounds pipe draining after the process exits or is killed, so a
	// detached grandchild holding an inherited pipe cannot wedge Wait forever.
	cmd.WaitDelay = waitDrainGrace

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
		// The deadline or a parent cancellation ended the process. Surface the
		// context error; RunManager classifies it as timeout vs canceled.
		return exitCodeOf(waitErr), i18n.Wrapf("%w", ctx.Err())
	}
	if waitErr != nil {
		return exitCodeOf(waitErr), waitErr
	}
	return 0, nil
}

// classifyCtx maps context state to a step status: a cancelled PARENT is a
// user/caller cancellation (StatusCanceled); a hit per-manager deadline (parent
// still live) is StatusTimeout; any other cancelled runCtx is a failure.
func classifyCtx(runCtx, parent context.Context) orchestrate.StepStatus {
	if parent.Err() != nil {
		return orchestrate.StatusCanceled
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
