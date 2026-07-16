// Package repair implements the deliberately narrow, plan-bound repair
// operations exposed by hukou v0.3. Planning is read-only. Applying a plan
// takes the state lock, re-observes every action input, and refuses to write
// business state when the observation no longer matches the plan.
package repair

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/durablefs"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/state"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

const (
	PlanSchemaVersion = 1
	maxPlanBytes      = 1 << 20
	maxManifestBytes  = 16 << 20
	maxIntentBytes    = 1 << 20
)

// Action is one of the two v0.3 repair operations. This is intentionally not
// a general-purpose repair registry.
type Action string

const (
	ActionRecoverTransaction    Action = "recover-transaction"
	ActionRestoreManifestBackup Action = "restore-manifest-backup"
	ActionPurgeQuarantine       Action = "purge-quarantine"
	ActionCleanLiveTemps        Action = "clean-live-temps"
)

// liveTransactionTempPrefix is the shared prefix of the temporary names that
// the transaction journal stages next to a live path before an atomic rename.
// It covers both the regular-file (".hukou-txn-*") and symlink
// (".hukou-txn-link-*") staging names.
const liveTransactionTempPrefix = ".hukou-txn-"

// liveTempMinAge is the minimum age an orphaned live transaction temporary must
// have before clean-live-temps will plan its removal. Age is an auxiliary
// guard, not the safety argument: the action additionally requires that no
// building or pending journal exists (no active transaction writer), and every
// planned deletion is re-verified against its full identity observation before
// removal.
const liveTempMinAge = time.Hour

// liveTempSHA256PrefixLen is the number of leading SHA-256 hex characters bound
// into a clean-live-temps target identity.
const liveTempSHA256PrefixLen = 16

func isSupportedAction(action Action) bool {
	switch action {
	case ActionRecoverTransaction, ActionRestoreManifestBackup, ActionPurgeQuarantine, ActionCleanLiveTemps:
		return true
	default:
		return false
	}
}

var (
	ErrInvalidPlan   = errors.New("invalid repair plan")
	ErrNotRepairable = errors.New("requested state is not safely repairable")
	ErrStateChanged  = errors.New("repair state changed after planning")
)

// Precondition records the fail-closed checks that were true when a plan was
// generated. Apply compares this list as well as the state fingerprint.
type Precondition struct {
	Code      string `json:"code"`
	Satisfied bool   `json:"satisfied"`
}

// Plan is a portable JSON description of one locally authorized repair.
// DataRootIdentity is an opaque digest; no absolute path is embedded, except
// that clean-live-temps targets necessarily name the exact temporary paths the
// plan is authorized to delete.
type Plan struct {
	SchemaVersion    int              `json:"schema_version"`
	Action           Action           `json:"action"`
	DataRootIdentity string           `json:"data_root_identity"`
	Preconditions    []Precondition   `json:"preconditions"`
	StateFingerprint string           `json:"state_fingerprint"`
	GeneratedAt      string           `json:"generated_at"`
	Targets          []LiveTempTarget `json:"targets,omitempty"`
}

// LiveTempTarget pins one planned live-temporary deletion to a strong identity
// observation captured at plan time: path, kind, permission bits, size,
// modification time, device/inode when the platform exposes them, and a
// SHA-256 prefix for regular files or the link target for symlinks. Apply
// re-verifies every recorded field and skips the item on any mismatch rather
// than deleting a resource the plan did not describe.
type LiveTempTarget struct {
	Path         string `json:"path"`
	Kind         string `json:"kind"`
	Mode         uint32 `json:"mode"`
	Size         int64  `json:"size,omitempty"`
	ModTime      string `json:"mod_time"`
	Dev          uint64 `json:"dev,omitempty"`
	Inode        uint64 `json:"inode,omitempty"`
	HasFileID    bool   `json:"has_file_id,omitempty"`
	SHA256Prefix string `json:"sha256_prefix,omitempty"`
	LinkTarget   string `json:"link_target,omitempty"`
}

// Result reports what an applied plan actually changed so the caller can
// surface it instead of discarding recovery evidence silently.
type Result struct {
	Quarantined      []statejournal.QuarantineRecord
	PurgedQuarantine []string
	RemovedLiveTemps []string
	SkippedLiveTemps []string
}

type evaluation struct {
	fingerprint   string
	preconditions []Precondition
	backup        []byte
	targets       []LiveTempTarget
}

// BuildPlan observes dataRoot without creating, locking, syncing, or changing
// any path. now is supplied by the caller so tests and automation can control
// the only nondeterministic field.
func BuildPlan(dataRoot string, action Action, now time.Time) (Plan, error) {
	root, err := existingDataRoot(dataRoot)
	if err != nil {
		return Plan{}, err
	}
	identity, err := dataRootIdentity(root)
	if err != nil {
		return Plan{}, err
	}
	eval, err := evaluate(root, action, now)
	if err != nil {
		return Plan{}, err
	}
	return Plan{
		SchemaVersion:    PlanSchemaVersion,
		Action:           action,
		DataRootIdentity: identity,
		Preconditions:    eval.preconditions,
		StateFingerprint: eval.fingerprint,
		GeneratedAt:      now.UTC().Format(time.RFC3339Nano),
		Targets:          eval.targets,
	}, nil
}

