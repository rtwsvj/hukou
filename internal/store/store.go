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
// Temporary activation files are created in the live path's directory so the
// final rename stays on one filesystem and atomically exposes either the old
// regular file or the new regular file.
type Store struct {
	Root string
}

func (s *Store) absRoot() (string, error) {
	if filepath.IsAbs(s.Root) {
		return s.Root, nil
	}
	return filepath.Abs(s.Root)
}

// storeDir resolves directory components below the configured store root.
// The root itself is the caller's trust anchor and may be a deliberate symlink,
// but every child component must have the requested spelling, be a real
// directory, and never be a symlink. This prevents a pre-positioned name/tag
// symlink from redirecting Put, AdoptOriginal, Versions, or Prune outside Root.
func (s *Store) storeDir(create bool, parts ...string) (string, error) {
	root, err := s.absRoot()
	if err != nil {
		return "", err
	}
	if create {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() {
		return "", fmt.Errorf("store root is not a directory: %s", root)
	}

	current := root
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part || strings.ContainsAny(part, `/\`) {
			return "", fmt.Errorf("invalid store directory component %q", part)
		}
		current, err = resolveStoreChild(current, part, create)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

func resolveStoreChild(parent, requested string, create bool) (string, error) {
	path := filepath.Join(parent, requested)
	for attempt := 0; attempt < 2; attempt++ {
		entries, err := os.ReadDir(parent)
		if err != nil {
			return "", err
		}
		for _, entry := range entries {
			if !strings.EqualFold(entry.Name(), requested) {
				continue
			}
			if entry.Name() != requested {
				return "", fmt.Errorf("store directory %q conflicts with case alias %q", requested, entry.Name())
			}
			info, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf("store directory must not be a symlink: %s", path)
			}
			if !info.IsDir() {
				return "", fmt.Errorf("store path is not a directory: %s", path)
			}
			return path, nil
		}
		if !create {
			return "", &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
		}
		if err := os.Mkdir(path, 0o755); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			return "", err
		}
		// A concurrent creator won. Re-read once and validate its spelling and
		// topology instead of following it implicitly.
	}
	return "", fmt.Errorf("store directory changed concurrently: %s", path)
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
	if kind == "name" && strings.EqualFold(v, ".tmp") {
		return fmt.Errorf("invalid name %q: reserved store namespace", v)
	}
	if kind == "tag" && strings.EqualFold(v, "original") {
		return fmt.Errorf("invalid tag %q: reserved immutable backup namespace", v)
	}
	return nil
}

func validateActivationTag(tag string) error {
	if tag == "original" {
		return nil
	}
	return validateNameTag("tag", tag)
}

// ValidateName validates a public tool name against the store namespace.
func ValidateName(name string) error { return validateNameTag("name", name) }

// ValidateTag validates a user/release version tag. The reserved original tag
// is intentionally rejected; only rollback's internal activation path may use it.
func ValidateTag(tag string) error { return validateNameTag("tag", tag) }

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
// The write is staged through <Root>/.tmp and committed with a no-replace hard
// link so an existing version can never be overwritten.
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

	if _, err := s.storeDir(true, name); err != nil {
		return err
	}
	dstDir, err := s.storeDir(false, name, tag)
	if err == nil {
		dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
		entries, err := os.ReadDir(dstDir)
		if err != nil {
			return err
		}
		if len(entries) != 1 || entries[0].Name() != filepath.Base(srcPath) || !entries[0].Type().IsRegular() {
			return fmt.Errorf("version %s/%s already exists with unexpected contents", name, tag)
		}
		srcSHA, err := SHA256File(srcPath)
		if err != nil {
			return err
		}
		dstSHA, err := SHA256File(dstPath)
		if err != nil {
			return err
		}
		if srcSHA != dstSHA {
			return fmt.Errorf("immutable version %s/%s already exists with different content", name, tag)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}

	tmpDir, err := s.storeDir(true, ".tmp")
	if err != nil {
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

	copiedHash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, copiedHash), src); err != nil {
		_ = tmpFile.Close()
		return err
	}
	sourceSHA, err := SHA256File(srcPath)
	if err != nil {
		_ = tmpFile.Close()
		return err
	}
	if copiedSHA := hex.EncodeToString(copiedHash.Sum(nil)); copiedSHA != sourceSHA {
		_ = tmpFile.Close()
		return fmt.Errorf("source changed while storing %s/%s", name, tag)
	}
	sourceInfoAfter, err := os.Stat(srcPath)
	if err != nil {
		_ = tmpFile.Close()
		return err
	}
	if sourceInfoAfter.Mode().Perm() != srcInfo.Mode().Perm() {
		_ = tmpFile.Close()
		return fmt.Errorf("source mode changed while storing %s/%s", name, tag)
	}
	if err := tmpFile.Chmod(srcInfo.Mode().Perm()); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Sync(); err != nil {
		_ = tmpFile.Close()
		return err
	}
	if err := tmpFile.Close(); err != nil {
		return err
	}

	dstDir, err = s.storeDir(true, name, tag)
	if err != nil {
		return err
	}
	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	// Link the completed staging inode into the final name. Unlike Rename,
	// Link fails with EEXIST and therefore cannot overwrite a version that
	// appeared between the initial existence check and this commit point.
	if err := os.Link(tmpPath, dstPath); err != nil {
		return fmt.Errorf("commit immutable version %s/%s: %w", name, tag, err)
	}
	// Keep cleanup=true: the deferred remove unlinks the staging name while the
	// final hard link remains. A failed cleanup is harmless to the committed
	// immutable version and will be retried by GC.
	return nil
}

// Versions returns the list of installed tags for name, sorted lexicographically.
// The "original" directory is never treated as a version.
func (s *Store) Versions(name string) ([]string, error) {
	if err := validateNameTag("name", name); err != nil {
		return nil, err
	}
	nameDir, err := s.storeDir(false, name)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		return nil, err
	}

	var tags []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), "original") {
			if e.Name() != "original" {
				return nil, fmt.Errorf("reserved original directory has non-canonical spelling %q", e.Name())
			}
			continue
		}
		if _, err := s.storeDir(false, name, e.Name()); err != nil {
			return nil, err
		}
		tags = append(tags, e.Name())
	}
	sort.Strings(tags)
	return tags, nil
}

// Activate copies the immutable store binary into a temporary regular file in
// livePath's directory, then renames it over livePath. Keeping the live path a
// regular file avoids platform-specific transient failures observed while a
// symlink inode is replaced concurrently on macOS/APFS.
func (s *Store) Activate(name, tag, livePath string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}
	if err := validateActivationTag(tag); err != nil {
		return err
	}

	tagDir, err := s.storeDir(false, name, tag)
	if err != nil {
		return err
	}
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

	liveDir := filepath.Dir(livePath)
	if err := os.MkdirAll(liveDir, 0o755); err != nil {
		return err
	}

	return atomicCopyFile(absTarget, livePath, liveDir)
}

// AdoptOriginal copies the regular file at binPath into the store's
// <name>/original/ backup directory without changing the live path.
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
	if srcInfo.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fmt.Errorf("special mode bits are not supported for original backups: %s", binPath)
	}

	origDir, err := s.storeDir(true, name, "original")
	if err != nil {
		return err
	}

	dstPath := filepath.Join(origDir, filepath.Base(binPath))
	if _, err := os.Lstat(dstPath); err == nil {
		return fmt.Errorf("original backup already exists: %s", dstPath)
	} else if !os.IsNotExist(err) {
		return err
	}

	// Stage under the store-wide .tmp directory. It is on the same filesystem
	// as original/, so the final no-replace hard link is atomic; an interrupted
	// cleanup is also recoverable by the normal GC path.
	tmpDir, err := s.storeDir(true, ".tmp")
	if err != nil {
		return err
	}
	return atomicCopyFileNoReplace(binPath, dstPath, tmpDir)
}

// Prune removes old versions of name, keeping the most recent keep versions by
// directory modification time. The original backup directory is never removed.
// protectedTag, when non-empty, is verified against protectedSHA before any
// deletion and is never removed.
func (s *Store) Prune(name string, keep int, protectedTag, protectedSHA string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}
	if protectedTag != "" {
		if err := validateActivationTag(protectedTag); err != nil {
			return err
		}
		if protectedSHA == "" {
			return fmt.Errorf("protected tag %q requires a SHA-256", protectedTag)
		}
		if err := s.verifyVersionSHA(name, protectedTag, protectedSHA); err != nil {
			return fmt.Errorf("verify protected version: %w", err)
		}
	} else if protectedSHA != "" {
		return fmt.Errorf("protected SHA-256 requires a tag")
	}
	if keep < 0 {
		keep = 0
	}

	nameDir, err := s.storeDir(false, name)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		return err
	}

	type version struct {
		tag   string
		mtime time.Time
	}
	var versions []version

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), "original") {
			if e.Name() != "original" {
				return fmt.Errorf("reserved original directory has non-canonical spelling %q", e.Name())
			}
			continue
		}
		if _, err := s.storeDir(false, name, e.Name()); err != nil {
			return err
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
		if versions[i].tag == protectedTag {
			continue
		}
		versionDir, err := s.storeDir(false, name, versions[i].tag)
		if err != nil {
			return err
		}
		if err := os.RemoveAll(versionDir); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) verifyVersionSHA(name, tag, wantSHA string) error {
	dir, err := s.storeDir(false, name, tag)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var binary string
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		if binary != "" {
			return fmt.Errorf("version %s/%s contains multiple binaries", name, tag)
		}
		binary = filepath.Join(dir, entry.Name())
	}
	if binary == "" {
		return fmt.Errorf("no binary found in version %s/%s", name, tag)
	}
	gotSHA, err := SHA256File(binary)
	if err != nil {
		return err
	}
	if gotSHA != wantSHA {
		return fmt.Errorf("version %s/%s SHA-256 mismatch: got %s, want %s", name, tag, gotSHA, wantSHA)
	}
	return nil
}

// atomicCopyFile copies srcPath into a complete temporary regular file beside
// dstPath, fsyncs and closes it, then atomically renames it over dstPath.
func atomicCopyFile(srcPath, dstPath, dstDir string) error {
	return atomicCopyFileWithMode(srcPath, dstPath, dstDir, true)
}

// atomicCopyFileNoReplace installs a completed copy only if dstPath does not
// already exist. The final hard-link operation is atomic and fails closed on a
// competing creator, which keeps the original backup write-once.
func atomicCopyFileNoReplace(srcPath, dstPath, stagingDir string) error {
	return atomicCopyFileWithMode(srcPath, dstPath, stagingDir, false)
}

func atomicCopyFileWithMode(srcPath, dstPath, stagingDir string, replace bool) error {
	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()
	srcInfo, err := src.Stat()
	if err != nil {
		return err
	}
	if !srcInfo.Mode().IsRegular() {
		return fmt.Errorf("activation source is not a regular file: %s", srcPath)
	}

	tmp, err := os.CreateTemp(stagingDir, ".hukou-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	copiedHash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, copiedHash), src); err != nil {
		_ = tmp.Close()
		return err
	}
	sourceSHA, err := SHA256File(srcPath)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if copiedSHA := hex.EncodeToString(copiedHash.Sum(nil)); copiedSHA != sourceSHA {
		_ = tmp.Close()
		return fmt.Errorf("activation source changed while copying: %s", srcPath)
	}
	sourceInfoAfter, err := os.Stat(srcPath)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if sourceInfoAfter.Mode().Perm() != srcInfo.Mode().Perm() {
		_ = tmp.Close()
		return fmt.Errorf("activation source mode changed while copying: %s", srcPath)
	}
	if err := tmp.Chmod(srcInfo.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if replace {
		if err := os.Rename(tmpPath, dstPath); err != nil {
			return err
		}
		cleanup = false
		return nil
	}
	if err := os.Link(tmpPath, dstPath); err != nil {
		return fmt.Errorf("commit without replacing %s: %w", dstPath, err)
	}
	// The deferred cleanup removes only the staging name; dstPath is a distinct
	// hard link to the completed inode.
	return nil
}

// GC removes all contents of <Root>/.tmp/ and recreates the directory.
func (s *Store) GC() error {
	tmpDir, err := s.storeDir(true, ".tmp")
	if err != nil {
		return err
	}
	if err := os.RemoveAll(tmpDir); err != nil {
		return err
	}
	return os.Mkdir(tmpDir, 0o755)
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
