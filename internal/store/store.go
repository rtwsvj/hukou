package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Store manages versioned binary artifacts under Root.
//
// Layout:
//
//	<Root>/<name>/<tag>/<bin>
//	<Root>/<name>/original/<bin>
//	<Root>/.tmp/            staging area for atomic file copies
//
// Temporary symlinks for Activate/AdoptOriginal are created as hidden names
// in the same directory as the destination link (required for atomic rename
// across filesystems), not under .tmp/.
type Store struct {
	Root string
}

func (s *Store) absRoot() (string, error) {
	if filepath.IsAbs(s.Root) {
		return s.Root, nil
	}
	return filepath.Abs(s.Root)
}

// validateNameTag rejects empty values and path traversal components.
// Manifest entries are semi-trusted input and must not escape the store layout.
func validateNameTag(kind, v string) error {
	if v == "" {
		return fmt.Errorf("empty %s", kind)
	}
	if v == "." || v == ".." {
		return fmt.Errorf("invalid %s %q", kind, v)
	}
	if strings.Contains(v, "..") {
		return fmt.Errorf("invalid %s %q: contains '..'", kind, v)
	}
	if strings.ContainsAny(v, `/\`) {
		return fmt.Errorf("invalid %s %q: path separator not allowed", kind, v)
	}
	if strings.ContainsRune(v, filepath.Separator) {
		return fmt.Errorf("invalid %s %q: path separator not allowed", kind, v)
	}
	return nil
}

// ensureUnderRoot returns an absolute path for p and verifies it resolves
// strictly inside the store root (or is the root itself).
func (s *Store) ensureUnderRoot(p string) (string, error) {
	root, err := s.absRoot()
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	// Resolve symlinks in the parent path when possible so a symlink escape
	// cannot place the target outside the store.
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	rootResolved := root
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		rootResolved = resolved
	}
	prefix := rootResolved
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if abs != rootResolved && !strings.HasPrefix(abs, prefix) {
		return "", fmt.Errorf("path %q escapes store root %q", abs, rootResolved)
	}
	return abs, nil
}

// Put copies srcPath into the store as <Root>/<name>/<tag>/<basename(srcPath)>.
// The write is staged through <Root>/.tmp and committed with os.Rename.
func (s *Store) Put(name, tag, srcPath string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}
	if err := validateNameTag("tag", tag); err != nil {
		return err
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("source is not a regular file: %s", srcPath)
	}

	root, err := s.absRoot()
	if err != nil {
		return err
	}

	tmpDir := filepath.Join(root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp(tmpDir, fmt.Sprintf("put-%s-%s-*", name, tag))
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if _, err := io.Copy(tmpFile, src); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	dstDir := filepath.Join(root, name, tag)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))

	if err := os.Rename(tmpPath, dstPath); err != nil {
		return err
	}
	if err := os.Chmod(dstPath, srcInfo.Mode()); err != nil {
		return err
	}

	cleanup = false
	return nil
}

// Versions returns the list of installed tags for name, sorted lexicographically.
// The "original" directory is never treated as a version.
func (s *Store) Versions(name string) ([]string, error) {
	root, err := s.absRoot()
	if err != nil {
		return nil, err
	}

	nameDir := filepath.Join(root, name)
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	var tags []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "original" {
			continue
		}
		tags = append(tags, e.Name())
	}
	sort.Strings(tags)
	return tags, nil
}

// Activate creates a symlink at linkPath that points to the binary stored as
// <name>/<tag>. The symlink is created atomically by building a temporary
// symlink in linkPath's parent directory and renaming it into place.
func (s *Store) Activate(name, tag, linkPath string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}
	if err := validateNameTag("tag", tag); err != nil {
		return err
	}

	root, err := s.absRoot()
	if err != nil {
		return err
	}

	tagDir := filepath.Join(root, name, tag)
	entries, err := os.ReadDir(tagDir)
	if err != nil {
		return err
	}

	var binName string
	for _, e := range entries {
		if e.Type().IsRegular() {
			if binName != "" {
				return fmt.Errorf("version %s/%s contains multiple binaries", name, tag)
			}
			binName = e.Name()
		}
	}
	if binName == "" {
		return fmt.Errorf("no binary found in version %s/%s", name, tag)
	}

	target := filepath.Join(tagDir, binName)
	absTarget, err := s.ensureUnderRoot(target)
	if err != nil {
		return fmt.Errorf("activate target: %w", err)
	}

	linkDir := filepath.Dir(linkPath)
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return err
	}

	return atomicSymlink(absTarget, linkPath, linkDir)
}

// AdoptOriginal moves the regular file at binPath into the store's
// <name>/original/ backup directory and leaves a symlink in binPath's original
// location pointing to the backup. The symlink replacement is atomic.
func (s *Store) AdoptOriginal(name, binPath string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}

	srcInfo, err := os.Lstat(binPath)
	if err != nil {
		return err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cannot adopt a symlink: %s", binPath)
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("not a regular file: %s", binPath)
	}

	root, err := s.absRoot()
	if err != nil {
		return err
	}

	origDir := filepath.Join(root, name, "original")
	if err := os.MkdirAll(origDir, 0o755); err != nil {
		return err
	}

	dstPath := filepath.Join(origDir, filepath.Base(binPath))
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("original backup already exists: %s", dstPath)
	}

	if err := os.Rename(binPath, dstPath); err != nil {
		return err
	}

	target, err := filepath.Abs(dstPath)
	if err != nil {
		// best-effort restore
		_ = os.Rename(dstPath, binPath)
		return err
	}

	binDir := filepath.Dir(binPath)
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		_ = os.Rename(dstPath, binPath)
		return err
	}

	if err := atomicSymlink(target, binPath, binDir); err != nil {
		_ = os.Rename(dstPath, binPath)
		return err
	}
	return nil
}

// activeVersionTag resolves linkPath (if it is a symlink into the store) and
// returns the version directory name (tag or "original") that it points at.
// Returns "" when the link is missing, not a symlink, or does not point into
// this store's <name>/ tree.
func (s *Store) activeVersionTag(name, linkPath string) string {
	if linkPath == "" {
		return ""
	}
	root, err := s.absRoot()
	if err != nil {
		return ""
	}
	// Resolve root (macOS /var -> /private/var) so Rel against symlink targets works.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	info, err := os.Lstat(linkPath)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return ""
	}
	target, err := os.Readlink(linkPath)
	if err != nil {
		return ""
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(absTarget); err == nil {
		absTarget = resolved
	} else if resolved, err := filepath.EvalSymlinks(filepath.Dir(absTarget)); err == nil {
		// Target file may have been deleted; still resolve parent.
		absTarget = filepath.Join(resolved, filepath.Base(absTarget))
	}
	nameDir := filepath.Join(root, name)
	rel, err := filepath.Rel(nameDir, absTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return ""
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) == 0 || parts[0] == "" {
		return ""
	}
	return parts[0]
}

// Prune removes old versions of name, keeping the most recent keep versions by
// directory modification time. The original backup directory is never removed.
// When linkPath is non-empty and is a symlink into the store for this name,
// the currently active version is also never removed (same protection tier as
// original).
func (s *Store) Prune(name string, keep int, linkPath string) error {
	if keep < 0 {
		keep = 0
	}

	root, err := s.absRoot()
	if err != nil {
		return err
	}

	nameDir := filepath.Join(root, name)
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	protected := s.activeVersionTag(name, linkPath)

	type version struct {
		tag   string
		mtime time.Time
	}
	var versions []version

	for _, e := range entries {
		if !e.IsDir() || e.Name() == "original" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		versions = append(versions, version{tag: e.Name(), mtime: info.ModTime()})
	}

	sort.Slice(versions, func(i, j int) bool {
		return versions[i].mtime.After(versions[j].mtime)
	})

	for i := keep; i < len(versions); i++ {
		if versions[i].tag == protected {
			continue
		}
		if err := os.RemoveAll(filepath.Join(nameDir, versions[i].tag)); err != nil {
			return err
		}
	}
	return nil
}

// GC removes all contents of <Root>/.tmp/ and recreates the directory.
func (s *Store) GC() error {
	root, err := s.absRoot()
	if err != nil {
		return err
	}
	tmpDir := filepath.Join(root, ".tmp")
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	return os.MkdirAll(tmpDir, 0o755)
}

// SHA256File returns the hex-encoded SHA-256 digest of the file at path.
func SHA256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// atomicSymlink creates a symlink at linkPath pointing to target. It builds the
// symlink as a hidden temporary name in the same directory as linkPath
// (.hukou-tmp-*) and renames it into place so that linkPath is always observed
// as either the old link or the new link. Same-directory rename is required for
// atomicity across filesystems.
func atomicSymlink(target, linkPath, linkDir string) error {
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(linkDir, ".hukou-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Remove(tmpPath); err != nil {
		return err
	}

	if err := os.Symlink(target, tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	if err := os.Rename(tmpPath, linkPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