// WritePlan writes only the explicitly requested plan path, atomically and
// with owner-only permissions. The destination parent must already exist.
func WritePlan(path string, plan Plan) error {
	if err := validatePlan(plan); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: output path is required", ErrInvalidPlan)
	}
	parent := filepath.Dir(filepath.Clean(path))
	info, err := os.Stat(parent)
	if err != nil {
		return fmt.Errorf("inspect repair plan output parent: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repair plan output parent is not a directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("repair plan output must be a regular file or missing")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := encodePlan(plan)
	if err != nil {
		return err
	}
	return durablefs.AtomicWriteFile(path, data, 0o600)
}

// LoadPlan reads a bounded regular JSON file without following a symlink.
func LoadPlan(path string) (Plan, error) {
	data, err := readStableRegular(path, maxPlanBytes)
	if err != nil {
		return Plan{}, fmt.Errorf("read repair plan: %w", err)
	}
	var plan Plan
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return Plan{}, fmt.Errorf("%w: decode: %v", ErrInvalidPlan, err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return Plan{}, fmt.Errorf("%w: %v", ErrInvalidPlan, err)
	}
	if err := validatePlan(plan); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

// Apply obtains the mutation lock without invoking automatic recovery,
// re-evaluates the plan under that lock, and only then performs its one action.
// The returned Result reports what actually changed.
func Apply(dataRoot string, plan Plan) (result Result, retErr error) {
	if err := validatePlan(plan); err != nil {
		return result, err
	}
	root, err := existingDataRoot(dataRoot)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrStateChanged, err)
	}
	// Reaffirm the existing root's durability, but do not call the normal
	// mutation helper: that helper recovers the WAL before fingerprint checking.
	if err := durablefs.MkdirAll(root, 0o755); err != nil {
		return result, err
	}
	lock, err := state.Acquire(filepath.Join(root, "state.lock"))
	if err != nil {
		return result, err
	}
	defer func() {
		retErr = errors.Join(retErr, lock.Release())
	}()

	identity, err := dataRootIdentity(root)
	if err != nil {
		return result, fmt.Errorf("%w: identify data root: %v", ErrStateChanged, err)
	}
	if identity != plan.DataRootIdentity {
		return result, fmt.Errorf("%w: data root identity mismatch", ErrStateChanged)
	}
	// generated_at is validated as RFC3339Nano by validatePlan; it is reused as
	// the deterministic reference clock for any age-bound action so Apply and the
	// original plan compute an identical fingerprint.
	planTime, err := time.Parse(time.RFC3339Nano, plan.GeneratedAt)
	if err != nil {
		return result, fmt.Errorf("%w: generated_at: %v", ErrInvalidPlan, err)
	}

	if plan.Action == ActionCleanLiveTemps {
		// The deletion set was fixed at plan time and every target carries its
		// own identity observation, so state drift degrades to per-item skips
		// instead of a whole-plan failure. The fingerprint is recomputed from the
		// plan's own targets as a tamper-evidence hint; it is not an authorization
		// check, because each target is re-proved against current manifest and
		// directory reality immediately before removal.
		expected, err := cleanLiveTempsFingerprint(plan.Targets, planTime)
		if err != nil {
			return result, err
		}
		if expected != plan.StateFingerprint {
			return result, fmt.Errorf("%w: fingerprint does not match plan targets", ErrInvalidPlan)
		}
		if !reflect.DeepEqual(plan.Preconditions, cleanLiveTempsPreconditions()) {
			return result, fmt.Errorf("%w: preconditions mismatch", ErrInvalidPlan)
		}
		return result, applyCleanLiveTemps(root, plan, planTime, &result)
	}

	current, err := evaluate(root, plan.Action, planTime)
	if err != nil {
		return result, fmt.Errorf("%w: %v", ErrStateChanged, err)
	}
	if current.fingerprint != plan.StateFingerprint || !reflect.DeepEqual(current.preconditions, plan.Preconditions) {
		return result, fmt.Errorf("%w: fingerprint or preconditions mismatch", ErrStateChanged)
	}

	switch plan.Action {
	case ActionRecoverTransaction:
		summary, recoverErr := statejournal.Recover(root)
		result.Quarantined = summary.Quarantined
		return result, recoverErr
	case ActionRestoreManifestBackup:
		return result, durablefs.AtomicWriteFile(filepath.Join(root, "manifest.json"), current.backup, 0o600)
	case ActionPurgeQuarantine:
		removed, purgeErr := statejournal.PurgeQuarantined(root)
		result.PurgedQuarantine = removed
		return result, purgeErr
	default:
		return result, fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, plan.Action)
	}
}

func evaluate(root string, action Action, now time.Time) (evaluation, error) {
	switch action {
	case ActionRecoverTransaction:
		return evaluateRecoverTransaction(root)
	case ActionRestoreManifestBackup:
		return evaluateRestoreManifestBackup(root)
	case ActionPurgeQuarantine:
		return evaluatePurgeQuarantine(root)
	case ActionCleanLiveTemps:
		return evaluateCleanLiveTemps(root, now)
	default:
		return evaluation{}, fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, action)
	}
}

type recoverObservation struct {
	Action       Action              `json:"action"`
	Status       statejournal.Status `json:"status"`
	JournalNodes []nodeObservation   `json:"journal_nodes"`
	Targets      []nodeObservation   `json:"targets"`
}

