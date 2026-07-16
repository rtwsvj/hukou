package transaction

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

// testBeforeApplyHook is an unexported deterministic race seam. Production
// leaves it nil; package tests use it to place a competing path after state
// classification but before the final apply guard.
var testBeforeApplyHook func(string)

// These narrow seams let package tests prove that an already-matching mutable
// participant reasserts both file and directory-entry durability. Production
// always uses the real durable filesystem operations.
var (
	syncMatchedFile   = durablefs.SyncFile
	syncMatchedParent = durablefs.SyncParent
)

// QuarantineRecord names one previously unknown transaction-root entry and the
// quarantined-* container it was moved into.
type QuarantineRecord struct {
	Original    string
	Quarantined string
}

// RecoverSummary reports the observable side effects of a recovery pass so that
// callers can surface quarantined entries. An empty summary means recovery had
// nothing to isolate.
type RecoverSummary struct {
	Quarantined []QuarantineRecord
}

// Quarantine container layout: quarantined-<16 hex random>/ holding the moved
// entry under a fixed payload name plus a META file that records the original
// name. The container name carries no user-controlled bytes, so its length is
// bounded and collisions are impossible to exploit.
const (
	quarantineMetaFileName = "META"
	quarantinePayloadName  = "payload"
	quarantineNameAttempts = 64
)

// quarantineNameSuffix is an unexported seam so tests can force container-name
// collisions. Production always derives 16 hex characters from crypto/rand.
var quarantineNameSuffix = func() (string, error) {
	id, err := randomID()
	if err != nil {
		return "", err
	}
	return id[:16], nil
}

// Recover resolves the one globally serialized pending transaction and cleans
// abandoned building/completed journals. Callers must hold hukou's mutation
// lock. PREPARED transactions roll back; transactions with a valid durable
// COMMIT marker roll forward.
//
// Unknown non-directory entries (stray files, symlinks, and similar junk) no
// longer wedge recovery: each is moved into a quarantined-* container (its data
// is preserved for diagnosis, never deleted) and recorded in the returned
// summary before recovery continues on the known journals. Unknown real
// directories keep the fail-closed contract: they may be journal layouts from a
// newer hukou, and demoting one automatically could destroy the only recovery
// evidence, so Recover reports them and changes nothing else. Quarantined
// containers are left untouched, so a recovery that is interrupted and retried
// converges on the same result.
func Recover(dataRoot string) (RecoverSummary, error) {
	var summary RecoverSummary
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return summary, err
	}
	txRoot := filepath.Join(root, transactionsDirName)
	if err := ensureTransactionRoot(root, txRoot); err != nil {
		return summary, err
	}
	status, err := Inspect(root)
	if err != nil {
		return summary, err
	}
	unknownDirs := make([]string, 0, len(status.Unknown))
	for _, name := range status.Unknown {
		info, err := os.Lstat(filepath.Join(txRoot, name))
		if err != nil {
			return summary, fmt.Errorf("inspect unknown transaction entry %s: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			unknownDirs = append(unknownDirs, name)
			continue
		}
		record, err := quarantineEntry(txRoot, name)
		if err != nil {
			return summary, fmt.Errorf("quarantine unknown transaction entry %s: %w", name, err)
		}
		summary.Quarantined = append(summary.Quarantined, record)
	}
	if len(unknownDirs) != 0 {
		// Fail closed before touching any journal: an unknown directory may hold
		// authoritative recovery evidence written by a newer hukou.
		return summary, fmt.Errorf("unknown directories in transaction root: %s; they may be journals from a newer hukou, so nothing was recovered or removed - inspect them with `hukou doctor`, then move them out manually or upgrade hukou", strings.Join(unknownDirs, ", "))
	}
	if len(status.Pending) > 1 {
		return summary, fmt.Errorf("multiple pending transactions violate the global journal invariant: %s", strings.Join(status.Pending, ", "))
	}

	for _, name := range status.Building {
		if err := removeJournalDirectory(txRoot, filepath.Join(txRoot, name)); err != nil {
			return summary, fmt.Errorf("clean abandoned building journal %s: %w", name, err)
		}
	}
	for _, name := range status.Completed {
		if err := removeJournalDirectory(txRoot, filepath.Join(txRoot, name)); err != nil {
			return summary, fmt.Errorf("clean completed journal %s: %w", name, err)
		}
	}
	if len(status.Pending) == 0 {
		return summary, nil
	}

	pendingName := status.Pending[0]
	pendingDir := filepath.Join(txRoot, pendingName)
	if err := requireRealDirectory(pendingDir); err != nil {
		return summary, err
	}
	intent, err := decodeIntent(filepath.Join(pendingDir, intentFileName))
	if err != nil {
		return summary, fmt.Errorf("load pending transaction %s: %w", pendingName, err)
	}
	if pendingName != pendingPrefix+intent.ID {
		return summary, fmt.Errorf("pending transaction directory/id mismatch: dir=%s id=%s", pendingName, intent.ID)
	}
	committed, err := hasValidCommit(pendingDir, intent.ID)
	if err != nil {
		return summary, fmt.Errorf("inspect transaction commit decision: %w", err)
	}
	return summary, recoverLoaded(txRoot, pendingDir, intent, committed)
}

