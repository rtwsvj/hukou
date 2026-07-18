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
	"bufio"
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

// runOne launches a single command and streams its combined output. It returns
// the process exit code (or -1 when the process never produced one) and a
// non-nil error when the command failed, timed out, or was cancelled. On POSIX
// the command runs in its own process group and cancellation/timeout kills the
// whole group (SIGTERM, then SIGKILL after TermGrace) so grandchildren spawned
// by a manager cannot outlive it.
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
	// WaitDelay bounds how long Wait blocks draining pipes after the process is
	// killed on ctx cancellation/timeout, so an inherited-stdout grandchild
	// cannot hang the whole run.
	cmd.WaitDelay = grace
	// Platform-specific: own process group + group-wide TERM->KILL cancel.
	finishKill := setupProcessControl(cmd, termGrace)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return -1, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return -1, err
	}

	if err := cmd.Start(); err != nil {
		return -1, err
	}

	// Serialize the two streams onto shared writers so stdout and stderr lines
	// never interleave mid-line, even when both point at the same buffer.
	var mu sync.Mutex
	prefix := "[" + name + "] "
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); streamPrefixed(&mu, e.stdout, prefix, stdoutPipe) }()
	go func() { defer wg.Done(); streamPrefixed(&mu, e.stderr, prefix, stderrPipe) }()

	waitErr := cmd.Wait()
	wg.Wait()
	// If a cancellation started the TERM->KILL escalation, make sure the group
	// SIGKILL lands (now, if the escalation timer has not fired yet) so no
	// TERM-surviving descendant lingers after RunManager returns.
	finishKill()

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

// streamPrefixed copies src to dst line by line, writing prefix before each
// line, guarded by mu so concurrent stdout/stderr streams stay line-atomic.
func streamPrefixed(mu *sync.Mutex, dst io.Writer, prefix string, src io.Reader) {
	sc := bufio.NewScanner(src)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		mu.Lock()
		fmt.Fprintf(dst, "%s%s\n", prefix, sc.Text())
		mu.Unlock()
	}
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