func evaluateRecoverTransaction(root string) (evaluation, error) {
	status, err := statejournal.Inspect(root)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect transaction state: %v", ErrNotRepairable, err)
	}
	if !status.NeedsRecovery() {
		return evaluation{}, fmt.Errorf("%w: no unfinished transaction state exists", ErrNotRepairable)
	}
	txRoot := filepath.Join(root, "transactions")
	// Unknown non-directory entries no longer block recovery: Apply routes
	// through statejournal.Recover, which quarantines each of them (preserving
	// the data) before converging the known journals. Unknown directories stay
	// fail-closed exactly like Recover itself: they may be journal layouts from
	// a newer hukou. The full transaction tree is still captured in the
	// fingerprint below, so any change to those entries between planning and
	// apply fails closed.
	for _, name := range status.Unknown {
		info, err := os.Lstat(filepath.Join(txRoot, name))
		if err != nil {
			return evaluation{}, fmt.Errorf("%w: inspect unknown transaction entry: %v", ErrNotRepairable, err)
		}
		if info.Mode()&os.ModeSymlink == 0 && info.IsDir() {
			return evaluation{}, fmt.Errorf("%w: transaction root contains unknown directory %q; it may be a journal from a newer hukou and must be inspected manually", ErrNotRepairable, name)
		}
	}
	if len(status.Pending) > 1 {
		return evaluation{}, fmt.Errorf("%w: multiple pending transactions are ambiguous", ErrNotRepairable)
	}
	nodes, err := observeTree(txRoot)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect transaction tree: %v", ErrNotRepairable, err)
	}
	for _, name := range append(append(append([]string{}, status.Building...), status.Pending...), status.Completed...) {
		info, err := os.Lstat(filepath.Join(txRoot, name))
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return evaluation{}, fmt.Errorf("%w: transaction entry %q is not a real directory", ErrNotRepairable, name)
		}
	}

	targetPaths := make([]string, 0)
	if len(status.Pending) == 1 {
		pendingName := status.Pending[0]
		pendingDir := filepath.Join(txRoot, pendingName)
		intent, err := loadAndValidateIntent(pendingDir, pendingName)
		if err != nil {
			return evaluation{}, fmt.Errorf("%w: %v", ErrNotRepairable, err)
		}
		if err := validateCommitMarker(pendingDir, intent.ID); err != nil {
			return evaluation{}, fmt.Errorf("%w: %v", ErrNotRepairable, err)
		}
		for _, mutation := range intent.Mutations {
			before, err := matchesTransactionState(mutation.Path, mutation.Before)
			if err != nil {
				return evaluation{}, fmt.Errorf("%w: classify transaction target: %v", ErrNotRepairable, err)
			}
			after, err := matchesTransactionState(mutation.Path, mutation.After)
			if err != nil {
				return evaluation{}, fmt.Errorf("%w: classify transaction target: %v", ErrNotRepairable, err)
			}
			if !before && !after {
				return evaluation{}, fmt.Errorf("%w: a transaction target has unknown drift", ErrNotRepairable)
			}
			if err := validateStatePayload(pendingDir, mutation.Before); err != nil {
				return evaluation{}, fmt.Errorf("%w: invalid before payload: %v", ErrNotRepairable, err)
			}
			if err := validateStatePayload(pendingDir, mutation.After); err != nil {
				return evaluation{}, fmt.Errorf("%w: invalid after payload: %v", ErrNotRepairable, err)
			}
			targetPaths = append(targetPaths, mutation.Path)
		}
	}
	sort.Strings(targetPaths)
	targets := make([]nodeObservation, 0, len(targetPaths))
	for _, path := range targetPaths {
		observed, err := observeNode(path, path)
		if err != nil {
			return evaluation{}, fmt.Errorf("%w: observe transaction target: %v", ErrNotRepairable, err)
		}
		if observed.Kind == "symlink" {
			sha, size, err := hashStableRegular(path)
			if err != nil {
				return evaluation{}, fmt.Errorf("%w: hash transaction symlink target: %v", ErrNotRepairable, err)
			}
			observed.ContentSHA256 = sha
			observed.Size = size
		}
		targets = append(targets, observed)
	}
	observation := recoverObservation{
		Action:       ActionRecoverTransaction,
		Status:       status,
		JournalNodes: nodes,
		Targets:      targets,
	}
	fingerprint, err := fingerprint(observation)
	if err != nil {
		return evaluation{}, err
	}
	preconditions := []Precondition{
		{Code: "unfinished_transaction_state_present", Satisfied: true},
		{Code: "transaction_topology_recoverable", Satisfied: true},
	}
	if len(status.Pending) == 1 {
		preconditions = append(preconditions, Precondition{Code: "pending_transaction_inputs_valid", Satisfied: true})
	}
	return evaluation{fingerprint: fingerprint, preconditions: preconditions}, nil
}

type restoreObservation struct {
	Action       Action              `json:"action"`
	Transactions statejournal.Status `json:"transactions"`
	Main         nodeObservation     `json:"main"`
	Backup       nodeObservation     `json:"backup"`
	Live         []nodeObservation   `json:"live"`
}

