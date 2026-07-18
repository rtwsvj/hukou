package orchestrate

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const (
	orchestratePkg = "github.com/rtwsvj/hukou/internal/orchestrate"
	executorPkg    = "github.com/rtwsvj/hukou/internal/orchestrate/executor"
)

// TestExecutorNotInOrchestrateDeps is a defense-in-depth layer beneath the
// primary repo-wide fence (execution_fence_test.go): `go list -deps` proves the
// orchestrate package — the planning/diff computation the dry-run path is built
// from — has no transitive dependency on the executor subpackage, the sole
// place a manager subprocess is launched.
func TestExecutorNotInOrchestrateDeps(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", orchestratePkg).CombinedOutput()
	if err != nil {
		t.Fatalf("go list -deps %s: %v\n%s", orchestratePkg, err, out)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(line) == executorPkg {
			t.Fatalf("orchestrate package must not depend on the executor subpackage, "+
				"but %s appears in its transitive deps; the dry-run call chain would then "+
				"be able to reach command execution", executorPkg)
		}
	}
}

// TestExecutorSubpackageExists sanity-checks that the guarded subpackage is the
// real one on disk, so the deps guard above is meaningful rather than guarding a
// package that was renamed away.
func TestExecutorSubpackageExists(t *testing.T) {
	if _, err := os.Stat(filepath.Join("executor", "executor.go")); err != nil {
		t.Fatalf("expected executor subpackage at ./executor/executor.go: %v", err)
	}
}
