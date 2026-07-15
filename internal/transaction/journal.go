package transaction

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
)

const (
	journalSchemaVersion = 1
	transactionsDirName  = "transactions"
	buildingPrefix       = ".building-"
	pendingPrefix        = "pending-"
	completedPrefix      = "completed-"
	intentFileName       = "intent.json"
	commitFileName       = "COMMIT"
	maxIntentBytes       = 1 << 20
)

// Kind describes the filesystem topology of one transaction resource.
type Kind string

const (
	KindAbsent  Kind = "absent"
	KindRegular Kind = "regular"
	KindSymlink Kind = "symlink"
)

// State is a durable, content-addressed description of one resource.
// Payload is a basename inside the transaction directory and is present only
// for regular files. Mode contains only the rwx permission bits.
type State struct {
	Kind       Kind   `json:"kind"`
	SHA256     string `json:"sha256,omitempty"`
	Mode       uint32 `json:"mode,omitempty"`
	LinkTarget string `json:"link_target,omitempty"`
	Payload    string `json:"payload,omitempty"`
}

// Mutation records the complete before and after state of one absolute path.
type Mutation struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	Before State  `json:"before"`
	After  State  `json:"after"`
}

// Intent is the immutable write-ahead transaction record.
type Intent struct {
	SchemaVersion int        `json:"schema_version"`
	ID            string     `json:"id"`
	Operation     string     `json:"operation"`
	Name          string     `json:"name"`
	CreatedAt     string     `json:"created_at"`
	Mutations     []Mutation `json:"mutations"`
}

type desiredKind int

const (
	desiredAbsent desiredKind = iota
	desiredRegularPath
	desiredRegularBytes
	desiredUnchanged
)

// Desired is an in-memory description used while building an intent. Use one
// of the constructor functions below rather than constructing it directly.
type Desired struct {
	kind       desiredKind
	sourcePath string
	data       []byte
	mode       fs.FileMode
}

// Absent describes a path that must not exist after commit.
func Absent() Desired { return Desired{kind: desiredAbsent} }

// RegularFile captures the current bytes and rwx mode of sourcePath as the
// desired state. The source is copied into the journal before PREPARED is
// published, so recovery never depends on the source still existing.
func RegularFile(sourcePath string) Desired {
	return Desired{kind: desiredRegularPath, sourcePath: sourcePath}
}

// RegularBytes stores data in the journal as the desired regular-file state.
func RegularBytes(data []byte, mode fs.FileMode) Desired {
	return Desired{kind: desiredRegularBytes, data: append([]byte(nil), data...), mode: mode.Perm()}
}

// Unchanged turns a path into a read-only transaction guard. Its after state
// is exactly its captured before state, so Commit fails closed if an external
// writer changes the path at any point after PREPARED.
func Unchanged() Desired { return Desired{kind: desiredUnchanged} }

// Spec defines one path that participates in a transaction. Roles must be
// unique and paths are normalized to absolute paths by Begin.
type Spec struct {
	Role  string
	Path  string
	After Desired
}

// Transaction is a single globally serialized journal under a data root.
type Transaction struct {
	dataRoot  string
	txRoot    string
	dir       string
	intent    Intent
	committed bool
	done      bool
}

// Status is a read-only inventory of transaction directories.
type Status struct {
	Building  []string
	Pending   []string
	Completed []string
	Unknown   []string
}

// NeedsRecovery reports whether state-changing recovery or cleanup is needed.
// Write paths (Begin) gate on this: any residue at all blocks a new mutation
// so recovery runs first under the state lock.
func (s Status) NeedsRecovery() bool {
	return len(s.Building)+len(s.Pending)+len(s.Completed)+len(s.Unknown) > 0
}

// PendingError is returned by read-only callers that cannot safely recover.
type PendingError struct {
	Status Status
}