func evaluateRestoreManifestBackup(root string) (evaluation, error) {
	transactionStatus, err := statejournal.Inspect(root)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect transaction state: %v", ErrNotRepairable, err)
	}
	if transactionStatus.NeedsRecovery() {
		return evaluation{}, fmt.Errorf("%w: recover unfinished transaction state before restoring a manifest backup", ErrNotRepairable)
	}
	mainPath := filepath.Join(root, "manifest.json")
	backupPath := mainPath + ".bak"
	main, mainRaw, err := observeBoundedRegularOrMissing(mainPath, maxManifestBytes)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect current manifest: %v", ErrNotRepairable, err)
	}
	if main.Kind == "regular" {
		if _, err := decodeAndValidateManifest(mainRaw); err == nil {
			return evaluation{}, fmt.Errorf("%w: current manifest is already valid", ErrNotRepairable)
		}
	} else if main.Kind != "missing" {
		return evaluation{}, fmt.Errorf("%w: current manifest must be missing or a readable invalid regular file", ErrNotRepairable)
	}

	backup, backupRaw, err := observeBoundedRegularOrMissing(backupPath, maxManifestBytes)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect manifest backup: %v", ErrNotRepairable, err)
	}
	if backup.Kind != "regular" {
		return evaluation{}, fmt.Errorf("%w: manifest backup is missing or not regular", ErrNotRepairable)
	}
	backupManifest, err := decodeAndValidateManifest(backupRaw)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: manifest backup is not semantically valid: %v", ErrNotRepairable, err)
	}

	entries := append([]manifest.Entry(nil), backupManifest.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Path != entries[j].Path {
			return entries[i].Path < entries[j].Path
		}
		return entries[i].Name < entries[j].Name
	})
	live := make([]nodeObservation, 0, len(entries))
	for _, entry := range entries {
		observed, err := observeLive(entry.Path)
		if err != nil {
			return evaluation{}, fmt.Errorf("%w: live file for %q cannot be verified: %v", ErrNotRepairable, entry.Name, err)
		}
		if !strings.EqualFold(observed.ContentSHA256, entry.SHA256) {
			return evaluation{}, fmt.Errorf("%w: live SHA-256 does not match backup for %q", ErrNotRepairable, entry.Name)
		}
		live = append(live, observed)
	}
	observation := restoreObservation{
		Action:       ActionRestoreManifestBackup,
		Transactions: transactionStatus,
		Main:         main,
		Backup:       backup,
		Live:         live,
	}
	fingerprint, err := fingerprint(observation)
	if err != nil {
		return evaluation{}, err
	}
	return evaluation{
		fingerprint: fingerprint,
		preconditions: []Precondition{
			{Code: "transaction_state_clean", Satisfied: true},
			{Code: "current_manifest_missing_or_invalid", Satisfied: true},
			{Code: "manifest_backup_semantically_valid", Satisfied: true},
			{Code: "live_sha256_matches_backup", Satisfied: true},
		},
		backup: append([]byte(nil), backupRaw...),
	}, nil
}

type purgeQuarantineObservation struct {
	Action Action            `json:"action"`
	Nodes  []nodeObservation `json:"nodes"`
}

func evaluatePurgeQuarantine(root string) (evaluation, error) {
	status, err := statejournal.Inspect(root)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect transaction state: %v", ErrNotRepairable, err)
	}
	if len(status.Quarantined) == 0 {
		return evaluation{}, fmt.Errorf("%w: no quarantined transaction entries exist", ErrNotRepairable)
	}
	txRoot := filepath.Join(root, "transactions")
	nodes := make([]nodeObservation, 0, len(status.Quarantined))
	for _, name := range status.Quarantined {
		path := filepath.Join(txRoot, name)
		observed, err := observeNode(path, name)
		if err != nil {
			return evaluation{}, fmt.Errorf("%w: observe quarantined entry: %v", ErrNotRepairable, err)
		}
		nodes = append(nodes, observed)
		if observed.Kind == "directory" {
			subtree, err := observeTree(path)
			if err != nil {
				return evaluation{}, fmt.Errorf("%w: observe quarantined subtree: %v", ErrNotRepairable, err)
			}
			for _, child := range subtree {
				if child.Name == "." {
					continue
				}
				child.Name = filepath.Join(name, child.Name)
				nodes = append(nodes, child)
			}
		}
	}
	fp, err := fingerprint(purgeQuarantineObservation{Action: ActionPurgeQuarantine, Nodes: nodes})
	if err != nil {
		return evaluation{}, err
	}
	return evaluation{
		fingerprint:   fp,
		preconditions: []Precondition{{Code: "quarantined_entries_present", Satisfied: true}},
	}, nil
}

type cleanLiveTempsObservation struct {
	Action  Action           `json:"action"`
	Cutoff  string           `json:"cutoff"`
	Targets []LiveTempTarget `json:"targets"`
}

// cleanLiveTempsFingerprint is computable from plan content alone: Apply
// recomputes it from the plan's own targets and reference clock, so a tampered
// or internally inconsistent plan document is rejected without touching disk.
func cleanLiveTempsFingerprint(targets []LiveTempTarget, planTime time.Time) (string, error) {
	return fingerprint(cleanLiveTempsObservation{
		Action:  ActionCleanLiveTemps,
		Cutoff:  planTime.Add(-liveTempMinAge).UTC().Format(time.RFC3339Nano),
		Targets: targets,
	})
}

func cleanLiveTempsPreconditions() []Precondition {
	return []Precondition{
		{Code: "no_active_transaction_journals", Satisfied: true},
		{Code: "orphan_live_transaction_temps_present", Satisfied: true},
	}
}

// fileIdentity is the physical device/inode identity of a filesystem node.
type fileIdentity struct {
	dev uint64
	ino uint64
}

func (id fileIdentity) matches(dev, ino uint64) bool {
	return id.dev == dev && id.ino == ino
}

func fileIdentityFromInfo(info os.FileInfo) (fileIdentity, bool) {
	dev, ino, ok := fileID(info)
	return fileIdentity{dev: dev, ino: ino}, ok
}

// liveIdentitySet captures the physical directories that contain manifest live
// paths and the physical files that are manifest live paths themselves. On
// platforms without stable file IDs the path string is retained as a fallback.
type liveIdentitySet struct {
	dirIDs    map[fileIdentity]struct{}
	dirPaths  map[string]struct{}
	fileIDs   map[fileIdentity]struct{}
	filePaths map[string]struct{}
}