// quarantineEntry moves one unknown non-directory transaction-root entry into a
// fresh quarantined-<16 hex> container. The container is allocated with the
// exclusive-create mkdir primitive and a colliding random name is retried with
// a new name, so an existing container is never overwritten. The entry's bytes
// are preserved verbatim under the fixed payload name and its original name is
// recorded in the container's META file. On Unix a backslash is an ordinary
// filename byte, so only names that cannot come from a directory entry are
// rejected.
func quarantineEntry(txRoot, name string) (QuarantineRecord, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, '/') {
		return QuarantineRecord{}, fmt.Errorf("invalid transaction entry name %q", name)
	}
	source := filepath.Join(txRoot, name)
	info, err := os.Lstat(source)
	if err != nil {
		return QuarantineRecord{}, err
	}
	if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
		return QuarantineRecord{}, fmt.Errorf("refuse to quarantine directory %q", name)
	}
	var container, containerName string
	for attempt := 0; attempt < quarantineNameAttempts; attempt++ {
		suffix, err := quarantineNameSuffix()
		if err != nil {
			return QuarantineRecord{}, err
		}
		candidate := quarantinedPrefix + suffix
		path := filepath.Join(txRoot, candidate)
		err = os.Mkdir(path, 0o700)
		if err == nil {
			container, containerName = path, candidate
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return QuarantineRecord{}, err
		}
	}
	if container == "" {
		return QuarantineRecord{}, fmt.Errorf("cannot allocate a unique quarantine container under %s", txRoot)
	}
	meta := fmt.Sprintf("schema=hukou-quarantine-v1\noriginal_name=%q\nquarantined_at=%s\n",
		name, time.Now().UTC().Format(time.RFC3339))
	if err := durablefs.AtomicWriteFile(filepath.Join(container, quarantineMetaFileName), []byte(meta), 0o600); err != nil {
		return QuarantineRecord{}, err
	}
	// durablefs.Rename only accepts same-parent moves; this move crosses from
	// the transaction root into the container, so rename first and then reassert
	// both affected directory entries explicitly.
	if err := os.Rename(source, filepath.Join(container, quarantinePayloadName)); err != nil {
		return QuarantineRecord{}, err
	}
	if err := durablefs.SyncDir(container); err != nil {
		return QuarantineRecord{}, err
	}
	if err := durablefs.SyncDir(txRoot); err != nil {
		return QuarantineRecord{}, err
	}
	return QuarantineRecord{Original: name, Quarantined: containerName}, nil
}