func (e *PendingError) Error() string {
	return fmt.Sprintf("hukou state has unfinished transaction data (building=%d pending=%d completed=%d unknown=%d); a normal mutating command will attempt safe recovery under the state lock, or run hukou doctor to inspect it",
		len(e.Status.Building), len(e.Status.Pending), len(e.Status.Completed), len(e.Status.Unknown))
}

// CheckClean performs a strictly read-only, fail-closed transaction check.
// Any journal residue (building/pending/completed/unknown) is treated as an
// error. Use it where a caller must refuse to proceed until the journal is
// fully clean.
func CheckClean(dataRoot string) error {
	status, err := Inspect(dataRoot)
	if err != nil {
		return err
	}
	if status.NeedsRecovery() {
		return &PendingError{Status: status}
	}
	return nil
}

// CheckReadable performs a narrower, read-path transaction check than
// CheckClean. It is strictly read-only and never creates or modifies any path.
//
// Exactly one residue class is tolerated: a VERIFIED completed-* journal,
// meaning all of (a) the entry name is exactly completed-<32-lowercase-hex-id>,
// (b) Lstat reports a real directory (not a symlink), and (c) the directory
// contains a valid COMMIT marker whose contents match the id. Such a journal
// is committed and already converged; only its directory deletion remains, so
// live state and the manifest are consistent. Verified completed residue is
// reported as a non-fatal note the caller may surface as an advisory.
//
// Everything else fails closed with a *PendingError, exactly like CheckClean:
//
//   - pending-*: a published transaction may have live state mid-flight.
//   - building-*: this check observes the journal at a single point in time
//     and cannot cover the caller's whole read cycle. A .building-* entry may
//     be the visible window of another process's ACTIVE Begin, which can
//     publish (rename to pending-*) and start applying mutations while the
//     caller is still reading, so an apparently abandoned building journal is
//     indistinguishable from a live writer and must stay fail-closed.
//   - unknown entries and any malformed name (wrong id shape, uppercase hex,
//     symlinked directory, missing or mismatched COMMIT): corrupted or
//     adversarial state that no read path may reason about.
//
// TOCTOU acceptance (recorded product decision, 2026-07-15; see
// docs/09-decision-log.md): this check is deliberately not atomic, at two
// layers. First, there is no atomicity between the completed-journal
// verification and the caller's subsequent reads — residue may appear, vanish,
// or change class after this function returns. Second, the three verification
// steps for one completed entry (name shape, Lstat topology, COMMIT contents)
// are individual snapshots, and a concurrent writer could swap the directory
// between any two of them. Both windows are accepted instead of closed with a
// read lock, because:
//
//   - the read path is a same-user diagnostic view, not a security boundary;
//     a writer who can race this check can already write the transaction root
//     and therefore owns the state outright.
//   - the hukou detector independently re-verifies every matched entry by
//     sha256 against the manifest (HukouDetector.Match), so attribution
//     conclusions never depend on this check being correct at the instant of
//     use — a stale observation here can at worst mis-phrase an advisory.
//   - the write path (Begin) stays fail-closed on EVERY residue class and
//     runs under the hukou mutation lock, so no mutation is ever based on an
//     observation made by this read-path check.
func CheckReadable(dataRoot string) (notes []string, err error) {
	status, err := Inspect(dataRoot)
	if err != nil {
		return nil, err
	}
	if len(status.Building)+len(status.Pending)+len(status.Unknown) > 0 {
		return nil, &PendingError{Status: status}
	}
	if len(status.Completed) == 0 {
		return nil, nil
	}
	txRoot, err := transactionRoot(dataRoot)
	if err != nil {
		return nil, err
	}
	for _, name := range status.Completed {
		if !isVerifiedCompletedJournal(txRoot, name) {
			return nil, &PendingError{Status: status}
		}
	}
	return []string{fmt.Sprintf(
		"stale journal residue; run a mutating command or repair to clean (completed=%d)",
		len(status.Completed))}, nil
}