// collectLiveIdentities resolves every manifest entry to its parent directory
// identity and to its own file identity. Directories are deduplicated by
// device/inode, not by path string, so a live directory reached through a
// symlink is not accidentally trusted.
func collectLiveIdentities(m *manifest.Manifest) liveIdentitySet {
	s := liveIdentitySet{
		dirIDs:    make(map[fileIdentity]struct{}),
		dirPaths:  make(map[string]struct{}),
		fileIDs:   make(map[fileIdentity]struct{}),
		filePaths: make(map[string]struct{}),
	}
	for _, entry := range m.Entries {
		if entry.Path == "" || !filepath.IsAbs(entry.Path) {
			continue
		}
		clean := filepath.Clean(entry.Path)
		s.filePaths[clean] = struct{}{}
		if info, err := os.Lstat(clean); err == nil {
			if id, ok := fileIdentityFromInfo(info); ok {
				s.fileIDs[id] = struct{}{}
			}
		}
		parent := filepath.Dir(clean)
		if _, seen := s.dirPaths[parent]; seen {
			continue
		}
		info, err := os.Lstat(parent)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			continue
		}
		if id, ok := fileIdentityFromInfo(info); ok {
			s.dirIDs[id] = struct{}{}
		}
		s.dirPaths[parent] = struct{}{}
	}
	return s
}

func (s liveIdentitySet) dirs() []string {
	dirs := make([]string, 0, len(s.dirPaths))
	for dir := range s.dirPaths {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return dirs
}

func (s liveIdentitySet) containsDir(info os.FileInfo, path string) bool {
	if id, ok := fileIdentityFromInfo(info); ok {
		_, in := s.dirIDs[id]
		return in
	}
	_, in := s.dirPaths[path]
	return in
}

func (s liveIdentitySet) isLivePath(info os.FileInfo, path string) bool {
	if id, ok := fileIdentityFromInfo(info); ok {
		_, in := s.fileIDs[id]
		return in
	}
	_, in := s.filePaths[path]
	return in
}

// testBeforeCleanLiveTempRemove is a deterministic race seam. Production leaves
// it nil; package tests use it to swap a target after re-validation but before
// the fd-anchored removal.
var testBeforeCleanLiveTempRemove func(string)

func evaluateCleanLiveTemps(root string, now time.Time) (evaluation, error) {
	// Age alone cannot prove a temporary is orphaned: a slow copy inside a live
	// transaction can legitimately exceed any threshold. Require that no
	// building or pending journal exists, so there is provably no hukou writer
	// whose staged temporaries could be selected.
	status, err := statejournal.Inspect(root)
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: inspect transaction state: %v", ErrNotRepairable, err)
	}
	if len(status.Building)+len(status.Pending) > 0 {
		return evaluation{}, fmt.Errorf("%w: transaction journals exist (building=%d pending=%d); recover them before cleaning live temporaries", ErrNotRepairable, len(status.Building), len(status.Pending))
	}
	m, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		return evaluation{}, fmt.Errorf("%w: load manifest: %v", ErrNotRepairable, err)
	}
	live := collectLiveIdentities(m)

	cutoff := now.Add(-liveTempMinAge)
	targets := make([]LiveTempTarget, 0)
	for _, dir := range live.dirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return evaluation{}, fmt.Errorf("%w: enumerate live directory: %v", ErrNotRepairable, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, liveTransactionTempPrefix) {
				continue
			}
			full := filepath.Join(dir, name)
			info, err := os.Lstat(full)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return evaluation{}, fmt.Errorf("%w: inspect live temporary: %v", ErrNotRepairable, err)
			}
			if live.isLivePath(info, full) {
				// Registered live paths are permanently excluded, even when their
				// basename carries the temporary prefix. The exclusion prefers the
				// physical file identity over the path string.
				continue
			}
			symlink := info.Mode()&os.ModeSymlink != 0
			if !symlink && !info.Mode().IsRegular() {
				continue
			}
			if !info.ModTime().Before(cutoff) {
				continue
			}
			target := LiveTempTarget{
				Path:    full,
				Mode:    uint32(info.Mode().Perm()),
				ModTime: info.ModTime().UTC().Format(time.RFC3339Nano),
			}
			if dev, inode, ok := fileID(info); ok {
				target.Dev, target.Inode, target.HasFileID = dev, inode, true
			}
			if symlink {
				target.Kind = "symlink"
				linkTarget, err := os.Readlink(full)
				if err != nil {
					return evaluation{}, fmt.Errorf("%w: read live temporary link: %v", ErrNotRepairable, err)
				}
				target.LinkTarget = linkTarget
			} else {
				target.Kind = "regular"
				target.Size = info.Size()
				sha, _, err := hashStableRegular(full)
				if err != nil {
					return evaluation{}, fmt.Errorf("%w: hash live temporary: %v", ErrNotRepairable, err)
				}
				target.SHA256Prefix = sha[:liveTempSHA256PrefixLen]
			}
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return evaluation{}, fmt.Errorf("%w: no orphaned live transaction temporaries older than %s exist", ErrNotRepairable, liveTempMinAge)
	}
	fp, err := cleanLiveTempsFingerprint(targets, now)
	if err != nil {
		return evaluation{}, err
	}
	return evaluation{
		fingerprint:   fp,
		preconditions: cleanLiveTempsPreconditions(),
		targets:       targets,
	}, nil
}

