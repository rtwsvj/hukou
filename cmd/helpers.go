package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/roottrust"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/state"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

// dataRoot returns the hukou data directory.
// HUKOU_DATA_DIR overrides the XDG Data Home default. When neither a home
// directory nor XDG_DATA_HOME is available there is NO safe default —
// falling back to a relative "./hukou" would silently scatter state into
// whatever directory hukou happened to be started from — so the process
// refuses to continue instead.
func dataRoot() string {
	root, err := resolveDataRoot(os.Getenv, os.UserHomeDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, i18n.T("error: %s", err))
		os.Exit(2)
	}
	return root
}

// resolveDataRoot is the testable core of dataRoot: same resolution order,
// error instead of exit.
func resolveDataRoot(getenv func(string) string, userHomeDir func() (string, error)) (string, error) {
	if v := getenv("HUKOU_DATA_DIR"); v != "" {
		// A relative HUKOU_DATA_DIR would scatter state into whatever
		// directory hukou happened to be started from — same failure mode as
		// the old "./hukou" fallback, so it is rejected outright.
		if !filepath.IsAbs(v) {
			return "", i18n.Errorf("cannot determine the hukou data directory: HUKOU_DATA_DIR must be an absolute path, got %q", v)
		}
		return v, nil
	}
	home, _ := userHomeDir()
	xdg := getenv("XDG_DATA_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".local", "share")
	}
	if xdg == "" {
		return "", i18n.Errorf("cannot determine the hukou data directory: neither HOME nor XDG_DATA_HOME is set; set HUKOU_DATA_DIR explicitly")
	}
	root := filepath.Join(xdg, "hukou")
	// Same error class as the HUKOU_DATA_DIR check above: whatever the source,
	// a relative result scatters state into whatever directory hukou was
	// started from. This also catches a relative XDG_DATA_HOME and a relative
	// home-derived base.
	if !filepath.IsAbs(root) {
		return "", i18n.Errorf("cannot determine the hukou data directory: resolved data directory %q must be an absolute path; is XDG_DATA_HOME or HOME relative?", root)
	}
	return root, nil
}

func manifestPath() string { return filepath.Join(dataRoot(), "manifest.json") }
func storeRoot() string    { return filepath.Join(dataRoot(), "store") }

func newStore() *store.Store { return &store.Store{Root: storeRoot()} }

// Seam hooks for tests: production defaults keep the real filesystem and the
// real effective uid.
var (
	lstatDataRoot   = os.Lstat
	effectiveUID    = os.Geteuid
	dataRootFileUID = ownerOfFileInfo
)

// checkDataRootTrust is the fail-closed trust gate for the hukou data
// directory: it must be a real directory (no symlink), owned by the effective
// user, and not group- or other-writable. This deliberately also refuses
// running under sudo (or any other privilege escalation) against a
// user-owned root, because a privileged writer could be steered into
// replaying state an attacker prepared as another user. The canonical check
// lives in internal/roottrust so the repair applier enforces the identical
// gate; the seam vars above stay the test hooks for this package's callers.
func checkDataRootTrust(root string) error {
	return roottrust.Check(root, roottrust.Hooks{
		Lstat:        lstatDataRoot,
		EffectiveUID: effectiveUID,
		FileUID:      dataRootFileUID,
	})
}

// trustedAcquire is the single entry point for mutating paths: it validates
// (or creates) the data root, runs the fail-closed trust gate, and only then
// takes the state lock under that root. Every caller that is about to mutate
// hukou state MUST go through this — acquiring the lock without the trust gate
// would let a group-writable or foreign-owned root be locked and written.
//
// Known limitation (accepted for now): the gate validates the leaf path, but a
// concurrent local attacker can still swap an ANCESTOR directory component of
// root for a symlink between validation and use — the classic stat-then-use
// TOCTOU. Fully closing it requires openat-style anchored traversal of every
// path component (future ADR-grade work); until then this matches the threat
// model of an advisory-lock CLI, and the leaf-level gate still blocks the
// cheap attacks: pre-planted group-writable roots, foreign-owned roots, and a
// symlinked leaf itself.
func trustedAcquire(root string, stderr io.Writer) (*state.Lock, error) {
	// Validate-before-create for the leaf: when the directory already exists,
	// the trust gate sees exactly what the lock will be taken against; creation
	// happens only when the leaf is missing. A missing leaf is fine — MkdirAll
	// brings it into existence below and the gate re-runs on the fresh
	// directory before any lock is taken.
	if err := checkDataRootTrust(root); err != nil {
		if _, statErr := lstatDataRoot(root); !errors.Is(statErr, os.ErrNotExist) {
			return nil, err
		}
	}
	if err := durablefs.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	if err := checkDataRootTrust(root); err != nil {
		return nil, err
	}
	return state.Acquire(filepath.Join(root, "state.lock"))
}