// isVerifiedCompletedJournal reports whether name under txRoot is a genuine
// committed-and-converged journal whose only unfinished work is directory
// deletion: an exact completed-<32-lowercase-hex> name, a real directory, and
// a valid COMMIT marker matching the id. Any uncertainty, including I/O
// errors, counts as unverified so read paths stay fail-closed.
func isVerifiedCompletedJournal(txRoot, name string) bool {
	id := strings.TrimPrefix(name, completedPrefix)
	if !isJournalID(id) {
		return false
	}
	dir := filepath.Join(txRoot, name)
	info, err := os.Lstat(dir)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return false
	}
	committed, err := hasValidCommit(dir, id)
	return err == nil && committed
}

// isJournalID reports whether s has the exact shape of a journal id as
// produced by randomID: 32 lowercase hexadecimal characters.
func isJournalID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// Inspect inventories journal directories without creating or modifying any
// path. It is safe for dry-run and doctor diagnostics.
func Inspect(dataRoot string) (Status, error) {
	var status Status
	txRoot, err := transactionRoot(dataRoot)
	if err != nil {
		return status, err
	}
	info, err := os.Lstat(txRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status, nil
		}
		return status, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return status, fmt.Errorf("transaction root must be a real directory: %s", txRoot)
	}
	entries, err := os.ReadDir(txRoot)
	if err != nil {
		return status, err
	}
	for _, entry := range entries {
		name := entry.Name()
		switch {
		case strings.HasPrefix(name, buildingPrefix):
			status.Building = append(status.Building, name)
		case strings.HasPrefix(name, pendingPrefix):
			status.Pending = append(status.Pending, name)
		case strings.HasPrefix(name, completedPrefix):
			status.Completed = append(status.Completed, name)
		default:
			status.Unknown = append(status.Unknown, name)
		}
	}
	sort.Strings(status.Building)
	sort.Strings(status.Pending)
	sort.Strings(status.Completed)
	sort.Strings(status.Unknown)
	return status, nil
}

