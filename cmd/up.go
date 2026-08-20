// This file wires the `up` command and dispatches between its two entry files:
// the dry-run (plan-only) path in up_plan.go and the real execution path in
// up_exec.go. The executor boundary is guarded at package level — the plan
// package's transitive deps contain neither the executor subpackage nor
// os/exec (internal/orchestrate/plan/guard_test.go) — with a file-level import
// check (up_guard_test.go) and the U1 forbidRunner behavioral stub as depth
// (see docs/09-decision-log.md, 2026-07-17).
package cmd

import (
	"os"
	"time"

	"github.com/spf13/cobra"
)

var (
	upDryRun          bool
	upJSON            bool
	upOnly            []string
	upSkip            []string
	upRetry           int
	upTimeout         time.Duration
	upManagerTimeouts []string
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Upgrade every known manager on this machine and diff the inventory",
	Long: `Upgrade the package managers hukou knows about on this machine plus
hukou's own adopted tools, then report exactly what changed.

A dry run (--dry-run) detects the managers present on PATH, prints the exact
commands that would run and a read-only inventory summary, and is guaranteed to
make zero writes and launch zero subprocesses.

A real run takes a full-machine inventory snapshot, runs each manager's upgrade
command in registry order (a failing manager is reported and does not stop the
rest), takes a second snapshot, and prints a diff of every added, removed, or
changed binary grouped by source. The snapshot pair and diff are persisted under
<dataRoot>/snapshots/<timestamp>/. Every manager subprocess is launched through
a single constrained executor with streamed output and a per-manager timeout
(default 15m; set it globally with --timeout or HUKOU_UP_TIMEOUT, or per manager
with --manager-timeout name=duration). On unix a timeout or Ctrl-C terminates
the manager's whole process group (SIGTERM, then SIGKILL after a grace period);
on other platforms only the direct child is killed. A manager inherits the OS
system proxy when the environment does not already configure one
(HUKOU_UP_NO_PROXY_INHERIT=1 opts out). hukou never mutates another
manager's state beyond invoking its upgrade command. The exit status is non-zero
if any manager fails, the run is interrupted, or the snapshot history cannot be
persisted. With --json, stdout carries only the final JSON document; all
streamed manager output goes to stderr.

The read-only "up history" and "up show" subcommands list and re-render the runs
a real run persisted, including each run's diff and rollback hints.`,
	Args:          cobra.NoArgs,
	SilenceErrors: true, // errors are printed once, here or via fail.
	RunE:          runUp,
}

func init() {
	upCmd.Flags().BoolVar(&upDryRun, "dry-run", false, "detect managers and print the upgrade plan without executing or writing anything")
	upCmd.Flags().BoolVar(&upJSON, "json", false, "emit the plan (dry-run) or the run report as JSON")
	upCmd.Flags().StringSliceVar(&upOnly, "only", nil, "restrict to these managers by registry name (repeatable or comma-separated)")
	upCmd.Flags().StringSliceVar(&upSkip, "skip", nil, "exclude these managers by registry name (repeatable or comma-separated)")
	upCmd.Flags().IntVar(&upRetry, "retry", 0, "retry each failed external manager this many times (default 0)")
	upCmd.Flags().DurationVar(&upTimeout, "timeout", 0, "per-manager timeout for external managers (e.g. 30m); overrides HUKOU_UP_TIMEOUT (default 15m)")
	upCmd.Flags().StringArrayVar(&upManagerTimeouts, "manager-timeout", nil, "override the timeout for one external manager, name=duration (repeatable, e.g. brew=45m)")
	rootCmd.AddCommand(upCmd)
}

// runUp dispatches one `up` invocation: --dry-run goes to the plan-only entry
// (doUpPlan, defined in up_plan.go, which cannot reach the executor), a real
// run goes to the execution entry (runUpExecute, defined in up_exec.go, the
// only cmd file that imports the executor subpackage). Timeout flags are
// resolved before dispatch so a typo fails fast on either path.
func runUp(cmd *cobra.Command, _ []string) error {
	timeout, err := resolveUpTimeout(upTimeout, os.Getenv)
	if err != nil {
		return fail(err)
	}
	managerTimeouts, err := parseManagerTimeouts(upManagerTimeouts)
	if err != nil {
		return fail(err)
	}
	opts := upOptions{
		dryRun:          upDryRun,
		json:            upJSON,
		only:            upOnly,
		skip:            upSkip,
		retries:         upRetry,
		timeout:         timeout,
		managerTimeouts: managerTimeouts,
	}
	if opts.dryRun {
		// nil runner: the production dry-run wires no execution capability at
		// all; the seam exists only so tests can inject a failing stub.
		return doUpPlan(cmd.OutOrStdout(), opts, nil, defaultInventory, nil)
	}
	return runUpExecute(cmd.OutOrStdout(), cmd.ErrOrStderr(), opts)
}

// upOptions captures one resolved `up` invocation.
type upOptions struct {
	dryRun  bool
	json    bool
	only    []string
	skip    []string
	retries int
	// timeout is the resolved base per-manager timeout (--timeout flag, then
	// HUKOU_UP_TIMEOUT); 0 means unset, normalized to the executor default by
	// the entry points.
	timeout time.Duration
	// managerTimeouts overrides timeout per external manager name (already
	// validated against the registry).
	managerTimeouts map[string]time.Duration
}

// ExitCode maps a top-level command error to a process exit status: success is
// 0 and every failure — including an aggregate manager failure or a snapshot
// persistence failure — is the generic 1. There is no other exit code.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	return 1
}