// PurgeQuarantined removes every quarantined-* entry under the data root's
// transaction directory and returns the names it removed. Callers must hold the
// mutation lock. It never touches building/pending/completed journals and is
// idempotent when no quarantined entries remain.
func PurgeQuarantined(dataRoot string) ([]string, error) {
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, err
	}
	txRoot := filepath.Join(root, transactionsDirName)
	status, err := Inspect(root)
	if err != nil {
		return nil, err
	}
	removed := make([]string, 0, len(status.Quarantined))
	for _, name := range status.Quarantined {
		// Inspect already validates containers, but purge is destructive so the
		// same layout check is repeated defensively before any removal.
		if !IsValidQuarantineContainer(txRoot, name) {
			continue
		}
		dir := filepath.Join(txRoot, name)
		if filepath.Dir(dir) != txRoot {
			return removed, fmt.Errorf("quarantine path escapes transaction root: %q", name)
		}
		if err := durablefs.RemoveAll(dir); err != nil {
			return removed, fmt.Errorf("remove quarantined entry %s: %w", name, err)
		}
		removed = append(removed, name)
	}
	return removed, nil
}

func recoverLoaded(txRoot, dir string, intent Intent, committed bool) error {
	targetAfter := committed
	// Classify every participant before touching any of them. This is the
	// fail-closed boundary that prevents one unknown external update from being
	// overwritten after another resource has already been repaired.
	for _, mutation := range intent.Mutations {
		before, err := matchesState(mutation.Path, mutation.Before)
		if err != nil {
			return fmt.Errorf("classify %s before state: %w", mutation.Role, err)
		}
		after, err := matchesState(mutation.Path, mutation.After)
		if err != nil {
			return fmt.Errorf("classify %s after state: %w", mutation.Role, err)
		}
		if !before && !after {
			return fmt.Errorf("transaction %s resource %s has unknown drift at %s; refusing overwrite", intent.ID, mutation.Role, mutation.Path)
		}
		if err := verifyStatePayload(dir, mutation.Before); err != nil {
			return fmt.Errorf("verify %s before payload: %w", mutation.Role, err)
		}
		if err := verifyStatePayload(dir, mutation.After); err != nil {
			return fmt.Errorf("verify %s after payload: %w", mutation.Role, err)
		}
	}

	for _, mutation := range intent.Mutations {
		target := mutation.Before
		if targetAfter {
			target = mutation.After
		}
		if err := convergeMutation(dir, mutation, target); err != nil {
			return fmt.Errorf("recover %s resource %s: %w", intent.ID, mutation.Role, err)
		}
	}
	if err := verifyIntentTarget(dir, intent, targetAfter); err != nil {
		return err
	}
	return finalizeDirectory(txRoot, dir, intent.ID)
}

func convergeMutation(dir string, mutation Mutation, target State) error {
	targetMatches, err := matchesState(mutation.Path, target)
	if err != nil {
		return err
	}
	if targetMatches {
		// Unchanged participants are read-only guards, not filesystem mutations.
		// Every mutable participant must, however, reassert durability even when
		// an earlier attempt already made the target state visible before returning
		// an error or crashing.
		if statesEqual(mutation.Before, mutation.After) {
			return nil
		}
		if err := persistMatchedState(mutation.Path, target); err != nil {
			return err
		}
		stillMatches, err := matchesState(mutation.Path, target)
		if err != nil {
			return err
		}
		if !stillMatches {
			return fmt.Errorf("resource changed while reasserting durability at %s", mutation.Path)
		}
		return nil
	}
	other := mutation.Before
	if statesEqual(target, mutation.Before) {
		other = mutation.After
	}
	otherMatches, err := matchesState(mutation.Path, other)
	if err != nil {
		return err
	}
	if !otherMatches {
		return fmt.Errorf("resource has unknown drift at %s", mutation.Path)
	}
	if testBeforeApplyHook != nil {
		testBeforeApplyHook(mutation.Path)
	}
	if err := verifyStatePayload(dir, target); err != nil {
		return err
	}
	if err := applyState(dir, mutation.Path, other, target); err != nil {
		return err
	}
	ok, err := matchesState(mutation.Path, target)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("resource does not match target after durable replace: %s", mutation.Path)
	}
	return nil
}