// Begin durably captures all before/after payloads and publishes PREPARED by
// atomically renaming a .building-* directory to pending-*. No caller-visible
// path may be changed before Begin succeeds.
func Begin(dataRoot, operation, name string, specs []Spec) (_ *Transaction, retErr error) {
	if strings.TrimSpace(operation) == "" {
		return nil, fmt.Errorf("transaction operation is required")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("transaction name is required")
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("transaction requires at least one mutation")
	}
	if len(specs) > 16 {
		return nil, fmt.Errorf("too many transaction mutations: %d", len(specs))
	}

	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return nil, err
	}
	txRoot := filepath.Join(root, transactionsDirName)
	if err := ensureTransactionRoot(root, txRoot); err != nil {
		return nil, err
	}
	status, err := Inspect(root)
	if err != nil {
		return nil, err
	}
	if status.NeedsRecovery() {
		return nil, &PendingError{Status: status}
	}

	id, err := randomID()
	if err != nil {
		return nil, err
	}
	buildingDir := filepath.Join(txRoot, buildingPrefix+id)
	pendingDir := filepath.Join(txRoot, pendingPrefix+id)
	if err := durablefs.Mkdir(buildingDir, 0o700); err != nil {
		return nil, fmt.Errorf("create building journal: %w", err)
	}
	cleanupBuilding := true
	defer func() {
		if !cleanupBuilding {
			return
		}
		if cleanupErr := durablefs.RemoveAll(buildingDir); cleanupErr != nil && !errors.Is(cleanupErr, os.ErrNotExist) {
			retErr = errors.Join(retErr, fmt.Errorf("clean building journal: %w", cleanupErr))
		}
	}()

	intent := Intent{
		SchemaVersion: journalSchemaVersion,
		ID:            id,
		Operation:     operation,
		Name:          name,
		CreatedAt:     time.Now().UTC().Format(time.RFC3339Nano),
		Mutations:     make([]Mutation, 0, len(specs)),
	}
	roles := make(map[string]struct{}, len(specs))
	paths := make(map[string]struct{}, len(specs))
	for i, spec := range specs {
		if spec.Role == "" || strings.ContainsAny(spec.Role, `/\\`) {
			return nil, fmt.Errorf("invalid transaction role %q", spec.Role)
		}
		if _, exists := roles[spec.Role]; exists {
			return nil, fmt.Errorf("duplicate transaction role %q", spec.Role)
		}
		roles[spec.Role] = struct{}{}
		path, err := filepath.Abs(spec.Path)
		if err != nil {
			return nil, err
		}
		path = filepath.Clean(path)
		if _, exists := paths[path]; exists {
			return nil, fmt.Errorf("duplicate transaction path %q", path)
		}
		paths[path] = struct{}{}

		before, err := capturePath(path, buildingDir, fmt.Sprintf("%02d-before.bin", i))
		if err != nil {
			return nil, fmt.Errorf("capture %s before state: %w", spec.Role, err)
		}
		after, err := materializeDesired(spec.After, before, buildingDir, fmt.Sprintf("%02d-after.bin", i))
		if err != nil {
			return nil, fmt.Errorf("capture %s after state: %w", spec.Role, err)
		}
		intent.Mutations = append(intent.Mutations, Mutation{Role: spec.Role, Path: path, Before: before, After: after})
	}

	intentBytes, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return nil, err
	}
	intentBytes = append(intentBytes, '\n')
	if err := durablefs.AtomicWriteFile(filepath.Join(buildingDir, intentFileName), intentBytes, 0o600); err != nil {
		return nil, fmt.Errorf("write transaction intent: %w", err)
	}
	if err := durablefs.SyncDir(buildingDir); err != nil {
		return nil, fmt.Errorf("sync building journal: %w", err)
	}
	if err := durablefs.Rename(buildingDir, pendingDir); err != nil {
		return nil, fmt.Errorf("publish prepared journal: %w", err)
	}
	cleanupBuilding = false

	return &Transaction{dataRoot: root, txRoot: txRoot, dir: pendingDir, intent: intent}, nil
}

// Before returns the captured before state for role.
func (t *Transaction) Before(role string) (State, bool) {
	mutation, ok := t.mutation(role)
	if !ok {
		return State{}, false
	}
	return mutation.Before, true
}

// After returns the captured after state for role.
func (t *Transaction) After(role string) (State, bool) {
	mutation, ok := t.mutation(role)
	if !ok {
		return State{}, false
	}
	return mutation.After, true
}

// Apply advances one mutation to its after state. It accepts an already-after
// resource, applies only from the captured before state, and refuses unknown
// external drift without overwriting it.
func (t *Transaction) Apply(role string) error {
	if t == nil || t.done {
		return fmt.Errorf("transaction is not active")
	}
	mutation, ok := t.mutation(role)
	if !ok {
		return fmt.Errorf("unknown transaction role %q", role)
	}
	return convergeMutation(t.dir, *mutation, mutation.After)
}

// Verify checks one participant against its captured before or after state
// without modifying it. Callers use this immediately before delegating a write
// to a component-specific durable save routine.
func (t *Transaction) Verify(role string, after bool) error {
	if t == nil || t.done {
		return fmt.Errorf("transaction is not active")
	}
	mutation, ok := t.mutation(role)
	if !ok {
		return fmt.Errorf("unknown transaction role %q", role)
	}
	target := mutation.Before
	if after {
		target = mutation.After
	}
	matches, err := matchesState(mutation.Path, target)
	if err != nil {
		return err
	}
	if !matches {
		return fmt.Errorf("transaction resource %s does not match %s state", role, map[bool]string{true: "after", false: "before"}[after])
	}
	return nil
}

