package cmd

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveDataRoot: HUKOU_DATA_DIR wins, then XDG_DATA_HOME, then HOME's
// .local/share — and when nothing is available the resolution is an error,
// never a silent relative "./hukou".
func TestResolveDataRoot(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	home := func(h string, err error) func() (string, error) {
		return func() (string, error) { return h, err }
	}

	// HUKOU_DATA_DIR wins over everything.
	if got, err := resolveDataRoot(env(map[string]string{"HUKOU_DATA_DIR": "/explicit", "XDG_DATA_HOME": "/xdg"}), home("/home/u", nil)); err != nil || got != "/explicit" {
		t.Fatalf("explicit = %q, %v", got, err)
	}
	// XDG next.
	if got, err := resolveDataRoot(env(map[string]string{"XDG_DATA_HOME": "/xdg"}), home("/home/u", nil)); err != nil || got != filepath.Join("/xdg", "hukou") {
		t.Fatalf("xdg = %q, %v", got, err)
	}
	// HOME fallback.
	if got, err := resolveDataRoot(env(map[string]string{}), home("/home/u", nil)); err != nil || got != filepath.Join("/home/u", ".local", "share", "hukou") {
		t.Fatalf("home = %q, %v", got, err)
	}
	// Nothing available: an error, never the old relative "./hukou" fallback.
	if _, err := resolveDataRoot(env(map[string]string{}), home("", nil)); err == nil {
		t.Fatal("expected an error when HOME and XDG_DATA_HOME are both unavailable")
	}
}

// resolveDataRoot must refuse a relative HUKOU_DATA_DIR with the same error
// class as the no-HOME case: a relative root scatters state into whatever
// directory hukou was started from.
func TestResolveDataRootRejectsRelativeHUKOUDataDir(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	home := func(h string, err error) func() (string, error) {
		return func() (string, error) { return h, err }
	}
	for _, value := range []string{"relative/root", "./hukou", "../hukou", "hukou"} {
		_, err := resolveDataRoot(env(map[string]string{"HUKOU_DATA_DIR": value}), home("/home/u", nil))
		if err == nil {
			t.Fatalf("HUKOU_DATA_DIR=%q: expected an error for a relative path", value)
		}
		if !strings.Contains(err.Error(), "absolute path") {
			t.Fatalf("HUKOU_DATA_DIR=%q: error must say the path must be absolute, got: %v", value, err)
		}
	}
}