func verifyIntentTarget(dir string, intent Intent, after bool) error {
	for _, mutation := range intent.Mutations {
		target := mutation.Before
		if after {
			target = mutation.After
		}
		if err := verifyStatePayload(dir, target); err != nil {
			return fmt.Errorf("verify %s payload: %w", mutation.Role, err)
		}
		matches, err := matchesState(mutation.Path, target)
		if err != nil {
			return fmt.Errorf("verify %s path: %w", mutation.Role, err)
		}
		if !matches {
			return fmt.Errorf("transaction resource %s does not match %s state", mutation.Role, map[bool]string{true: "after", false: "before"}[after])
		}
	}
	return nil
}

func statesEqual(a, b State) bool {
	return a.Kind == b.Kind && a.SHA256 == b.SHA256 && a.Mode == b.Mode && a.LinkTarget == b.LinkTarget
}

func persistMatchedState(path string, state State) error {
	switch state.Kind {
	case KindRegular:
		before, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
			return fmt.Errorf("matched regular resource changed topology before sync: %s", path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		opened, statErr := file.Stat()
		if statErr != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
			closeErr := file.Close()
			if statErr == nil {
				statErr = fmt.Errorf("matched regular resource changed while opening: %s", path)
			}
			return errors.Join(statErr, closeErr)
		}
		if err := syncMatchedFile(file); err != nil {
			_ = file.Close()
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		return syncMatchedParent(path)
	case KindSymlink, KindAbsent:
		// A symlink's bytes are its directory entry, and a durable absence is also
		// represented only by the parent directory entry.
		return syncMatchedParent(path)
	default:
		return fmt.Errorf("unknown state kind %q", state.Kind)
	}
}

func matchesState(path string, state State) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return state.Kind == KindAbsent, nil
		}
		return false, err
	}
	switch state.Kind {
	case KindAbsent:
		return false, nil
	case KindRegular:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != state.Mode {
			return false, nil
		}
		sha, err := sha256File(path)
		if err != nil {
			return false, err
		}
		return sha == state.SHA256, nil
	case KindSymlink:
		if info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := os.Readlink(path)
		if err != nil {
			return false, err
		}
		if target != state.LinkTarget {
			return false, nil
		}
		sha, err := sha256File(path)
		if err != nil {
			return false, err
		}
		return sha == state.SHA256, nil
	default:
		return false, fmt.Errorf("unknown state kind %q", state.Kind)
	}
}

func verifyStatePayload(dir string, state State) error {
	if state.Kind != KindRegular {
		return nil
	}
	if filepath.Base(state.Payload) != state.Payload {
		return fmt.Errorf("payload escapes journal: %q", state.Payload)
	}
	path := filepath.Join(dir, state.Payload)
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("payload is not a regular file: %s", path)
	}
	sha, err := sha256File(path)
	if err != nil {
		return err
	}
	if sha != state.SHA256 {
		return fmt.Errorf("payload SHA-256 mismatch: got %s want %s", sha, state.SHA256)
	}
	return nil
}

func applyState(dir, path string, current, target State) error {
	switch target.Kind {
	case KindAbsent:
		if err := requireExpectedState(path, current); err != nil {
			return err
		}
		// A non-cooperating writer can still race this check and the remove
		// syscall. There is no portable compare-and-remove primitive, so keep this
		// final window explicit and as small as possible.
		return durablefs.Remove(path)
	case KindRegular:
		if current.Kind == KindAbsent {
			return installRegularNoReplace(filepath.Join(dir, target.Payload), path, os.FileMode(target.Mode))
		}
		return replaceRegular(filepath.Join(dir, target.Payload), path, os.FileMode(target.Mode), current)
	case KindSymlink:
		if current.Kind == KindAbsent {
			return installSymlinkNoReplace(target.LinkTarget, path)
		}
		return replaceSymlink(target.LinkTarget, path, current)
	default:
		return fmt.Errorf("unknown state kind %q", target.Kind)
	}
}

func requireExpectedState(path string, expected State) error {
	matches, err := matchesState(path, expected)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("resource has unknown drift at %s", path)
	}
	return nil
}