// applyCleanLiveTemps deletes exactly the planned targets. The plan's
// fingerprint is retained only as a tamper-evidence hint; every target is
// re-proved against the current manifest and directory reality under the
// mutation lock before removal. A mismatch, a disappearance, a path that is
// now a registered live path, or an age that no longer satisfies the cutoff
// downgrades that item to a skip instead of a deletion.
func applyCleanLiveTemps(root string, plan Plan, planTime time.Time, result *Result) error {
	status, err := statejournal.Inspect(root)
	if err != nil {
		return fmt.Errorf("%w: inspect transaction state: %v", ErrStateChanged, err)
	}
	if len(status.Building)+len(status.Pending) > 0 {
		return fmt.Errorf("%w: transaction journals appeared after planning; their staged temporaries may still be needed", ErrStateChanged)
	}
	m, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		return fmt.Errorf("%w: load manifest: %v", ErrStateChanged, err)
	}
	live := collectLiveIdentities(m)
	cutoff := planTime.Add(-liveTempMinAge)
	for _, target := range plan.Targets {
		ok, err := revalidateLiveTempTarget(target, cutoff, live)
		if err != nil {
			return err
		}
		if !ok {
			result.SkippedLiveTemps = append(result.SkippedLiveTemps, target.Path)
			continue
		}
		parent := filepath.Dir(target.Path)
		rootFd, err := os.OpenRoot(parent)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				result.SkippedLiveTemps = append(result.SkippedLiveTemps, target.Path)
				continue
			}
			return fmt.Errorf("anchor live temporary parent %s: %w", parent, err)
		}
		if testBeforeCleanLiveTempRemove != nil {
			testBeforeCleanLiveTempRemove(target.Path)
		}
		if err := rootFd.Remove(filepath.Base(target.Path)); err != nil {
			_ = rootFd.Close()
			if errors.Is(err, os.ErrNotExist) {
				result.SkippedLiveTemps = append(result.SkippedLiveTemps, target.Path)
				continue
			}
			return fmt.Errorf("remove live transaction temporary %s: %w", target.Path, err)
		}
		if err := rootFd.Close(); err != nil {
			return fmt.Errorf("close live temporary parent %s: %w", parent, err)
		}
		result.RemovedLiveTemps = append(result.RemovedLiveTemps, target.Path)
	}
	return nil
}

// revalidateLiveTempTarget re-proves one planned target against the current
// manifest and directory reality. It returns true only when the target is still
// inside a manifest live directory, is not a registered live path itself, still
// carries the transaction temporary prefix, matches the recorded seven-field
// identity (kind, permissions, size, mtime, dev/inode, and SHA-256 prefix or
// symlink target), and is still older than the age cutoff.
func revalidateLiveTempTarget(target LiveTempTarget, cutoff time.Time, live liveIdentitySet) (bool, error) {
	path := filepath.Clean(target.Path)
	if !strings.HasPrefix(filepath.Base(path), liveTransactionTempPrefix) {
		return false, nil
	}
	parent := filepath.Dir(path)
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect live temporary parent %s: %w", parent, err)
	}
	if !live.containsDir(parentInfo, parent) {
		return false, nil
	}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("inspect live transaction temporary %s: %w", path, err)
	}
	if live.isLivePath(info, path) {
		return false, nil
	}
	symlink := info.Mode()&os.ModeSymlink != 0
	switch target.Kind {
	case "symlink":
		if !symlink {
			return false, nil
		}
		linkTarget, err := os.Readlink(path)
		if err != nil || linkTarget != target.LinkTarget {
			return false, nil
		}
	case "regular":
		if symlink || !info.Mode().IsRegular() || info.Size() != target.Size {
			return false, nil
		}
		sha, _, err := hashStableRegular(path)
		if err != nil || !strings.HasPrefix(sha, target.SHA256Prefix) {
			return false, nil
		}
	default:
		return false, nil
	}
	if uint32(info.Mode().Perm()) != target.Mode {
		return false, nil
	}
	if info.ModTime().UTC().Format(time.RFC3339Nano) != target.ModTime {
		return false, nil
	}
	if target.HasFileID {
		dev, ino, ok := fileID(info)
		if !ok || dev != target.Dev || ino != target.Inode {
			return false, nil
		}
	}
	if !info.ModTime().Before(cutoff) {
		return false, nil
	}
	return true, nil
}

func existingDataRoot(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("data root is required")
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil {
		return "", fmt.Errorf("data root must already exist: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("data root is not a directory")
	}
	return root, nil
}

func dataRootIdentity(root string) (string, error) {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte("hukou-repair-data-root-v1\x00" + filepath.Clean(root) + "\x00" + filepath.Clean(resolved)))
	return hex.EncodeToString(sum[:]), nil
}

func validatePlan(plan Plan) error {
	if plan.SchemaVersion != PlanSchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrInvalidPlan, plan.SchemaVersion)
	}
	if !isSupportedAction(plan.Action) {
		return fmt.Errorf("%w: unsupported action %q", ErrInvalidPlan, plan.Action)
	}
	if !validSHA256(plan.DataRootIdentity) || !validSHA256(plan.StateFingerprint) {
		return fmt.Errorf("%w: identity and fingerprint must be SHA-256 digests", ErrInvalidPlan)
	}
	if _, err := time.Parse(time.RFC3339Nano, plan.GeneratedAt); err != nil {
		return fmt.Errorf("%w: generated_at: %v", ErrInvalidPlan, err)
	}
	if len(plan.Preconditions) == 0 {
		return fmt.Errorf("%w: preconditions are required", ErrInvalidPlan)
	}
	seen := make(map[string]struct{}, len(plan.Preconditions))
	for _, condition := range plan.Preconditions {
		if condition.Code == "" || !condition.Satisfied {
			return fmt.Errorf("%w: every precondition must be named and satisfied", ErrInvalidPlan)
		}
		if _, exists := seen[condition.Code]; exists {
			return fmt.Errorf("%w: duplicate precondition %q", ErrInvalidPlan, condition.Code)
		}
		seen[condition.Code] = struct{}{}
	}
	return validatePlanTargets(plan)
}

