// Package executor is the single place in hukou allowed to launch a manager's
// upgrade subprocess. It wraps os/exec with streamed, prefixed output and a
// per-manager timeout, and implements orchestrate.StepExecutor so the rest of
// the program consumes it through that interface.
//
// Execution model. Each command runs as exec.CommandContext(ctx, argv[0],
// argv[1:]...). On unix the command is placed in its OWN process group
// (SysProcAttr.Setpgid) and context cancellation — a per-manager timeout, or
// an interrupt propagated through signal.NotifyContext — terminates the whole
// group: SIGTERM first, SIGKILL after a short grace (procgroup_unix.go), so a
// grandchild the manager spawned (brew's curl, say) dies with it instead of
// being orphaned. Off unix there are no process groups and cancellation kills
// only the direct child (procgroup_other.go), the plain CommandContext
// default. The child's environment comes from buildChildEnv (env.go), which
// passes the OS system proxy through unless the environment already
// configures a proxy or opts out.
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
// matters the child is already gone, and any grandchild that kept an output
// pipe open past the group kill is deliberately left alone (a killed process
// cannot hold hukou's Wait hostage either way). Without it, such a grandchild
// could wedge Wait forever.
const waitDrainGrace = 10 * time.Second

// Executor runs manager commands as real subprocesses. The zero value is not
// usable; construct it with New so writers are non-nil.
type Executor struct {
	// Timeout bounds each manager's whole run; <= 0 uses DefaultTimeout.
	Timeout time.Duration

	// Timeouts overrides Timeout for individual managers by registry name
	// (e.g. {"brew": 45m} for a slow brew on a slow network). Names not
	// present fall back to Timeout.
	Timeouts map[string]time.Duration

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

// TimeoutFor resolves the effective timeout for manager name: a per-name
// override from Timeouts first, then the executor-wide Timeout, then
// DefaultTimeout. Non-positive entries are treated as unset.
func (e *Executor) TimeoutFor(name string) time.Duration {
	if d, ok := e.Timeouts[name]; ok && d > 0 {
		return d
	}
	if e.Timeout > 0 {
		return e.Timeout
	}
	return DefaultTimeout
}

// RunManager runs the manager's commands in registry order. It stops at the
// first command that fails (matching the `&&` chaining the registry encodes),
// enforces a single per-manager timeout across all commands, and honors ctx
// cancellation between and during commands. Output from every command streams
// through with a "[name] " prefix on each line.
func (e *Executor) RunManager(ctx context.Context, name string, commands [][]string) orchestrate.StepResult {
	timeout := e.TimeoutFor(name)
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
// timeout are delivered through ctx: on unix the command runs in its own
// process group and the whole group is terminated (configureProc), off unix
// CommandContext kills the direct child.
func (e *Executor) runOne(ctx context.Context, name string, argv []string) (int, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	// Process-group setup decides what cancellation kills (see procgroup_*).
	configureProc(cmd)
	// WaitDelay bounds pipe draining after the process exits or is killed, so a
	// detached grandchild holding an inherited pipe cannot wedge Wait forever.
	cmd.WaitDelay = waitDrainGrace

	env, proxyHost, fullPassthru := buildChildEnv()
	cmd.Env = env
	if fullPassthru {
		fmt.Fprintf(e.stderr, "[%s] HUKOU_UP_ENV_PASSTHRU=*: passing the full parent environment to the subprocess\n", name)
	}
	if proxyHost != "" {
		// Host (with port) only: the proxy URL may carry credentials in its
		// userinfo, which must never land in a streamed log line.
		fmt.Fprintf(e.stderr, "[%s] using system proxy %s\n", name, proxyHost)
	}

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

// classifyCtx maps context state to a step status: an explicitly cancelled
// parent (a Ctrl-C via signal.NotifyContext) is StatusCanceled; a hit
// deadline is StatusTimeout — whether it is the executor's own per-manager
// budget or an earlier caller-supplied deadline (a retry loop sharing one
// budget across attempts relies on WithTimeout honoring the earlier parent
// deadline); any other cancelled runCtx is a failure.
func classifyCtx(runCtx, parent context.Context) orchestrate.StepStatus {
	if errors.Is(parent.Err(), context.Canceled) {
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
