package orchestrate

import (
	"context"
	"time"
)

// StepStatus is the terminal outcome of running one manager's upgrade commands.
type StepStatus string

const (
	// StatusOK means every command for the manager exited zero.
	StatusOK StepStatus = "ok"
	// StatusFailed means a command exited non-zero (or could not start).
	StatusFailed StepStatus = "failed"
	// StatusTimeout means the manager exceeded its per-manager timeout and was
	// killed.
	StatusTimeout StepStatus = "timeout"
)

// StepResult reports how one manager's run finished. It carries enough for the
// aggregate exit policy (a non-OK status makes `up` exit non-zero) and for the
// per-manager JSON summary.
type StepResult struct {
	Name     string        `json:"name"`
	Status   StepStatus    `json:"status"`
	Duration time.Duration `json:"-"`
	ExitCode int           `json:"exit"`
	// Err carries the underlying failure for logging; it is never serialized.
	Err error `json:"-"`
}

// OK reports whether the step succeeded.
func (r StepResult) OK() bool { return r.Status == StatusOK }

// StepExecutor runs one manager's ordered upgrade commands as subprocesses. The
// only production implementation lives in the executor subpackage, which is the
// single place in the codebase allowed to launch a manager subprocess. Keeping
// this interface (and its result types) in the orchestrate package — with no
// import of the executor subpackage — is what lets the dry-run call chain be
// statically proven free of any command-execution dependency (see
// executor_boundary_test.go and docs/09-decision-log.md, 2026-07-17).
type StepExecutor interface {
	// RunManager runs the manager's commands in order, stopping at the first
	// failing command (mirroring the `&&` chaining in the registry). It honors
	// ctx cancellation and enforces its own per-manager timeout.
	RunManager(ctx context.Context, name string, commands [][]string) StepResult
}
