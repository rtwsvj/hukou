// Package roottrust holds the fail-closed trust gate for the hukou data
// directory. Every mutating code path — the cmd lock helpers and the repair
// applier alike — must consult this gate before locking or writing under a
// data root: the root must be a real directory (no symlink), owned by the
// effective user, and not group- or other-writable. The gate deliberately also
// refuses running under sudo (or any other privilege escalation) against a
// user-owned root, because a privileged writer could be steered into replaying
// state an attacker prepared as another user.
//
// Hooks exist purely so tests can fake the stat/uid seams; production callers
// pass the zero value.
package roottrust

import (
	"os"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// Hooks override the production filesystem and uid seams. Nil fields fall
// back to the real implementations.
type Hooks struct {
	Lstat        func(string) (os.FileInfo, error)
	EffectiveUID func() int
	// FileUID reports the owning uid of fi (-1 when unknown on this platform).
	FileUID func(os.FileInfo) int
}

func (h Hooks) withDefaults() Hooks {
	if h.Lstat == nil {
		h.Lstat = os.Lstat
	}
	if h.EffectiveUID == nil {
		h.EffectiveUID = os.Geteuid
	}
	if h.FileUID == nil {
		h.FileUID = ownerOfFileInfo
	}
	return h
}

// Check runs the trust gate against root. It inspects the path with Lstat, so
// a symbolic link pointing at a perfectly good directory is still refused:
// the link itself could be swapped by another local writer.
func Check(root string, hooks Hooks) error {
	h := hooks.withDefaults()
	fi, err := h.Lstat(root)
	if err != nil {
		return i18n.Errorf("cannot inspect the hukou data directory %s: %v", root, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return i18n.Errorf("refusing to use the hukou data directory %s: it is a symbolic link, not a real directory", root)
	}
	if !fi.IsDir() {
		return i18n.Errorf("refusing to use the hukou data directory %s: it is not a directory (%s)", root, fi.Mode())
	}
	euid := h.EffectiveUID()
	if uid := h.FileUID(fi); uid >= 0 && uid != euid {
		return i18n.Errorf("refusing to use the hukou data directory %s: it is owned by uid %d but hukou runs as uid %d; running hukou with elevated privileges against another user's data root is unsupported", root, uid, euid)
	}
	if perm := fi.Mode().Perm(); perm&0o022 != 0 {
		return i18n.Errorf("refusing to use the hukou data directory %s: it is group- or other-writable (%s); it must belong to the current user and not be writable by anyone else", root, fi.Mode())
	}
	return nil
}
