// Package orchestrate holds the machine-upgrade orchestration model for
// `hukou up`. The U1 slice contributes the table-driven manager registry and
// its read-only detection; real execution and the snapshot/diff report land in
// later slices. Nothing in this package launches a subprocess or writes to
// disk: detection resolves binaries through an injectable LookPath, which only
// stats the filesystem.
package orchestrate

import "github.com/rtwsvj/hukou/internal/lookpath"

// Manager is one upgrade participant: a named package manager (or hukou
// itself) paired with the binary that proves it is present and the exact argv
// sequences that would upgrade everything it owns. Each command is an argv
// slice, never a shell string, so a plan can never smuggle shell metacharacters
// into execution.
type Manager struct {
	// Name is the stable registry key surfaced to --only/--skip.
	Name string
	// DetectBinary is the executable whose presence on PATH activates the
	// manager. It is empty for an internal manager (see Internal).
	DetectBinary string
	// Commands are the ordered upgrade steps, each a complete argv slice.
	Commands [][]string
	// Internal marks hukou's own in-process step: it always participates and
	// runs no subprocess, so detection never probes PATH for it.
	Internal bool
}

// Registry returns the manager table in execution order. Adding a manager is
// one row here plus one fixture test, under the layered admission policy
// documented in docs/specs/phase3-up.md: a built-in row requires an
// atomic-ish inventory (the manager keeps its own install ledger, so the
// snapshot diff can attribute what it changed) and no root requirement;
// privileged system package managers stay out pending a privilege-model ADR;
// user-delegated rows arrive via managers.json (P-B).
//
// The table mirrors docs/specs/phase3-up.md exactly.
func Registry() []Manager {
	return []Manager{
		{Name: "brew", DetectBinary: "brew", Commands: [][]string{{"brew", "update"}, {"brew", "upgrade"}}},
		{Name: "npm", DetectBinary: "npm", Commands: [][]string{{"npm", "update", "-g"}}},
		{Name: "pnpm", DetectBinary: "pnpm", Commands: [][]string{{"pnpm", "update", "-g"}}},
		{Name: "rustup", DetectBinary: "rustup", Commands: [][]string{{"rustup", "update"}}},
		// cargo-install upgrades `cargo install`ed crates through the
		// third-party cargo-update helper. Detection is gated on the helper
		// binary itself, not on cargo: registry detection is a PATH stat and
		// cannot prove a subcommand exists, and `install-update` is not a
		// first-class cargo subcommand — gating on cargo would activate the
		// row on every rust machine without the helper, whose step then fails
		// (StatusFailed, exit -1) and flips the aggregate `up` exit non-zero
		// on every run. Detecting the helper keeps detect⇒run exact: the
		// stat'ed binary is the executed argv[0].
		//
		// Diff limitation: `-a` updates registry crates only; git-installed
		// crates are skipped (cargo-update's default) and can therefore never
		// appear in a diff.
		{Name: "cargo-install", DetectBinary: "cargo-install-update", Commands: [][]string{{"cargo-install-update", "-a"}}},
		{Name: "uv", DetectBinary: "uv", Commands: [][]string{{"uv", "tool", "upgrade", "--all"}}},
		// pipx bulk-upgrades every pipx-managed venv with one verb. Diff
		// limitation: the upgrade happens inside each venv while the diff
		// observes only the PATH launcher stubs (reported version + sha256),
		// so a venv-internal upgrade that leaves the stub byte-identical is
		// invisible. Injected packages are not upgraded (no
		// --include-injected).
		{Name: "pipx", DetectBinary: "pipx", Commands: [][]string{{"pipx", "upgrade-all"}}},
		{Name: "gh-extensions", DetectBinary: "gh", Commands: [][]string{{"gh", "extension", "upgrade", "--all"}}},
		{Name: "hukou", DetectBinary: "", Internal: true, Commands: [][]string{{"hukou", "upgrade", "--all"}}},
	}
}

// LookPathFunc resolves an executable name to a path, matching the signature of
// exec.LookPath (accessed via the fence-allowlisted internal/lookpath wrapper).
// Injecting it lets tests supply a fake PATH without touching the real system
// and lets callers prove detection never runs anything.
type LookPathFunc func(file string) (string, error)

// Detected is a Manager paired with its detection outcome.
type Detected struct {
	Manager
	// Available reports whether the manager participates on this machine.
	Available bool
	// BinaryPath is the resolved path to DetectBinary when the manager is
	// available and external; it is empty for internal or absent managers.
	BinaryPath string
}

// Detect resolves each manager against PATH through lookPath (nil defaults to
// lookpath.LookPath, the fence-allowlisted exec.LookPath wrapper). It launches
// no subprocess: lookPath only stats the filesystem. The internal hukou row is
// always available and is never probed.
func Detect(managers []Manager, lookPath LookPathFunc) []Detected {
	if lookPath == nil {
		lookPath = lookpath.LookPath
	}
	out := make([]Detected, 0, len(managers))
	for _, m := range managers {
		d := Detected{Manager: m}
		switch {
		case m.Internal:
			d.Available = true
		default:
			if p, err := lookPath(m.DetectBinary); err == nil {
				d.Available = true
				d.BinaryPath = p
			}
		}
		out = append(out, d)
	}
	return out
}

// Filter narrows managers to those permitted by only (whitelist; empty means
// all) and not excluded by skip. Names are registry keys; an unknown name in
// either list is an error so a typo never silently upgrades the wrong set.
func Filter(managers []Manager, only, skip []string) ([]Manager, error) {
	known := make(map[string]struct{}, len(managers))
	for _, m := range managers {
		known[m.Name] = struct{}{}
	}
	for _, name := range append(append([]string(nil), only...), skip...) {
		if _, ok := known[name]; !ok {
			return nil, &UnknownManagerError{Name: name}
		}
	}
	onlySet := toSet(only)
	skipSet := toSet(skip)
	out := make([]Manager, 0, len(managers))
	for _, m := range managers {
		if len(onlySet) > 0 {
			if _, ok := onlySet[m.Name]; !ok {
				continue
			}
		}
		if _, ok := skipSet[m.Name]; ok {
			continue
		}
		out = append(out, m)
	}
	return out, nil
}

// UnknownManagerError reports a --only/--skip name absent from the registry.
type UnknownManagerError struct{ Name string }

func (e *UnknownManagerError) Error() string {
	return "unknown manager: " + e.Name
}

func toSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(names))
	for _, n := range names {
		set[n] = struct{}{}
	}
	return set
}