func validatePlanTargets(plan Plan) error {
	if plan.Action != ActionCleanLiveTemps {
		if len(plan.Targets) != 0 {
			return fmt.Errorf("%w: targets are only valid for %s", ErrInvalidPlan, ActionCleanLiveTemps)
		}
		return nil
	}
	if len(plan.Targets) == 0 {
		return fmt.Errorf("%w: %s requires at least one target", ErrInvalidPlan, ActionCleanLiveTemps)
	}
	seenPaths := make(map[string]struct{}, len(plan.Targets))
	for _, target := range plan.Targets {
		if !filepath.IsAbs(target.Path) || filepath.Clean(target.Path) != target.Path {
			return fmt.Errorf("%w: target path must be absolute and clean: %q", ErrInvalidPlan, target.Path)
		}
		if !strings.HasPrefix(filepath.Base(target.Path), liveTransactionTempPrefix) {
			return fmt.Errorf("%w: target %q lacks the transaction temporary prefix", ErrInvalidPlan, target.Path)
		}
		if _, exists := seenPaths[target.Path]; exists {
			return fmt.Errorf("%w: duplicate target path %q", ErrInvalidPlan, target.Path)
		}
		seenPaths[target.Path] = struct{}{}
		if _, err := time.Parse(time.RFC3339Nano, target.ModTime); err != nil {
			return fmt.Errorf("%w: target mod_time: %v", ErrInvalidPlan, err)
		}
		if target.Mode&^0o777 != 0 {
			return fmt.Errorf("%w: target mode contains non-permission bits", ErrInvalidPlan)
		}
		switch target.Kind {
		case "regular":
			if len(target.SHA256Prefix) != liveTempSHA256PrefixLen || !validHex(target.SHA256Prefix) {
				return fmt.Errorf("%w: regular target requires a %d-character SHA-256 prefix", ErrInvalidPlan, liveTempSHA256PrefixLen)
			}
			if target.LinkTarget != "" {
				return fmt.Errorf("%w: regular target must not carry a link target", ErrInvalidPlan)
			}
		case "symlink":
			if target.LinkTarget == "" || target.SHA256Prefix != "" || target.Size != 0 {
				return fmt.Errorf("%w: symlink target requires a link target and no content metadata", ErrInvalidPlan)
			}
		default:
			return fmt.Errorf("%w: unsupported target kind %q", ErrInvalidPlan, target.Kind)
		}
	}
	return nil
}

func encodePlan(plan Plan) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(plan); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func fingerprint(value any) (string, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

type nodeObservation struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Mode          uint32 `json:"mode,omitempty"`
	Size          int64  `json:"size,omitempty"`
	ContentSHA256 string `json:"content_sha256,omitempty"`
	LinkTarget    string `json:"link_target,omitempty"`
}

func observeTree(root string) ([]nodeObservation, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
		return nil, fmt.Errorf("tree root is not a real directory")
	}
	result := []nodeObservation{{Name: ".", Kind: "directory", Mode: uint32(rootInfo.Mode().Perm())}}
	var walk func(string, string) error
	walk = func(dir, relative string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			name := filepath.Join(relative, entry.Name())
			observed, err := observeNode(path, name)
			if err != nil {
				return err
			}
			result = append(result, observed)
			if observed.Kind == "directory" {
				if err := walk(path, name); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, "."); err != nil {
		return nil, err
	}
	return result, nil
}

func observeNode(path, name string) (nodeObservation, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nodeObservation{Name: name, Kind: "missing"}, nil
		}
		return nodeObservation{}, err
	}
	result := nodeObservation{Name: name, Mode: uint32(info.Mode().Perm())}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(path)
		if err != nil {
			return nodeObservation{}, err
		}
		result.Kind = "symlink"
		result.LinkTarget = target
	case info.IsDir():
		result.Kind = "directory"
	case info.Mode().IsRegular():
		sha, size, err := hashStableRegular(path)
		if err != nil {
			return nodeObservation{}, err
		}
		result.Kind = "regular"
		result.Size = size
		result.ContentSHA256 = sha
	default:
		result.Kind = "other"
		result.Size = info.Size()
	}
	return result, nil
}

func observeBoundedRegularOrMissing(path string, limit int64) (nodeObservation, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nodeObservation{Name: filepath.Base(path), Kind: "missing"}, nil, nil
		}
		return nodeObservation{}, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, readErr := os.Readlink(path)
		return nodeObservation{Name: filepath.Base(path), Kind: "symlink", LinkTarget: target}, nil, readErr
	}
	if !info.Mode().IsRegular() {
		return nodeObservation{Name: filepath.Base(path), Kind: "other", Mode: uint32(info.Mode().Perm())}, nil, nil
	}
	data, err := readStableRegular(path, limit)
	if err != nil {
		return nodeObservation{}, nil, err
	}
	sum := sha256.Sum256(data)
	return nodeObservation{
		Name:          filepath.Base(path),
		Kind:          "regular",
		Mode:          uint32(info.Mode().Perm()),
		Size:          int64(len(data)),
		ContentSHA256: hex.EncodeToString(sum[:]),
	}, data, nil
}

func observeLive(path string) (nodeObservation, error) {
	observed, err := observeNode(path, path)
	if err != nil {
		return nodeObservation{}, err
	}
	if observed.Kind == "symlink" {
		sha, size, err := hashStableRegular(path)
		if err != nil {
			return nodeObservation{}, err
		}
		observed.Size = size
		observed.ContentSHA256 = sha
		return observed, nil
	}
	if observed.Kind != "regular" {
		return nodeObservation{}, fmt.Errorf("live path is not a regular file or symlink to one")
	}
	return observed, nil
}