// VerifyAfter checks that every participant exactly matches the committed
// state before the COMMIT decision is made.
func (t *Transaction) VerifyAfter() error {
	if t == nil || t.done {
		return fmt.Errorf("transaction is not active")
	}
	return verifyIntentTarget(t.dir, t.intent, true)
}

// Commit writes and durably syncs the transaction's irreversible decision.
// If Commit returns an error, callers must invoke Recover rather than assuming
// either outcome; the marker may have become visible before a sync error.
func (t *Transaction) Commit() error {
	if t == nil || t.done {
		return fmt.Errorf("transaction is not active")
	}
	if t.committed {
		return nil
	}
	if err := t.VerifyAfter(); err != nil {
		return err
	}
	marker := []byte(t.intent.ID + "\n")
	path := filepath.Join(t.dir, commitFileName)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create commit marker: %w", err)
	}
	if _, err := f.Write(marker); err != nil {
		_ = f.Close()
		return fmt.Errorf("write commit marker: %w", err)
	}
	if err := durablefs.SyncFile(f); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync commit marker: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close commit marker: %w", err)
	}
	if err := durablefs.SyncDir(t.dir); err != nil {
		return fmt.Errorf("sync committed journal: %w", err)
	}
	t.committed = true
	return nil
}

// Abort durably converges every participant to its before state and removes
// the PREPARED journal. Unknown drift is preserved and leaves the journal for
// explicit diagnosis.
func (t *Transaction) Abort() error {
	if t == nil || t.done {
		return nil
	}
	if t.committed {
		return fmt.Errorf("cannot abort committed transaction %s", t.intent.ID)
	}
	committed, err := hasValidCommit(t.dir, t.intent.ID)
	if err != nil {
		return err
	}
	if committed {
		return fmt.Errorf("cannot abort transaction %s: COMMIT exists", t.intent.ID)
	}
	if err := recoverLoaded(t.txRoot, t.dir, t.intent, false); err != nil {
		return err
	}
	t.done = true
	return nil
}

// Finalize moves a committed journal into the cleanup-only namespace and then
// removes it. A crash during deletion can never turn a committed transaction
// back into an apparent PREPARED transaction.
func (t *Transaction) Finalize() error {
	if t == nil || t.done {
		return nil
	}
	committed, err := hasValidCommit(t.dir, t.intent.ID)
	if err != nil {
		return err
	}
	if !committed {
		return fmt.Errorf("transaction %s is not committed", t.intent.ID)
	}
	if err := finalizeDirectory(t.txRoot, t.dir, t.intent.ID); err != nil {
		return err
	}
	t.done = true
	return nil
}

func (t *Transaction) mutation(role string) (*Mutation, bool) {
	for i := range t.intent.Mutations {
		if t.intent.Mutations[i].Role == role {
			return &t.intent.Mutations[i], true
		}
	}
	return nil, false
}

func materializeDesired(desired Desired, before State, dir, payloadName string) (State, error) {
	switch desired.kind {
	case desiredAbsent:
		return State{Kind: KindAbsent}, nil
	case desiredRegularPath:
		if desired.sourcePath == "" {
			return State{}, fmt.Errorf("regular desired state requires a source path")
		}
		return captureRegular(desired.sourcePath, dir, payloadName)
	case desiredRegularBytes:
		mode := desired.mode.Perm()
		if mode == 0 {
			mode = 0o600
		}
		payload := filepath.Join(dir, payloadName)
		if err := durablefs.AtomicWriteFile(payload, desired.data, mode); err != nil {
			return State{}, err
		}
		sum := sha256.Sum256(desired.data)
		return State{Kind: KindRegular, SHA256: hex.EncodeToString(sum[:]), Mode: uint32(mode), Payload: payloadName}, nil
	case desiredUnchanged:
		return before, nil
	default:
		return State{}, fmt.Errorf("unknown desired state")
	}
}

