package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/state"
	"github.com/rtwsvj/hukou/internal/store"
)

// dataRoot returns the hukou data directory.
// HUKOU_DATA_DIR overrides the XDG Data Home default.
func dataRoot() string {
	if v := os.Getenv("HUKOU_DATA_DIR"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	xdg := os.Getenv("XDG_DATA_HOME")
	if xdg == "" && home != "" {
		xdg = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(xdg, "hukou")
}

func manifestPath() string { return filepath.Join(dataRoot(), "manifest.json") }
func storeRoot() string    { return filepath.Join(dataRoot(), "store") }

func newStore() *store.Store { return &store.Store{Root: storeRoot()} }

func acquireMutationLock() (*state.Lock, error) {
	if err := os.MkdirAll(dataRoot(), 0o755); err != nil {
		return nil, err
	}
	return state.Acquire(filepath.Join(dataRoot(), "state.lock"))
}

func releaseMutationLock(lock *state.Lock, stderr io.Writer) {
	if err := lock.Release(); err != nil {
		if stderr != nil {
			fmt.Fprintf(stderr, "警告: 释放 hukou 状态锁失败: %v\n", err)
		}
	}
}

func loadManifest() (*manifest.Manifest, error) {
	return manifest.Load(manifestPath())
}

func saveManifest(m *manifest.Manifest) error {
	if err := os.MkdirAll(filepath.Dir(manifestPath()), 0o755); err != nil {
		return err
	}
	return m.Save(manifestPath())
}

func rfc3339Now() string { return time.Now().Format(time.RFC3339) }

func firstEnv(names ...string) string {
	for _, n := range names {
		if v := os.Getenv(n); v != "" {
			return v
		}
	}
	return ""
}

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
	if warnings := runner.Load(env); len(warnings) > 0 {
		return nil, fmt.Errorf("load provenance security gate: %s", strings.Join(warnings, "; "))
	}
	return runner.Match(b), nil
}

func copyFile(src, dst string, mode os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(df, sf); err != nil {
		_ = df.Close()
		return err
	}
	return df.Close()
}

func activateOriginal(s *store.Store, name, linkPath string) error {
	origBin := filepath.Join(s.Root, name, "original", name)
	target, err := filepath.Abs(origBin)
	if err != nil {
		return err
	}
	linkDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return err
	}

	tmpPath := filepath.Join(linkDir, ".hukou-activate-"+name+"-tmp")
	_ = os.Remove(tmpPath)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, linkPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("atomic replace symlink: %w", err)
	}
	return nil
}