func installRegularNoReplace(source, destination string, mode os.FileMode) (retErr error) {
	parent := filepath.Dir(destination)
	if err := durablefs.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmpPath, err := stageRegular(source, parent, mode)
	if err != nil {
		return err
	}
	defer func() {
		if cleanupErr := durablefs.Remove(tmpPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()
	if err := durablefs.Link(tmpPath, destination); err != nil {
		return fmt.Errorf("install without replacing %s: %w", destination, err)
	}
	return nil
}

func replaceRegular(source, destination string, mode os.FileMode, expected State) (retErr error) {
	parent := filepath.Dir(destination)
	if err := durablefs.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	tmpPath, err := stageRegular(source, parent, mode)
	if err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			if cleanupErr := durablefs.Remove(tmpPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	if err := requireExpectedState(destination, expected); err != nil {
		return err
	}
	// The same unavoidable non-cooperating-writer window remains between this
	// recheck and rename(2); the hukou mutation lock closes it for all hukou
	// writers, while the recheck prevents stale observations across staging.
	if err := durablefs.Rename(tmpPath, destination); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func stageRegular(source, parent string, mode os.FileMode) (tmpPath string, retErr error) {
	src, err := os.Open(source)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp, err := os.CreateTemp(parent, ".hukou-txn-*")
	if err != nil {
		return "", err
	}
	tmpPath = tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			if cleanupErr := durablefs.Remove(tmpPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), src); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := durablefs.SyncFile(tmp); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	want, err := sha256File(source)
	if err != nil {
		return "", err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return "", fmt.Errorf("journal payload changed while applying: got %s want %s", got, want)
	}
	cleanup = false
	return tmpPath, nil
}

func replaceSymlink(target, destination string, expected State) (retErr error) {
	parent := filepath.Dir(destination)
	if err := durablefs.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	raw, err := randomID()
	if err != nil {
		return err
	}
	tmpPath := filepath.Join(parent, ".hukou-txn-link-"+raw)
	if err := os.Symlink(target, tmpPath); err != nil {
		return err
	}
	cleanup := true
	defer func() {
		if cleanup {
			if cleanupErr := durablefs.Remove(tmpPath); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
				retErr = errors.Join(retErr, cleanupErr)
			}
		}
	}()
	if err := requireExpectedState(destination, expected); err != nil {
		return err
	}
	// See replaceRegular: no portable compare-and-rename primitive can exclude
	// a non-cooperating writer in the final syscall window.
	if err := durablefs.Rename(tmpPath, destination); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func installSymlinkNoReplace(target, destination string) error {
	parent := filepath.Dir(destination)
	if err := durablefs.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	if err := os.Symlink(target, destination); err != nil {
		return fmt.Errorf("install symlink without replacing %s: %w", destination, err)
	}
	if err := durablefs.SyncParent(destination); err != nil {
		return fmt.Errorf("persist symlink %s: %w", destination, err)
	}
	return nil
}

func hasValidCommit(dir, id string) (bool, error) {
	path := filepath.Join(dir, commitFileName)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 256 {
		return false, fmt.Errorf("invalid COMMIT marker topology")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	if string(data) != id+"\n" {
		return false, fmt.Errorf("invalid COMMIT marker contents")
	}
	return true, nil
}

func finalizeDirectory(txRoot, pendingDir, id string) error {
	completedDir := filepath.Join(txRoot, completedPrefix+id)
	if filepath.Base(pendingDir) != pendingPrefix+id {
		return fmt.Errorf("refuse to finalize unexpected journal path %s", pendingDir)
	}
	if err := durablefs.Rename(pendingDir, completedDir); err != nil {
		return fmt.Errorf("move committed journal to cleanup namespace: %w", err)
	}
	if err := durablefs.RemoveAll(completedDir); err != nil {
		return fmt.Errorf("remove completed journal: %w", err)
	}
	return nil
}

func removeJournalDirectory(txRoot, dir string) error {
	if filepath.Dir(dir) != txRoot {
		return fmt.Errorf("journal path escapes transaction root: %s", dir)
	}
	if err := requireRealDirectory(dir); err != nil {
		return err
	}
	return durablefs.RemoveAll(dir)
}

func requireRealDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("journal path must be a real directory: %s", path)
	}
	return nil
}
