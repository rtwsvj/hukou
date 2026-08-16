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
	"sync/atomic"

	"github.com/rtwsvj/hukou/internal/durablefs"
	"github.com/rtwsvj/hukou/internal/i18n"
)

// sha256FileCalls counts whole-file SHA-256 computations performed by
// SHA256File. It is a diagnostics/benchmark accelerator only: no production
// decision reads it, and the single atomic increment is negligible next to
// reading and hashing an entire file. Benchmarks use it to measure how many
// redundant full-file passes a refactor removes.
var sha256FileCalls atomic.Uint64

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
	fs   durableOperations
}

type durableOperations interface {
	SyncFile(*os.File) error
	SyncDir(string) error
	Mkdir(string, os.FileMode) error
	MkdirAll(string, os.FileMode) error
	Rename(string, string) error
	Link(string, string) error
	Remove(string) error
	RemoveAll(string) error
}

func (s *Store) durability() durableOperations {
	if s.fs != nil {
		return s.fs
	}
	return durablefs.FileSystem{}
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
		if err := s.durability().MkdirAll(root, 0o755); err != nil {
			return "", err
		}
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !rootInfo.IsDir() {
		return "", i18n.Errorf("store root is not a directory: %s", root)
	}

	current := root
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || filepath.Base(part) != part || strings.ContainsAny(part, `/\`) {
			return "", i18n.Errorf("invalid store directory component %q", part)
		}
		current, err = resolveStoreChild(s.durability(), current, part, create)
		if err != nil {
			return "", err
		}
	}
	return current, nil
}

func resolveStoreChild(fs durableOperations, parent, requested string, create bool) (string, error) {
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
				return "", i18n.Errorf("store directory %q conflicts with case alias %q", requested, entry.Name())
			}
			info, err := os.Lstat(path)
			if err != nil {
				return "", err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return "", i18n.Errorf("store directory must not be a symlink: %s", path)
			}
			if !info.IsDir() {
				return "", i18n.Errorf("store path is not a directory: %s", path)
			}
			// Mutating callers may be retrying a mkdir that became visible before
			// its parent sync completed. Read-only callers must not turn a lookup
			// into a filesystem sync merely by inspecting the store.
			if create {
				if err := fs.SyncDir(parent); err != nil {
					return "", i18n.Wrapf("persist existing store directory %s: %w", err, path)
				}
			}
			return path, nil
		}
		if !create {
			return "", &os.PathError{Op: "lstat", Path: path, Err: os.ErrNotExist}
		}
		if err := fs.Mkdir(path, 0o755); err == nil {
			return path, nil
		} else if !os.IsExist(err) {
			return "", err
		}
		// A concurrent creator won. Re-read once and validate its spelling and
		// topology instead of following it implicitly.
	}
	return "", i18n.Errorf("store directory changed concurrently: %s", path)
}

// validateNameTag rejects empty values and path traversal components.
// Manifest entries are semi-trusted input and must not escape the store layout.
func validateNameTag(kind, v string) error {
	if v == "" {
		return i18n.Errorf("empty %s", kind)
	}
	if strings.TrimSpace(v) == "" {
		return i18n.Errorf("invalid %s %q: whitespace only", kind, v)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f {
			return i18n.Errorf("invalid %s %q: control character not allowed", kind, v)
		}
	}
	if v == "." || v == ".." {
		return i18n.Errorf("invalid %s %q", kind, v)
	}
	if strings.Contains(v, "..") {
		return i18n.Errorf("invalid %s %q: contains '..'", kind, v)
	}
	if strings.ContainsAny(v, `/\`) {
		return i18n.Errorf("invalid %s %q: path separator not allowed", kind, v)
	}
	if strings.ContainsRune(v, filepath.Separator) {
		return i18n.Errorf("invalid %s %q: path separator not allowed", kind, v)
	}
	if kind == "name" && strings.EqualFold(v, ".tmp") {
		return i18n.Errorf("invalid name %q: reserved store namespace", v)
	}
	if kind == "name" && strings.EqualFold(v, "original") {
		return i18n.Errorf("invalid name %q: reserved immutable backup namespace", v)
	}
	if kind == "tag" && strings.EqualFold(v, "original") {
		return i18n.Errorf("invalid tag %q: reserved immutable backup namespace", v)
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
		return "", i18n.Errorf("path %q escapes store root %q", abs, rootResolved)
	}
	return abs, nil
}

// Put copies srcPath into the store as <Root>/<name>/<tag>/<basename(srcPath)>.
// The write is staged through <Root>/.tmp and committed with a no-replace hard
// link so an existing version can never be overwritten.
func (s *Store) Put(name, tag, srcPath string) error {
	_, err := s.PutWithDigest(name, tag, srcPath)
	return err
}

// PutWithDigest behaves exactly like Put and additionally returns the content
// SHA-256 of the stored artifact. The digest is a by-product of the copy the
// store already performs (and cross-checks against a fresh read of the source),
// so callers that need to activate or record the just-stored version can reuse
// it instead of hashing the immutable store file a second time. The digest is
// only returned on success; any error yields "".
func (s *Store) PutWithDigest(name, tag, srcPath string) (string, error) {
	fs := s.durability()
	if err := validateNameTag("name", name); err != nil {
		return "", err
	}
	if err := validateNameTag("tag", tag); err != nil {
		return "", err
	}

	srcInfo, err := os.Stat(srcPath)
	if err != nil {
		return "", err
	}
	if !srcInfo.Mode().IsRegular() {
		return "", i18n.Errorf("source is not a regular file: %s", srcPath)
	}

	if _, err := s.storeDir(true, name); err != nil {
		return "", err
	}
	// A prior process may have durably created the final version directory and
	// then exited before linking the completed file into it. An empty, validated
	// directory is therefore a recoverable staging state; any other unexpected
	// contents still fail closed.
	dstDir, err := s.storeDir(false, name, tag)
	if err == nil {
		entries, err := os.ReadDir(dstDir)
		if err != nil {
			return "", err
		}
		if len(entries) == 0 {
			// Continue below and install the staged inode with a no-replace link.
		} else if len(entries) != 1 || entries[0].Name() != filepath.Base(srcPath) || !entries[0].Type().IsRegular() {
			return "", i18n.Errorf("version %s/%s already exists with unexpected contents", name, tag)
		} else {
			dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
			srcSHA, err := SHA256File(srcPath)
			if err != nil {
				return "", err
			}
			dstSHA, err := SHA256File(dstPath)
			if err != nil {
				return "", err
			}
			if srcSHA != dstSHA {
				return "", i18n.Errorf("immutable version %s/%s already exists with different content", name, tag)
			}
			if err := syncExistingVersion(s.durability(), dstPath, dstDir); err != nil {
				return "", err
			}
			return dstSHA, nil
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}

	tmpDir, err := s.storeDir(true, ".tmp")
	if err != nil {
		return "", err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return "", err
	}
	defer src.Close()

	tmpFile, err := os.CreateTemp(tmpDir, fmt.Sprintf("put-%s-%s-*", name, tag))
	if err != nil {
		return "", err
	}
	tmpPath := tmpFile.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = fs.Remove(tmpPath)
		}
	}()

	copiedHash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, copiedHash), src); err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	sourceSHA, err := SHA256File(srcPath)
	if err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	copiedSHA := hex.EncodeToString(copiedHash.Sum(nil))
	if copiedSHA != sourceSHA {
		_ = tmpFile.Close()
		return "", i18n.Errorf("source changed while storing %s/%s", name, tag)
	}
	sourceInfoAfter, err := os.Stat(srcPath)
	if err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	if sourceInfoAfter.Mode().Perm() != srcInfo.Mode().Perm() {
		_ = tmpFile.Close()
		return "", i18n.Errorf("source mode changed while storing %s/%s", name, tag)
	}
	if err := tmpFile.Chmod(srcInfo.Mode().Perm()); err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	if err := fs.SyncFile(tmpFile); err != nil {
		_ = tmpFile.Close()
		return "", err
	}
	if err := tmpFile.Close(); err != nil {
		return "", err
	}

	if dstDir == "" {
		dstDir, err = s.storeDir(true, name, tag)
		if err != nil {
			return "", err
		}
	}
	dstPath := filepath.Join(dstDir, filepath.Base(srcPath))
	// Link the completed staging inode into the final name. Unlike Rename,
	// Link fails with EEXIST and therefore cannot overwrite a version that
	// appeared between the initial existence check and this commit point.
	if err := fs.Link(tmpPath, dstPath); err != nil {
		return "", i18n.Wrapf("commit immutable version %s/%s: %w", err, name, tag)
	}
	// Keep cleanup=true: the deferred remove unlinks the staging name while the
	// final hard link remains. A failed cleanup is harmless to the committed
	// immutable version and will be retried by GC.
	return copiedSHA, nil
}

// syncExistingVersion repairs the indeterminate edge where a previous hard
// link became visible before its parent directory sync completed. Both the
// inode and its directory entry are reaffirmed before an idempotent Put reports
// success.
func syncExistingVersion(fs durableOperations, path, dir string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()
		return statErr
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return i18n.Errorf("stored version is not a regular file: %s", path)
	}
	if err := fs.SyncFile(file); err != nil {
		_ = file.Close()
		return i18n.Wrapf("persist existing version file %s: %w", err, path)
	}
	if err := file.Close(); err != nil {
		return i18n.Wrapf("close existing version file %s: %w", err, path)
	}
	if err := fs.SyncDir(filepath.Dir(dir)); err != nil {
		return i18n.Wrapf("persist existing version directory entry %s: %w", err, dir)
	}
	if err := fs.SyncDir(dir); err != nil {
		return i18n.Wrapf("persist existing version directory %s: %w", err, dir)
	}
	return nil
}

// Versions returns the list of installed tags for name, sorted lexicographically.
// The "original" directory is never treated as a version.
func (s *Store) Versions(name string) ([]string, error) {
	if err := validateNameTag("name", name); err != nil {
		return nil, err
	}
	installed, err := s.installedVersions(name)
	if err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(installed))
	for tag := range installed {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	return tags, nil
}

// Original returns the immutable backup reference for name after validating
// that the original namespace contains exactly one regular artifact. Commands
// that start from a manifest entry use this to distinguish a legitimately
// empty version list from a missing or malformed tool store.
func (s *Store) Original(name string) (VersionRef, error) {
	return s.inspectVersion(name, "original")
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
				return i18n.Errorf("version %s/%s contains multiple binaries", name, tag)
			}
			binName = e.Name()
		}
	}
	if binName == "" {
		return i18n.Errorf("no binary found in version %s/%s", name, tag)
	}

	target := filepath.Join(tagDir, binName)
	absTarget, err := s.ensureUnderRoot(target)
	if err != nil {
		return i18n.Wrapf("activate target: %w", err)
	}

	liveDir := filepath.Dir(livePath)
	if err := s.durability().MkdirAll(liveDir, 0o755); err != nil {
		return err
	}

	return atomicCopyFile(s.durability(), absTarget, livePath, liveDir)
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
		return i18n.Errorf("cannot adopt a symlink: %s", binPath)
	}
	if !srcInfo.Mode().IsRegular() {
		return i18n.Errorf("not a regular file: %s", binPath)
	}
	if srcInfo.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return i18n.Errorf("special mode bits are not supported for original backups: %s", binPath)
	}

	origDir, err := s.storeDir(true, name, "original")
	if err != nil {
		return err
	}

	dstPath := filepath.Join(origDir, filepath.Base(binPath))
	if _, err := os.Lstat(dstPath); err == nil {
		return i18n.Errorf("original backup already exists: %s", dstPath)
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
	return atomicCopyFileNoReplace(s.durability(), binPath, dstPath, tmpDir)
}

// VersionRef binds a store tag to its immutable content digest.
type VersionRef struct {
	Tag    string
	SHA256 string
}

// PruneRequest describes the state that must remain available after pruning.
// Ancestors must be ordered nearest-first along the logical activation lineage.
type PruneRequest struct {
	Name            string
	Current         VersionRef
	PinnedTag       string
	Ancestors       []VersionRef
	RetainAncestors int
}

// PrunePlan is a fully-bound, deterministic deletion plan. Delete entries carry
// their observed SHA-256 so ApplyPrunePlan can reject stale or replaced state
// before removing the first directory.
type PrunePlan struct {
	Name      string
	Current   VersionRef
	Protected []VersionRef
	Delete    []VersionRef
}

// PlanPrune protects the current version, an installed exact pin, and the first
// RetainAncestors logical ancestors. It never consults directory timestamps.
// Every protected artifact is verified before a deletion plan is returned.
func (s *Store) PlanPrune(request PruneRequest) (PrunePlan, error) {
	if err := validateNameTag("name", request.Name); err != nil {
		return PrunePlan{}, err
	}
	if request.RetainAncestors < 0 {
		return PrunePlan{}, i18n.Errorf("negative retained ancestor count")
	}
	if request.Current.Tag == "" || request.Current.SHA256 == "" {
		return PrunePlan{}, i18n.Errorf("current version requires a tag and SHA-256")
	}
	if err := validateActivationTag(request.Current.Tag); err != nil {
		return PrunePlan{}, i18n.Wrapf("current version: %w", err)
	}
	if request.PinnedTag != "" {
		if err := validateNameTag("tag", request.PinnedTag); err != nil {
			return PrunePlan{}, i18n.Wrapf("pinned version: %w", err)
		}
	}

	installed, err := s.installedVersions(request.Name)
	if err != nil {
		return PrunePlan{}, err
	}
	original, err := s.inspectVersion(request.Name, "original")
	if err != nil {
		return PrunePlan{}, i18n.Wrapf("verify immutable original backup: %w", err)
	}
	protected := make(map[string]VersionRef)
	addProtected := func(ref VersionRef, role string) error {
		if ref.Tag == "" || ref.SHA256 == "" {
			return i18n.Errorf("%s requires a tag and SHA-256", role)
		}
		if err := validateActivationTag(ref.Tag); err != nil {
			return i18n.Wrapf("%s: %w", err, role)
		}
		ref.SHA256 = strings.ToLower(ref.SHA256)
		if existing, ok := protected[ref.Tag]; ok && existing.SHA256 != ref.SHA256 {
			return i18n.Errorf("%s conflicts with protected tag %q SHA-256", role, ref.Tag)
		}
		protected[ref.Tag] = ref
		return nil
	}
	if err := addProtected(request.Current, "current version"); err != nil {
		return PrunePlan{}, err
	}
	if err := addProtected(original, "immutable original backup"); err != nil {
		return PrunePlan{}, err
	}
	ancestorCount := min(request.RetainAncestors, len(request.Ancestors))
	for i := 0; i < ancestorCount; i++ {
		if err := addProtected(request.Ancestors[i], fmt.Sprintf("activation ancestor %d", i+1)); err != nil {
			return PrunePlan{}, err
		}
	}
	if request.PinnedTag != "" {
		if pinned, ok := installed[request.PinnedTag]; ok {
			if err := addProtected(pinned, "pinned version"); err != nil {
				return PrunePlan{}, err
			}
		}
	}

	protectedRefs := make([]VersionRef, 0, len(protected))
	for _, ref := range protected {
		observed, err := s.inspectVersion(request.Name, ref.Tag)
		if err != nil {
			return PrunePlan{}, i18n.Wrapf("verify protected version %s: %w", err, ref.Tag)
		}
		if observed.SHA256 != ref.SHA256 {
			return PrunePlan{}, i18n.Errorf("verify protected version %s: SHA-256 mismatch: got %s, want %s", ref.Tag, observed.SHA256, ref.SHA256)
		}
		protectedRefs = append(protectedRefs, ref)
	}
	sort.Slice(protectedRefs, func(i, j int) bool { return protectedRefs[i].Tag < protectedRefs[j].Tag })

	deleteRefs := make([]VersionRef, 0, len(installed))
	for tag, ref := range installed {
		if _, keep := protected[tag]; keep {
			continue
		}
		deleteRefs = append(deleteRefs, ref)
	}
	sort.Slice(deleteRefs, func(i, j int) bool { return deleteRefs[i].Tag < deleteRefs[j].Tag })
	return PrunePlan{
		Name:      request.Name,
		Current:   VersionRef{Tag: request.Current.Tag, SHA256: strings.ToLower(request.Current.SHA256)},
		Protected: protectedRefs,
		Delete:    deleteRefs,
	}, nil
}

// ApplyPrunePlan revalidates every protected and deletion artifact before the
// first removal. New unlisted versions are left untouched; changed, missing,
// aliased, or malformed listed versions make the whole plan fail closed.
func (s *Store) ApplyPrunePlan(plan PrunePlan) error {
	if err := validateNameTag("name", plan.Name); err != nil {
		return err
	}
	if plan.Current.Tag == "" || plan.Current.SHA256 == "" {
		return i18n.Errorf("prune plan has no bound current version")
	}
	protected := make(map[string]string, len(plan.Protected))
	currentProtected := false
	for _, ref := range plan.Protected {
		if err := validateBoundRef(ref); err != nil {
			return i18n.Wrapf("invalid protected version: %w", err)
		}
		sha := strings.ToLower(ref.SHA256)
		if existing, ok := protected[ref.Tag]; ok && existing != sha {
			return i18n.Errorf("protected tag %q has conflicting SHA-256 values", ref.Tag)
		}
		protected[ref.Tag] = sha
		if ref.Tag == plan.Current.Tag && sha == strings.ToLower(plan.Current.SHA256) {
			currentProtected = true
		}
	}
	if !currentProtected {
		return i18n.Errorf("prune plan does not protect its current version")
	}

	deleteTags := make(map[string]struct{}, len(plan.Delete))
	for _, ref := range plan.Delete {
		if err := validateBoundRef(ref); err != nil {
			return i18n.Wrapf("invalid deletion version: %w", err)
		}
		if _, keep := protected[ref.Tag]; keep {
			return i18n.Errorf("version %q is both protected and scheduled for deletion", ref.Tag)
		}
		if _, duplicate := deleteTags[ref.Tag]; duplicate {
			return i18n.Errorf("duplicate deletion tag %q", ref.Tag)
		}
		deleteTags[ref.Tag] = struct{}{}
	}

	// Complete preflight: no removal occurs until every binding has been
	// re-read and verified.
	for tag, wantSHA := range protected {
		observed, err := s.inspectVersion(plan.Name, tag)
		if err != nil {
			return i18n.Wrapf("revalidate protected version %s: %w", err, tag)
		}
		if observed.SHA256 != wantSHA {
			return i18n.Errorf("revalidate protected version %s: SHA-256 mismatch: got %s, want %s", tag, observed.SHA256, wantSHA)
		}
	}
	for _, ref := range plan.Delete {
		observed, err := s.inspectVersion(plan.Name, ref.Tag)
		if err != nil {
			return i18n.Wrapf("revalidate deletion version %s: %w", err, ref.Tag)
		}
		if observed.SHA256 != strings.ToLower(ref.SHA256) {
			return i18n.Errorf("revalidate deletion version %s: SHA-256 mismatch: got %s, want %s", ref.Tag, observed.SHA256, ref.SHA256)
		}
	}

	for _, ref := range plan.Delete {
		versionDir, err := s.storeDir(false, plan.Name, ref.Tag)
		if err != nil {
			return err
		}
		if err := s.durability().RemoveAll(versionDir); err != nil {
			return err
		}
	}
	return nil
}

// PruneHistory plans and applies a history-aware prune. The caller must hold
// the same state lock used for activation transactions until this method
// returns.
func (s *Store) PruneHistory(request PruneRequest) error {
	plan, err := s.PlanPrune(request)
	if err != nil {
		return err
	}
	return s.ApplyPrunePlan(plan)
}

// Prune is retained for v0.2 callers. It is deterministic and no longer uses
// mtime, but it cannot infer activation lineage; new code must use
// PruneHistory. The lexicographically greatest keep tags are retained in
// addition to the explicitly protected version.
func (s *Store) Prune(name string, keep int, protectedTag, protectedSHA string) error {
	if err := validateNameTag("name", name); err != nil {
		return err
	}
	if protectedTag != "" {
		if err := validateActivationTag(protectedTag); err != nil {
			return err
		}
		if protectedSHA == "" {
			return i18n.Errorf("protected tag %q requires a SHA-256", protectedTag)
		}
		if err := s.verifyVersionSHA(name, protectedTag, protectedSHA); err != nil {
			return i18n.Wrapf("verify protected version: %w", err)
		}
	} else if protectedSHA != "" {
		return i18n.Errorf("protected SHA-256 requires a tag")
	}
	if keep < 0 {
		keep = 0
	}

	installed, err := s.installedVersions(name)
	if err != nil {
		return err
	}
	tags := make([]string, 0, len(installed))
	for tag := range installed {
		tags = append(tags, tag)
	}
	sort.Strings(tags)
	protected := make(map[string]struct{})
	if protectedTag != "" {
		protected[protectedTag] = struct{}{}
	}
	for i := max(0, len(tags)-keep); i < len(tags); i++ {
		protected[tags[i]] = struct{}{}
	}
	plan := PrunePlan{
		Name:      name,
		Current:   VersionRef{Tag: protectedTag, SHA256: strings.ToLower(protectedSHA)},
		Protected: make([]VersionRef, 0, len(protected)),
	}
	for tag := range protected {
		ref := installed[tag]
		plan.Protected = append(plan.Protected, ref)
	}
	for tag, ref := range installed {
		if _, keep := protected[tag]; !keep {
			plan.Delete = append(plan.Delete, ref)
		}
	}
	if protectedTag == "" {
		// The compatibility API permits an empty protection set. Bind a retained
		// tag as the synthetic current when possible; with keep=0 there is no
		// protected state and the compatibility removal is applied directly.
		if len(plan.Protected) == 0 {
			for _, ref := range plan.Delete {
				observed, inspectErr := s.inspectVersion(name, ref.Tag)
				if inspectErr != nil || observed.SHA256 != ref.SHA256 {
					if inspectErr != nil {
						return inspectErr
					}
					return i18n.Errorf("version %s changed while pruning", ref.Tag)
				}
			}
			for _, ref := range plan.Delete {
				versionDir, dirErr := s.storeDir(false, name, ref.Tag)
				if dirErr != nil {
					return dirErr
				}
				if removeErr := s.durability().RemoveAll(versionDir); removeErr != nil {
					return removeErr
				}
			}
			return nil
		}
		plan.Current = plan.Protected[0]
	}
	sort.Slice(plan.Protected, func(i, j int) bool { return plan.Protected[i].Tag < plan.Protected[j].Tag })
	sort.Slice(plan.Delete, func(i, j int) bool { return plan.Delete[i].Tag < plan.Delete[j].Tag })
	return s.ApplyPrunePlan(plan)
}

func (s *Store) verifyVersionSHA(name, tag, wantSHA string) error {
	ref, err := s.inspectVersion(name, tag)
	if err != nil {
		return err
	}
	if ref.SHA256 != strings.ToLower(wantSHA) {
		return i18n.Errorf("version %s/%s SHA-256 mismatch: got %s, want %s", name, tag, ref.SHA256, wantSHA)
	}
	return nil
}

func validateBoundRef(ref VersionRef) error {
	if ref.Tag == "" || ref.SHA256 == "" {
		return i18n.Errorf("version reference requires a tag and SHA-256")
	}
	if err := validateActivationTag(ref.Tag); err != nil {
		return err
	}
	if len(ref.SHA256) != sha256.Size*2 {
		return i18n.Errorf("version %q has invalid SHA-256", ref.Tag)
	}
	if _, err := hex.DecodeString(ref.SHA256); err != nil {
		return i18n.Wrapf("version %q has invalid SHA-256: %w", err, ref.Tag)
	}
	return nil
}

func (s *Store) installedVersions(name string) (map[string]VersionRef, error) {
	nameDir, err := s.storeDir(false, name)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]VersionRef{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(nameDir)
	if err != nil {
		return nil, err
	}
	versions := make(map[string]VersionRef)
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), "original") {
			if entry.Name() != "original" {
				return nil, i18n.Errorf("reserved original directory has non-canonical spelling %q", entry.Name())
			}
			if !entry.IsDir() {
				return nil, i18n.Errorf("original store path is not a directory")
			}
			if _, err := s.storeDir(false, name, "original"); err != nil {
				return nil, err
			}
			continue
		}
		if !entry.IsDir() {
			return nil, i18n.Errorf("unexpected non-directory in tool store: %s", entry.Name())
		}
		if err := validateNameTag("tag", entry.Name()); err != nil {
			return nil, err
		}
		ref, err := s.inspectVersion(name, entry.Name())
		if err != nil {
			return nil, err
		}
		versions[ref.Tag] = ref
	}
	return versions, nil
}

func (s *Store) inspectVersion(name, tag string) (VersionRef, error) {
	if err := validateNameTag("name", name); err != nil {
		return VersionRef{}, err
	}
	if err := validateActivationTag(tag); err != nil {
		return VersionRef{}, err
	}
	dir, err := s.storeDir(false, name, tag)
	if err != nil {
		return VersionRef{}, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return VersionRef{}, err
	}
	if len(entries) != 1 {
		return VersionRef{}, i18n.Errorf("version %s/%s must contain exactly one binary", name, tag)
	}
	path := filepath.Join(dir, entries[0].Name())
	info, err := os.Lstat(path)
	if err != nil {
		return VersionRef{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return VersionRef{}, i18n.Errorf("version %s/%s contains a non-regular binary", name, tag)
	}
	if _, err := s.ensureUnderRoot(path); err != nil {
		return VersionRef{}, err
	}
	digest, err := SHA256File(path)
	if err != nil {
		return VersionRef{}, err
	}
	return VersionRef{Tag: tag, SHA256: digest}, nil
}

// atomicCopyFile copies srcPath into a complete temporary regular file beside
// dstPath, fsyncs and closes it, then atomically renames it over dstPath.
func atomicCopyFile(fs durableOperations, srcPath, dstPath, dstDir string) error {
	return atomicCopyFileWithMode(fs, srcPath, dstPath, dstDir, true)
}

// atomicCopyFileNoReplace installs a completed copy only if dstPath does not
// already exist. The final hard-link operation is atomic and fails closed on a
// competing creator, which keeps the original backup write-once.
func atomicCopyFileNoReplace(fs durableOperations, srcPath, dstPath, stagingDir string) error {
	return atomicCopyFileWithMode(fs, srcPath, dstPath, stagingDir, false)
}

func atomicCopyFileWithMode(fs durableOperations, srcPath, dstPath, stagingDir string, replace bool) error {
	if err := fs.MkdirAll(stagingDir, 0o755); err != nil {
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
		return i18n.Errorf("activation source is not a regular file: %s", srcPath)
	}

	tmp, err := os.CreateTemp(stagingDir, ".hukou-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = fs.Remove(tmpPath)
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
		return i18n.Errorf("activation source changed while copying: %s", srcPath)
	}
	sourceInfoAfter, err := os.Stat(srcPath)
	if err != nil {
		_ = tmp.Close()
		return err
	}
	if sourceInfoAfter.Mode().Perm() != srcInfo.Mode().Perm() {
		_ = tmp.Close()
		return i18n.Errorf("activation source mode changed while copying: %s", srcPath)
	}
	if err := tmp.Chmod(srcInfo.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := fs.SyncFile(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if replace {
		if err := fs.Rename(tmpPath, dstPath); err != nil {
			return err
		}
		cleanup = false
		return nil
	}
	if err := fs.Link(tmpPath, dstPath); err != nil {
		return i18n.Wrapf("commit without replacing %s: %w", err, dstPath)
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
	if err := s.durability().RemoveAll(tmpDir); err != nil {
		return err
	}
	return s.durability().Mkdir(tmpDir, 0o755)
}

// SHA256File returns the hex-encoded SHA-256 digest of the file at path.
func SHA256File(path string) (string, error) {
	sha256FileCalls.Add(1)
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
	if err := durablefs.MkdirAll(linkDir, 0o755); err != nil {
		return err
	}

	tmpFile, err := os.CreateTemp(linkDir, ".hukou-tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		_ = durablefs.Remove(tmpPath)
		return err
	}
	if err := durablefs.Remove(tmpPath); err != nil {
		return err
	}

	if err := os.Symlink(target, tmpPath); err != nil {
		_ = durablefs.Remove(tmpPath)
		return err
	}
	if err := durablefs.SyncDir(linkDir); err != nil {
		_ = durablefs.Remove(tmpPath)
		return err
	}

	if err := durablefs.Rename(tmpPath, linkPath); err != nil {
		_ = durablefs.Remove(tmpPath)
		return err
	}
	return nil
}
