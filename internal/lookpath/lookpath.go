// Package lookpath isolates the ONLY non-executing use of os/exec that hukou
// needs outside the executor package: resolving a binary name against PATH.
//
// The repo-wide execution fence (internal/orchestrate/execution_fence_test.go)
// bans importing os/exec in any form (named, aliased, dot, blank) in every
// non-test file outside internal/orchestrate/executor, with this file as the
// single, explicitly allowlisted exception. The fence still applies its
// execution-primitive selector rules to this file, so it can resolve names but
// can never grow the ability to run them (exec.Command / exec.CommandContext /
// the exec.Cmd type are all still violations here).
//
// It exists as its own package because the natural home — the executor
// package — is unreachable from detection: the executor imports orchestrate
// for its result types, so orchestrate (whose Detect defaults to LookPath)
// importing the executor back would be an import cycle.
package lookpath

import "os/exec"

// LookPath resolves an executable name against PATH, exactly like
// exec.LookPath. It only stats the filesystem; it launches nothing.
func LookPath(file string) (string, error) {
	return exec.LookPath(file)
}