func capturePath(path, dir, payloadName string) (State, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{Kind: KindAbsent}, nil
		}
		return State{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return State{}, err
		}
		sha, err := sha256File(path)
		if err != nil {
			return State{}, fmt.Errorf("hash symlink target: %w", err)
		}
		after, err := os.Lstat(path)
		if err != nil {
			return State{}, err
		}
		afterTarget, err := os.Readlink(path)
		if err != nil || after.Mode()&os.ModeSymlink == 0 || !os.SameFile(info, after) || afterTarget != target {
			return State{}, fmt.Errorf("symlink changed while capturing: %s", path)
		}
		return State{Kind: KindSymlink, SHA256: sha, LinkTarget: target}, nil
	}
	if !info.Mode().IsRegular() {
		return State{}, fmt.Errorf("unsupported transaction path type at %s: %s", path, info.Mode())
	}
	return captureRegular(path, dir, payloadName)
}

func captureRegular(sourcePath, dir, payloadName string) (State, error) {
	before, err := os.Lstat(sourcePath)
	if err != nil {
		return State{}, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return State{}, fmt.Errorf("source is not a regular file: %s", sourcePath)
	}
	payloadPath := filepath.Join(dir, payloadName)
	src, err := os.Open(sourcePath)
	if err != nil {
		return State{}, err
	}
	opened, err := src.Stat()
	if err != nil {
		_ = src.Close()
		return State{}, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = src.Close()
		return State{}, fmt.Errorf("source changed while opening: %s", sourcePath)
	}
	dst, err := os.OpenFile(payloadPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, before.Mode().Perm())
	if err != nil {
		_ = src.Close()
		return State{}, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(dst, h), src)
	closeSrcErr := src.Close()
	if copyErr != nil || closeSrcErr != nil {
		_ = dst.Close()
		_ = os.Remove(payloadPath)
		return State{}, errors.Join(copyErr, closeSrcErr)
	}
	if err := dst.Chmod(before.Mode().Perm()); err != nil {
		_ = dst.Close()
		_ = os.Remove(payloadPath)
		return State{}, err
	}
	if err := durablefs.SyncFile(dst); err != nil {
		_ = dst.Close()
		_ = os.Remove(payloadPath)
		return State{}, err
	}
	if err := dst.Close(); err != nil {
		_ = os.Remove(payloadPath)
		return State{}, err
	}
	copiedSHA := hex.EncodeToString(h.Sum(nil))
	after, err := os.Lstat(sourcePath)
	if err != nil {
		return State{}, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, after) || after.Mode().Perm() != before.Mode().Perm() {
		return State{}, fmt.Errorf("source topology or mode changed while capturing: %s", sourcePath)
	}
	currentSHA, err := sha256File(sourcePath)
	if err != nil {
		return State{}, err
	}
	if currentSHA != copiedSHA {
		return State{}, fmt.Errorf("source changed while capturing: %s", sourcePath)
	}
	return State{Kind: KindRegular, SHA256: copiedSHA, Mode: uint32(before.Mode().Perm()), Payload: payloadName}, nil
}

func randomID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func transactionRoot(dataRoot string) (string, error) {
	root, err := filepath.Abs(dataRoot)
	if err != nil {
		return "", err
	}
	return filepath.Join(root, transactionsDirName), nil
}

func ensureTransactionRoot(dataRoot, txRoot string) error {
	if err := durablefs.MkdirAll(dataRoot, 0o755); err != nil {
		return err
	}
	info, err := os.Lstat(txRoot)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := durablefs.Mkdir(txRoot, 0o700); err != nil {
			return err
		}
		return nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("transaction root must be a real directory: %s", txRoot)
	}
	// The directory may be visible from an earlier mkdir whose parent sync did
	// not complete, and its journal entries may likewise be visible before the
	// transaction-root sync completed. Reaffirm both levels before publishing a
	// new intent or using an existing journal to mutate business state.
	if err := durablefs.SyncParent(txRoot); err != nil {
		return fmt.Errorf("persist transaction root entry %s: %w", txRoot, err)
	}
	if err := durablefs.SyncDir(txRoot); err != nil {
		return fmt.Errorf("persist transaction root contents %s: %w", txRoot, err)
	}
	return nil
}

