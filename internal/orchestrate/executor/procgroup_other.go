//go:build !unix

package executor

import "os/exec"

// configureProc is a no-op off unix: there are no portable process groups, so
// CommandContext's default — killing the direct child on cancellation — is
// the whole cancellation story.
func configureProc(cmd *exec.Cmd) {}