// checkDataRootTrust is the fail-closed gate on the data directory: real dir,
// owned by the effective user, not group/other-writable.
func TestCheckDataRootTrust(t *testing.T) {
	newDir := func(t *testing.T, perm os.FileMode) string {
		t.Helper()
		dir := filepath.Join(t.TempDir(), "root")
		if err := os.Mkdir(dir, perm); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	t.Run("accepts private user-owned 0700", func(t *testing.T) {
		dir := newDir(t, 0o700)
		if err := checkDataRootTrust(dir); err != nil {
			t.Fatalf("expected 0700 dir to be accepted, got: %v", err)
		}
	})
	t.Run("accepts private user-owned 0755", func(t *testing.T) {
		dir := newDir(t, 0o755)
		if err := checkDataRootTrust(dir); err != nil {
			t.Fatalf("expected 0755 dir to be accepted, got: %v", err)
		}
	})
	t.Run("refuses symlinked root", func(t *testing.T) {
		real := newDir(t, 0o700)
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		err := checkDataRootTrust(link)
		if err == nil {
			t.Fatal("expected symlinked data root to be refused")
		}
		if !strings.Contains(err.Error(), link) || !strings.Contains(err.Error(), "symbolic link") {
			t.Fatalf("error must name the path and the reason, got: %v", err)
		}
	})
	t.Run("refuses regular file", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "notadir")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := checkDataRootTrust(file); err == nil {
			t.Fatal("expected a regular file as data root to be refused")
		} else if !strings.Contains(err.Error(), file) {
			t.Fatalf("error must name the path, got: %v", err)
		}
	})
	t.Run("refuses group-writable dir", func(t *testing.T) {
		dir := newDir(t, 0o700)
		if err := os.Chmod(dir, 0o770); err != nil { // explicit: Mkdir applies umask
			t.Fatal(err)
		}
		err := checkDataRootTrust(dir)
		if err == nil {
			t.Fatal("expected group-writable data root to be refused")
		}
		if !strings.Contains(err.Error(), dir) || !strings.Contains(err.Error(), "writable") {
			t.Fatalf("error must name the path and the reason, got: %v", err)
		}
	})
	t.Run("refuses other-writable dir", func(t *testing.T) {
		dir := newDir(t, 0o700)
		if err := os.Chmod(dir, 0o702); err != nil { // explicit: Mkdir applies umask
			t.Fatal(err)
		}
		if err := checkDataRootTrust(dir); err == nil {
			t.Fatal("expected other-writable data root to be refused")
		}
	})
	t.Run("refuses foreign-owned dir", func(t *testing.T) {
		dir := newDir(t, 0o700)
		origUID := dataRootFileUID
		dataRootFileUID = func(fi os.FileInfo) int { return os.Getuid() + 1234 }
		t.Cleanup(func() { dataRootFileUID = origUID })
		err := checkDataRootTrust(dir)
		if err == nil {
			t.Fatal("expected foreign-owned data root to be refused")
		}
		if !strings.Contains(err.Error(), "elevated privileges") {
			t.Fatalf("error must say elevated privileges against another user's data root are unsupported, got: %v", err)
		}
	})
	t.Run("refuses when running as another uid (sudo direction)", func(t *testing.T) {
		dir := newDir(t, 0o700)
		origEUID := effectiveUID
		effectiveUID = func() int { return 0 }
		t.Cleanup(func() { effectiveUID = origEUID })
		err := checkDataRootTrust(dir)
		if err == nil {
			t.Fatal("expected a user-owned root to be refused under an elevated uid")
		}
		if !strings.Contains(err.Error(), "elevated privileges") {
			t.Fatalf("error must say elevated privileges against another user's data root are unsupported, got: %v", err)
		}
	})
	t.Run("refuses missing path instead of guessing", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone")
		if err := checkDataRootTrust(missing); err == nil {
			t.Fatal("expected an uninspectable data root to be refused")
		}
	})
}

// acquireMutationLock must fail closed before touching the lock when the data
// root fails the trust gate — no state.lock may appear in an untrusted root.
func TestAcquireMutationLockRefusesUntrustedDataRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o770); err != nil { // explicit: Mkdir applies umask
		t.Fatal(err)
	}
	t.Setenv("HUKOU_DATA_DIR", root)
	lock, err := acquireMutationLock(io.Discard)
	if err == nil {
		_ = lock.Release()
		t.Fatal("expected acquireMutationLock to refuse a group-writable data root")
	}
	if !strings.Contains(err.Error(), root) {
		t.Fatalf("error must name the data root, got: %v", err)
	}
	if _, statErr := os.Lstat(filepath.Join(root, "state.lock")); !os.IsNotExist(statErr) {
		t.Fatalf("no state.lock may be created in an untrusted root (stat err: %v)", statErr)
	}
}

// The happy path keeps working: a normal private TempDir root acquires and
// releases cleanly through the gated function.
func TestAcquireMutationLockAcceptsPrivateRoot(t *testing.T) {
	root := t.TempDir() // 0700, owned by the test's euid
	t.Setenv("HUKOU_DATA_DIR", root)
	lock, err := acquireMutationLock(io.Discard)
	if err != nil {
		t.Fatalf("expected a private user-owned root to be accepted, got: %v", err)
	}
	releaseMutationLock(lock, io.Discard)
}