func hashStableRegular(path string) (string, int64, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if before.Mode()&os.ModeSymlink != 0 {
		before, err = os.Stat(path)
		if err != nil {
			return "", 0, err
		}
	}
	if !before.Mode().IsRegular() {
		return "", 0, fmt.Errorf("path does not resolve to a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return "", 0, fmt.Errorf("regular file changed while opening")
	}
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	after, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if !os.SameFile(opened, after) || after.Size() != n {
		return "", 0, fmt.Errorf("regular file changed while hashing")
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

func readStableRegular(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("path is not a regular file")
	}
	if before.Size() > limit {
		return nil, fmt.Errorf("file exceeds %d-byte limit", limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("file changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("file exceeds %d-byte limit", limit)
	}
	after, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(opened, after) || after.Size() != int64(len(data)) {
		return nil, fmt.Errorf("file changed while reading")
	}
	return data, nil
}

func decodeAndValidateManifest(data []byte) (*manifest.Manifest, error) {
	return manifest.Decode(data)
}

func loadAndValidateIntent(dir, pendingName string) (statejournal.Intent, error) {
	data, err := readStableRegular(filepath.Join(dir, "intent.json"), maxIntentBytes)
	if err != nil {
		return statejournal.Intent{}, fmt.Errorf("read pending transaction intent: %w", err)
	}
	var intent statejournal.Intent
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&intent); err != nil {
		return statejournal.Intent{}, fmt.Errorf("decode pending transaction intent: %w", err)
	}
	if err := ensureJSONEOF(dec); err != nil {
		return statejournal.Intent{}, err
	}
	if intent.SchemaVersion != 1 || len(intent.ID) != 32 {
		return statejournal.Intent{}, fmt.Errorf("invalid transaction schema or id")
	}
	if _, err := hex.DecodeString(intent.ID); err != nil || pendingName != "pending-"+intent.ID {
		return statejournal.Intent{}, fmt.Errorf("transaction directory and id do not match")
	}
	if intent.Operation == "" || intent.Name == "" || len(intent.Mutations) == 0 || len(intent.Mutations) > 16 {
		return statejournal.Intent{}, fmt.Errorf("incomplete transaction intent")
	}
	roles := make(map[string]struct{}, len(intent.Mutations))
	paths := make(map[string]struct{}, len(intent.Mutations))
	for _, mutation := range intent.Mutations {
		if mutation.Role == "" || strings.ContainsAny(mutation.Role, `/\\`) {
			return statejournal.Intent{}, fmt.Errorf("invalid transaction role")
		}
		if _, exists := roles[mutation.Role]; exists {
			return statejournal.Intent{}, fmt.Errorf("duplicate transaction role")
		}
		roles[mutation.Role] = struct{}{}
		if !filepath.IsAbs(mutation.Path) || filepath.Clean(mutation.Path) != mutation.Path {
			return statejournal.Intent{}, fmt.Errorf("transaction path is not absolute and clean")
		}
		if _, exists := paths[mutation.Path]; exists {
			return statejournal.Intent{}, fmt.Errorf("duplicate transaction path")
		}
		paths[mutation.Path] = struct{}{}
		if err := validateTransactionState(mutation.Before); err != nil {
			return statejournal.Intent{}, err
		}
		if err := validateTransactionState(mutation.After); err != nil {
			return statejournal.Intent{}, err
		}
	}
	return intent, nil
}

func validateTransactionState(value statejournal.State) error {
	switch value.Kind {
	case statejournal.KindAbsent:
		if value.SHA256 != "" || value.Mode != 0 || value.LinkTarget != "" || value.Payload != "" {
			return fmt.Errorf("invalid absent transaction state")
		}
	case statejournal.KindRegular:
		if !validSHA256(value.SHA256) || value.Payload == "" || filepath.Base(value.Payload) != value.Payload || value.Mode&^0o777 != 0 || value.LinkTarget != "" {
			return fmt.Errorf("invalid regular transaction state")
		}
	case statejournal.KindSymlink:
		if value.LinkTarget == "" || !validSHA256(value.SHA256) || value.Payload != "" || value.Mode != 0 {
			return fmt.Errorf("invalid symlink transaction state")
		}
	default:
		return fmt.Errorf("unknown transaction state kind")
	}
	return nil
}

func validateCommitMarker(dir, id string) error {
	path := filepath.Join(dir, "COMMIT")
	data, err := readStableRegular(path, 256)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect transaction commit marker: %w", err)
	}
	if string(data) != id+"\n" {
		return fmt.Errorf("invalid transaction commit marker")
	}
	return nil
}

func validateStatePayload(dir string, value statejournal.State) error {
	if value.Kind != statejournal.KindRegular {
		return nil
	}
	sha, _, err := hashStableRegular(filepath.Join(dir, value.Payload))
	if err != nil {
		return err
	}
	if sha != value.SHA256 {
		return fmt.Errorf("transaction payload SHA-256 mismatch")
	}
	return nil
}

func matchesTransactionState(path string, value statejournal.State) (bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return value.Kind == statejournal.KindAbsent, nil
		}
		return false, err
	}
	switch value.Kind {
	case statejournal.KindAbsent:
		return false, nil
	case statejournal.KindRegular:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != value.Mode {
			return false, nil
		}
		sha, _, err := hashStableRegular(path)
		return sha == value.SHA256, err
	case statejournal.KindSymlink:
		if info.Mode()&os.ModeSymlink == 0 {
			return false, nil
		}
		target, err := os.Readlink(path)
		if err != nil || target != value.LinkTarget {
			return false, err
		}
		sha, _, err := hashStableRegular(path)
		return sha == value.SHA256, err
	default:
		return false, fmt.Errorf("unknown transaction state kind")
	}
}

func ensureJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return err
	}
	return nil
}

func validHex(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