// acquireMutationLock resolves the data root and returns its locked,
// trust-gated handle with automatic WAL recovery applied; see trustedAcquire.
// The gate inside trustedAcquire is inseparable: there is no code path that
// locks hukou state without passing it.
func acquireMutationLock(stderr io.Writer) (*state.Lock, error) {
	root := dataRoot()
	lock, err := trustedAcquire(root, stderr)
	if err != nil {
		return nil, err
	}
	summary, err := statejournal.Recover(root)
	reportRecoverSummary(stderr, summary)
	if err != nil {
		return nil, errors.Join(i18n.Wrapf("recover unfinished transaction: %w", err), lock.Release())
	}
	return lock, nil
}

// reportRecoverSummary surfaces recovery side effects on the warning channel so
// quarantined evidence is never isolated silently.
func reportRecoverSummary(stderr io.Writer, summary statejournal.RecoverSummary) {
	if stderr == nil {
		return
	}
	for _, record := range summary.Quarantined {
		fmt.Fprintf(stderr, "warning: quarantined unknown transaction entry %q as transactions/%s; inspect it with `hukou doctor`, then remove it manually when you are satisfied it is no longer needed\n", record.Original, record.Quarantined)
	}
}

func releaseMutationLock(lock *state.Lock, stderr io.Writer) {
	if err := lock.Release(); err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "warning: failed to release the hukou state lock: %v\n", err)
		}
	}
}

func loadManifest() (*manifest.Manifest, error) {
	return manifest.Load(manifestPath())
}

func saveManifest(m *manifest.Manifest) error {
	if err := durablefs.MkdirAll(filepath.Dir(manifestPath()), 0o755); err != nil {
		return err
	}
	return m.Save(manifestPath())
}

func rfc3339Now() string { return time.Now().Format(time.RFC3339) }

func splitRepo(repo string) (owner, repoName string, ok bool) {
	parts := strings.Split(repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func modulePathToRepo(mp string) string {
	if !strings.HasPrefix(mp, "github.com/") {
		return ""
	}
	parts := strings.Split(strings.TrimPrefix(mp, "github.com/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "/" + parts[1]
}

func allowedAdoptSource(src string) bool {
	switch src {
	case "unknown", "curl-installer", "local-project":
		return true
	}
	return false
}

func runSecurityGate(binPath string) (*provenance.Attribution, error) {
	realPath, err := filepath.EvalSymlinks(binPath)
	if err != nil {
		realPath = binPath
	}
	kind, _ := scan.DetectKind(binPath)
	b := scan.Binary{
		Name:     filepath.Base(binPath),
		Path:     binPath,
		RealPath: realPath,
		Kind:     kind,
	}
	env := provenance.DefaultEnv()
	runner := provenance.DefaultRunner()
	// The gate keys ONLY on warnings (detector load failures = degraded
	// provenance). Notes are non-fatal advisories from detectors that loaded
	// fine (e.g. verified stale journal residue); consuming them here would
	// let any detector's routine advisory block adopt.
	warnings, _ := runner.Load(env)
	if len(warnings) > 0 {
		return nil, i18n.Errorf("load provenance security gate: %s", strings.Join(warnings, "; "))
	}
	return runner.Match(b), nil
}

func activateOriginal(s *store.Store, name, livePath string) error {
	return s.Activate(name, "original", livePath)
}