func decodeIntent(path string) (Intent, error) {
	var intent Intent
	info, err := os.Lstat(path)
	if err != nil {
		return intent, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return intent, fmt.Errorf("transaction intent must be a regular file: %s", path)
	}
	if info.Size() > maxIntentBytes {
		return intent, fmt.Errorf("transaction intent too large: %d", info.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return intent, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&intent); err != nil {
		return intent, fmt.Errorf("decode transaction intent: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return intent, err
	}
	if err := validateIntent(intent); err != nil {
		return intent, err
	}
	return intent, nil
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("transaction intent contains trailing JSON")
		}
		return err
	}
	return nil
}

func validateIntent(intent Intent) error {
	if intent.SchemaVersion != journalSchemaVersion {
		return fmt.Errorf("unsupported transaction schema_version %d", intent.SchemaVersion)
	}
	if len(intent.ID) != 32 {
		return fmt.Errorf("invalid transaction id %q", intent.ID)
	}
	if _, err := hex.DecodeString(intent.ID); err != nil {
		return fmt.Errorf("invalid transaction id: %w", err)
	}
	if intent.Operation == "" || intent.Name == "" || len(intent.Mutations) == 0 || len(intent.Mutations) > 16 {
		return fmt.Errorf("incomplete transaction intent")
	}
	roles := map[string]struct{}{}
	paths := map[string]struct{}{}
	for _, mutation := range intent.Mutations {
		if mutation.Role == "" || strings.ContainsAny(mutation.Role, `/\\`) {
			return fmt.Errorf("invalid transaction role %q", mutation.Role)
		}
		if _, exists := roles[mutation.Role]; exists {
			return fmt.Errorf("duplicate transaction role %q", mutation.Role)
		}
		roles[mutation.Role] = struct{}{}
		if !filepath.IsAbs(mutation.Path) || filepath.Clean(mutation.Path) != mutation.Path {
			return fmt.Errorf("transaction path must be absolute and clean: %q", mutation.Path)
		}
		if _, exists := paths[mutation.Path]; exists {
			return fmt.Errorf("duplicate transaction path %q", mutation.Path)
		}
		paths[mutation.Path] = struct{}{}
		if err := validateState(mutation.Before); err != nil {
			return fmt.Errorf("invalid %s before state: %w", mutation.Role, err)
		}
		if err := validateState(mutation.After); err != nil {
			return fmt.Errorf("invalid %s after state: %w", mutation.Role, err)
		}
	}
	return nil
}

func validateState(state State) error {
	switch state.Kind {
	case KindAbsent:
		if state.SHA256 != "" || state.Mode != 0 || state.LinkTarget != "" || state.Payload != "" {
			return fmt.Errorf("absent state contains file metadata")
		}
	case KindRegular:
		if len(state.SHA256) != 64 || state.Payload == "" || filepath.Base(state.Payload) != state.Payload {
			return fmt.Errorf("regular state has invalid digest or payload")
		}
		if _, err := hex.DecodeString(state.SHA256); err != nil {
			return err
		}
		if state.Mode&^0o777 != 0 || state.LinkTarget != "" {
			return fmt.Errorf("regular state has invalid mode or link target")
		}
	case KindSymlink:
		if state.LinkTarget == "" || len(state.SHA256) != 64 || state.Payload != "" || state.Mode != 0 {
			return fmt.Errorf("symlink state has invalid metadata")
		}
		if _, err := hex.DecodeString(state.SHA256); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown state kind %q", state.Kind)
	}
	return nil
}

func sha256File(path string) (string, error) {
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
