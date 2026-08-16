package doctor

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

const maxManifestSize = 16 << 20

type manifestAudit struct {
	available bool
	parsed    bool
	trusted   bool
	entries   []manifest.Entry
	byName    map[string]manifest.Entry
}

type fileAudit struct {
	path    string
	sha256  string
	hashOK  bool
	valid   bool
	present bool
}

type toolAudit struct {
	original fileAudit
	versions map[string]fileAudit
}

// Scan audits a data root without creating, removing, renaming, locking, or
// otherwise changing any path. It performs no network operations.
func Scan(opts Options) Report {
	opts.DataRoot = filepath.Clean(opts.DataRoot)
	report := newReport(opts)
	report.ManifestPath = filepath.Join(opts.DataRoot, "manifest.json")

	if opts.DataRoot == "" || opts.DataRoot == "." {
		report.incomplete("DATA_ROOT_INVALID", "state", "", opts.DataRoot, "data root is empty or ambiguous")
		report.finalize()
		return report
	}

	rootInfo, err := os.Lstat(opts.DataRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.finalize()
			return report
		}
		report.incomplete("DATA_ROOT_STAT_FAILED", "state", "", opts.DataRoot, "cannot inspect data root: %v", err)
		report.finalize()
		return report
	}
	if rootInfo.Mode()&os.ModeSymlink != 0 {
		report.add(SeverityInfo, "DATA_ROOT_SYMLINK", "state", "", opts.DataRoot, "data root is a symlink; child topology is still checked without following child symlinks")
		rootInfo, err = os.Stat(opts.DataRoot)
		if err != nil {
			report.incomplete("DATA_ROOT_TARGET_FAILED", "state", "", opts.DataRoot, "cannot inspect data root target: %v", err)
			report.finalize()
			return report
		}
	}
	if !rootInfo.IsDir() {
		report.incomplete("DATA_ROOT_NOT_DIRECTORY", "state", "", opts.DataRoot, "data root is not a directory")
		report.finalize()
		return report
	}

	rootEntries, err := os.ReadDir(opts.DataRoot)
	if err != nil {
		report.incomplete("DATA_ROOT_READ_FAILED", "state", "", opts.DataRoot, "cannot enumerate data root: %v", err)
		report.finalize()
		return report
	}
	scanManifestTemps(&report, opts.DataRoot, rootEntries)
	scanTransactions(&report, opts.DataRoot)

	manifestState := scanManifest(&report, report.ManifestPath)
	scanManifestBackup(&report, report.ManifestPath+".bak", manifestState.trusted)
	live := scanLiveEntries(&report, manifestState.entries)
	tools := scanStore(&report, opts, manifestState)
	crossCheckEntries(&report, manifestState.entries, live, tools)
	if opts.Deep {
		scanLiveTemps(&report, manifestState.entries)
	}

	report.finalize()
	return report
}

func scanManifest(report *Report, path string) manifestAudit {
	result := manifestAudit{byName: make(map[string]manifest.Entry)}
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			report.add(SeverityWarning, "MANIFEST_MISSING", "manifest", "", path, "data root exists but manifest.json is missing")
			return result
		}
		report.incomplete("MANIFEST_STAT_FAILED", "manifest", "", path, "cannot inspect manifest: %v", err)
		return result
	}
	result.available = true
	if info.Mode()&os.ModeSymlink != 0 {
		report.add(SeverityError, "MANIFEST_SYMLINK", "manifest", "", path, "manifest must be a regular file, not a symlink")
		return result
	}
	if !info.Mode().IsRegular() {
		report.add(SeverityError, "MANIFEST_NOT_REGULAR", "manifest", "", path, "manifest is not a regular file")
		return result
	}
	if info.Size() > maxManifestSize {
		report.add(SeverityError, "MANIFEST_TOO_LARGE", "manifest", "", path, "manifest is %d bytes; audit limit is %d bytes", info.Size(), maxManifestSize)
		return result
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		report.incomplete("MANIFEST_READ_FAILED", "manifest", "", path, "cannot read manifest: %v", err)
		return result
	}
	var m manifest.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		report.add(SeverityError, "MANIFEST_JSON_INVALID", "manifest", "", path, "cannot decode manifest: %v", err)
		return result
	}
	if m.SchemaVersion < 0 || m.SchemaVersion > manifest.CurrentSchemaVersion {
		report.add(SeverityError, "MANIFEST_SCHEMA_UNSUPPORTED", "manifest", "", path, "schema_version %d is unsupported", m.SchemaVersion)
		return result
	}
	if m.SchemaVersion == 0 {
		report.add(SeverityWarning, "MANIFEST_SCHEMA_IMPLICIT", "manifest", "", path, "schema_version 0 is accepted as legacy v1 but should be normalized on a future explicit repair")
	}

	result.parsed = true
	strict, strictErr := manifest.Decode(raw)
	if strictErr != nil {
		report.add(SeverityError, "MANIFEST_SEMANTIC_INVALID", "manifest", "", path, "%v", strictErr)
		result.entries = append([]manifest.Entry(nil), m.Entries...)
	} else {
		result.entries = append([]manifest.Entry(nil), strict.Entries...)
	}
	beforeErrors := findingCount(*report, SeverityError)
	validateManifestEntries(report, result.entries, result.byName)
	result.trusted = strictErr == nil && findingCount(*report, SeverityError) == beforeErrors
	return result
}

func scanManifestBackup(report *Report, path string, mainTrusted bool) {
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			report.incomplete("MANIFEST_BACKUP_STAT_FAILED", "manifest_backup", "", path, "cannot inspect manifest backup: %v", err)
		}
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		report.add(SeverityError, "MANIFEST_BACKUP_SYMLINK", "manifest_backup", "", path, "manifest backup must be a regular file, not a symlink")
		return
	}
	if !info.Mode().IsRegular() {
		report.add(SeverityError, "MANIFEST_BACKUP_NOT_REGULAR", "manifest_backup", "", path, "manifest backup is not a regular file")
		return
	}
	if info.Size() > maxManifestSize {
		report.add(SeverityWarning, "MANIFEST_BACKUP_TOO_LARGE", "manifest_backup", "", path, "manifest backup is %d bytes; audit limit is %d bytes", info.Size(), maxManifestSize)
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		report.incomplete("MANIFEST_BACKUP_READ_FAILED", "manifest_backup", "", path, "cannot read manifest backup: %v", err)
		return
	}
	var backupHeader manifest.Manifest
	if err := json.Unmarshal(raw, &backupHeader); err != nil {
		report.add(SeverityWarning, "MANIFEST_BACKUP_JSON_INVALID", "manifest_backup", "", path, "backup is not a usable recovery candidate: %v", err)
		return
	}
	if backupHeader.SchemaVersion < 0 || backupHeader.SchemaVersion > manifest.CurrentSchemaVersion {
		report.add(SeverityWarning, "MANIFEST_BACKUP_SCHEMA_UNSUPPORTED", "manifest_backup", "", path, "backup schema_version %d is unsupported", backupHeader.SchemaVersion)
		return
	}
	backup, err := manifest.Decode(raw)
	if err != nil {
		report.add(SeverityWarning, "MANIFEST_BACKUP_SEMANTIC_INVALID", "manifest_backup", "", path, "backup is not a usable recovery candidate: %v", err)
		return
	}
	temporary := newReport(Options{DataRoot: report.DataRoot})
	validateManifestEntries(&temporary, backup.Entries, make(map[string]manifest.Entry))
	if errorsFound := findingCount(temporary, SeverityError); errorsFound > 0 {
		report.add(SeverityWarning, "MANIFEST_BACKUP_SEMANTIC_INVALID", "manifest_backup", "", path, "backup has %d structural or required-field error(s)", errorsFound)
		return
	}
	if !mainTrusted {
		report.add(SeverityInfo, "MANIFEST_BACKUP_AVAILABLE", "manifest_backup", "", path, "current manifest is unavailable or invalid, but this backup passed read-only structural validation")
	}
}

func validateManifestEntries(report *Report, entries []manifest.Entry, byName map[string]manifest.Entry) {
	nameCount := make(map[string]int)
	pathCount := make(map[string]int)
	for _, entry := range entries {
		nameCount[entry.Name]++
		if entry.Path != "" {
			pathCount[filepath.Clean(entry.Path)]++
		}
	}

	for _, entry := range entries {
		subject := entry.Name
		if subject == "" {
			subject = "<empty>"
		}
		if err := store.ValidateName(entry.Name); err != nil {
			report.add(SeverityError, "MANIFEST_ENTRY_NAME_INVALID", "entry", subject, entry.Path, "%v", err)
		} else if _, exists := byName[entry.Name]; !exists {
			byName[entry.Name] = entry
		}
		if nameCount[entry.Name] > 1 {
			report.add(SeverityError, "MANIFEST_DUPLICATE_NAME", "entry", subject, entry.Path, "name appears %d times", nameCount[entry.Name])
		}

		cleanPath := filepath.Clean(entry.Path)
		switch {
		case entry.Path == "":
			report.add(SeverityError, "MANIFEST_ENTRY_PATH_MISSING", "entry", subject, "", "active path is empty")
		case !filepath.IsAbs(entry.Path):
			report.add(SeverityError, "MANIFEST_ENTRY_PATH_RELATIVE", "entry", subject, entry.Path, "active path must be absolute")
		default:
			if pathCount[cleanPath] > 1 {
				report.add(SeverityError, "MANIFEST_DUPLICATE_PATH", "entry", subject, entry.Path, "clean path appears %d times", pathCount[cleanPath])
			}
			if entry.Name != "" && filepath.Base(cleanPath) != entry.Name {
				report.add(SeverityWarning, "MANIFEST_PATH_NAME_MISMATCH", "entry", subject, entry.Path, "path basename %q does not match manifest name", filepath.Base(cleanPath))
			}
		}

		if entry.Tag == "original" {
			// original is valid only as the exact internal rollback tag.
		} else if err := store.ValidateTag(entry.Tag); err != nil {
			report.add(SeverityError, "MANIFEST_ENTRY_TAG_INVALID", "entry", subject, entry.Path, "%v", err)
		}
		if !validSHA256(entry.SHA256) {
			report.add(SeverityError, "MANIFEST_ENTRY_SHA256_INVALID", "entry", subject, entry.Path, "sha256 must contain exactly 64 hexadecimal characters")
		}
		if entry.Repo != "" && !validRepo(entry.Repo) {
			report.add(SeverityError, "MANIFEST_ENTRY_REPO_INVALID", "entry", subject, entry.Path, "repo must be owner/repo")
		}
		if !validRFC3339(entry.AdoptedAt) {
			report.add(SeverityError, "MANIFEST_ENTRY_ADOPTED_AT_INVALID", "entry", subject, entry.Path, "adopted_at must be RFC3339")
		}
		if !validRFC3339(entry.UpdatedAt) {
			report.add(SeverityError, "MANIFEST_ENTRY_UPDATED_AT_INVALID", "entry", subject, entry.Path, "updated_at must be RFC3339")
		}
		if entry.AssetSHA256 != "" && !validSHA256(entry.AssetSHA256) {
			report.add(SeverityError, "MANIFEST_ENTRY_ASSET_SHA256_INVALID", "entry", subject, entry.Path, "asset_sha256 must contain exactly 64 hexadecimal characters")
		}
		if (entry.AssetName == "") != (entry.AssetSHA256 == "") {
			report.add(SeverityError, "MANIFEST_ENTRY_ASSET_EVIDENCE_INCOMPLETE", "entry", subject, entry.Path, "asset_name and asset_sha256 must either both be present or both be absent")
		}
		if entry.ChecksumVerified && (entry.AssetName == "" || entry.AssetSHA256 == "" || entry.ChecksumAsset == "") {
			report.add(SeverityError, "MANIFEST_ENTRY_CHECKSUM_EVIDENCE_INCOMPLETE", "entry", subject, entry.Path, "checksum_verified requires asset_name, asset_sha256, and checksum_asset")
		}
		if entry.ChecksumAsset != "" && entry.AssetName == "" {
			report.add(SeverityWarning, "MANIFEST_ENTRY_CHECKSUM_WITHOUT_ASSET", "entry", subject, entry.Path, "checksum_asset is present without asset_name")
		}
	}
}

func scanLiveEntries(report *Report, entries []manifest.Entry) map[string]fileAudit {
	result := make(map[string]fileAudit)
	for i, entry := range entries {
		key := liveKey(i, entry)
		if entry.Path == "" || !filepath.IsAbs(entry.Path) {
			continue
		}
		audit := fileAudit{path: entry.Path}
		info, err := os.Lstat(entry.Path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				report.add(SeverityError, "LIVE_MISSING", "live", entry.Name, entry.Path, "registered active path does not exist")
			} else {
				report.add(SeverityError, "LIVE_STAT_FAILED", "live", entry.Name, entry.Path, "cannot inspect active path: %v", err)
			}
			result[key] = audit
			continue
		}
		audit.present = true
		if info.Mode()&os.ModeSymlink != 0 {
			report.add(SeverityInfo, "LIVE_LEGACY_SYMLINK", "live", entry.Name, entry.Path, "active path is a supported legacy symlink; doctor validated its target but did not rewrite it")
			info, err = os.Stat(entry.Path)
			if err != nil {
				report.add(SeverityError, "LIVE_SYMLINK_TARGET_INVALID", "live", entry.Name, entry.Path, "cannot inspect symlink target: %v", err)
				result[key] = audit
				continue
			}
		}
		if !info.Mode().IsRegular() {
			report.add(SeverityError, "LIVE_NOT_REGULAR", "live", entry.Name, entry.Path, "active path does not resolve to a regular file")
			result[key] = audit
			continue
		}
		audit.valid = true
		if info.Mode()&0o111 == 0 {
			report.add(SeverityError, "LIVE_NOT_EXECUTABLE", "live", entry.Name, entry.Path, "active file has no executable bit")
		}
		if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			report.add(SeverityWarning, "LIVE_SPECIAL_MODE", "live", entry.Name, entry.Path, "active file has special mode bits outside hukou's preservation contract")
		}
		sha, err := store.SHA256File(entry.Path)
		if err != nil {
			report.add(SeverityError, "LIVE_HASH_FAILED", "live", entry.Name, entry.Path, "cannot hash active file: %v", err)
			result[key] = audit
			continue
		}
		audit.sha256 = sha
		audit.hashOK = true
		if !strings.EqualFold(sha, entry.SHA256) {
			report.add(SeverityError, "LIVE_SHA256_MISMATCH", "live", entry.Name, entry.Path, "active sha256 is %s, manifest records %s", sha, entry.SHA256)
		}
		result[key] = audit
	}
	return result
}

func scanStore(report *Report, opts Options, state manifestAudit) map[string]toolAudit {
	result := make(map[string]toolAudit)
	path := filepath.Join(opts.DataRoot, "store")
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			if len(state.entries) > 0 {
				report.add(SeverityError, "STORE_ROOT_MISSING", "store", "", path, "manifest has entries but store root is missing")
			}
			return result
		}
		report.incomplete("STORE_ROOT_STAT_FAILED", "store", "", path, "cannot inspect store root: %v", err)
		return result
	}
	if info.Mode()&os.ModeSymlink != 0 {
		report.add(SeverityInfo, "STORE_ROOT_SYMLINK", "store", "", path, "store root is a configured trust-anchor symlink; internal child symlinks remain forbidden")
		info, err = os.Stat(path)
		if err != nil {
			report.incomplete("STORE_ROOT_TARGET_FAILED", "store", "", path, "cannot inspect store root target: %v", err)
			return result
		}
	}
	if !info.IsDir() {
		report.add(SeverityError, "STORE_ROOT_NOT_DIRECTORY", "store", "", path, "store root is not a directory")
		return result
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		report.incomplete("STORE_ROOT_READ_FAILED", "store", "", path, "cannot enumerate store root: %v", err)
		return result
	}

	seenTools := make(map[string]bool)
	for _, entry := range entries {
		name := entry.Name()
		entryPath := filepath.Join(path, name)
		if strings.EqualFold(name, ".tmp") {
			scanStoreTmp(report, entry, entryPath)
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report.add(SeverityError, "STORE_CHILD_SYMLINK", "store", name, entryPath, "store child must not be a symlink")
			continue
		}
		if !entry.IsDir() {
			report.add(SeverityError, "STORE_UNEXPECTED_ENTRY", "store", name, entryPath, "store root child is not a directory")
			continue
		}
		if err := store.ValidateName(name); err != nil {
			report.add(SeverityError, "STORE_TOOL_NAME_INVALID", "store", name, entryPath, "%v", err)
			continue
		}

		manifestEntry, referenced := state.byName[name]
		if !referenced {
			aliases := make([]string, 0)
			for registered := range state.byName {
				if strings.EqualFold(registered, name) {
					aliases = append(aliases, registered)
				}
			}
			sort.Strings(aliases)
			for _, registered := range aliases {
				report.add(SeverityError, "STORE_TOOL_CASE_ALIAS", "store", name, entryPath, "store spelling conflicts with manifest name %q", registered)
			}
			referenced = len(aliases) > 0
		}
		if !referenced {
			if state.trusted {
				report.add(SeverityWarning, "STORE_TOOL_ORPHAN", "store", name, entryPath, "tool directory is not referenced by the valid manifest; no repair was attempted")
			} else {
				report.add(SeverityWarning, "STORE_TOOL_UNCLASSIFIABLE", "store", name, entryPath, "manifest is unavailable or structurally invalid, so this directory cannot be classified as orphaned")
			}
		}

		currentTag := ""
		if referenced && manifestEntry.Name != "" {
			currentTag = manifestEntry.Tag
			seenTools[manifestEntry.Name] = true
		}
		result[name] = scanToolDir(report, opts.Deep, name, entryPath, currentTag, manifestEntry.AdoptedSHA256)
	}

	if state.parsed {
		for name, entry := range state.byName {
			if err := store.ValidateName(name); err != nil {
				continue
			}
			if !seenTools[name] {
				report.add(SeverityError, "STORE_TOOL_MISSING", "store", name, filepath.Join(path, name), "manifest entry has no exact-spelling store directory (active tag %q)", entry.Tag)
			}
		}
	}
	return result
}

func scanStoreTmp(report *Report, entry os.DirEntry, path string) {
	if entry.Name() != ".tmp" {
		report.add(SeverityError, "STORE_TMP_CASE_ALIAS", "store", entry.Name(), path, "reserved .tmp directory has non-canonical spelling")
		return
	}
	if entry.Type()&os.ModeSymlink != 0 {
		report.add(SeverityError, "STORE_TMP_SYMLINK", "store", ".tmp", path, "reserved .tmp path must not be a symlink")
		return
	}
	if !entry.IsDir() {
		report.add(SeverityError, "STORE_TMP_NOT_DIRECTORY", "store", ".tmp", path, "reserved .tmp path is not a directory")
		return
	}
	children, err := os.ReadDir(path)
	if err != nil {
		report.incomplete("STORE_TMP_READ_FAILED", "store", ".tmp", path, "cannot enumerate store staging directory: %v", err)
		return
	}
	if len(children) > 0 {
		report.add(SeverityWarning, "STORE_TMP_NOT_EMPTY", "store", ".tmp", path, "staging directory contains %d entrie(s); doctor is read-only and did not remove them", len(children))
	}
}

func scanToolDir(report *Report, deep bool, name, path, currentTag, adoptedSHA256 string) toolAudit {
	result := toolAudit{versions: make(map[string]fileAudit)}
	entries, err := os.ReadDir(path)
	if err != nil {
		report.incomplete("STORE_TOOL_READ_FAILED", "store", name, path, "cannot enumerate tool directory: %v", err)
		return result
	}
	foundOriginal := false
	for _, entry := range entries {
		childName := entry.Name()
		childPath := filepath.Join(path, childName)
		if strings.EqualFold(childName, "original") {
			if childName != "original" {
				report.add(SeverityError, "STORE_ORIGINAL_CASE_ALIAS", "store", name, childPath, "reserved original directory has non-canonical spelling")
				continue
			}
			foundOriginal = true
			result.original = scanArtifactDir(report, name, "original", childPath, entry, true)
			if deep && result.original.hashOK {
				report.add(SeverityInfo, "STORE_ORIGINAL_SHA256", "store", name, result.original.path, "original sha256 is %s", result.original.sha256)
			}
			// Adoption anchor (B5): the immutable original backup must hash to
			// the adopt-time anchor recorded in the manifest. A mismatch means
			// the store's original was tampered with after adoption — an error,
			// never a silent repair. Legacy entries without an anchor are
			// skipped (there is nothing to compare against).
			if adoptedSHA256 != "" && result.original.hashOK && !strings.EqualFold(result.original.sha256, adoptedSHA256) {
				report.add(SeverityError, "ADOPT_ANCHOR_MISMATCH", "store", name, result.original.path, "original backup does not match the adoption anchor (possible store tampering): got %s want %s", result.original.sha256, adoptedSHA256)
			}
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 {
			report.add(SeverityError, "STORE_VERSION_SYMLINK", "store", name+"@"+childName, childPath, "version path must not be a symlink")
			continue
		}
		if !entry.IsDir() {
			report.add(SeverityError, "STORE_VERSION_NOT_DIRECTORY", "store", name+"@"+childName, childPath, "tool child is not a version directory")
			continue
		}
		if err := store.ValidateTag(childName); err != nil {
			report.add(SeverityError, "STORE_VERSION_TAG_INVALID", "store", name+"@"+childName, childPath, "%v", err)
			continue
		}
		shouldHash := deep || childName == currentTag
		artifact := scanArtifactDir(report, name, childName, childPath, entry, shouldHash)
		result.versions[childName] = artifact
		if deep && childName != currentTag && artifact.hashOK {
			report.add(SeverityInfo, "STORE_RETAINED_SHA256", "store", name+"@"+childName, artifact.path, "retained version sha256 is %s", artifact.sha256)
		}
	}
	if !foundOriginal {
		report.add(SeverityError, "STORE_ORIGINAL_MISSING", "store", name, filepath.Join(path, "original"), "tool directory has no immutable original backup")
	}
	return result
}

func scanArtifactDir(report *Report, tool, tag, path string, entry os.DirEntry, hash bool) fileAudit {
	result := fileAudit{}
	if entry.Type()&os.ModeSymlink != 0 {
		report.add(SeverityError, "STORE_ARTIFACT_SYMLINK", "store", tool+"@"+tag, path, "artifact directory must not be a symlink")
		return result
	}
	if !entry.IsDir() {
		report.add(SeverityError, "STORE_ARTIFACT_NOT_DIRECTORY", "store", tool+"@"+tag, path, "artifact path is not a directory")
		return result
	}
	children, err := os.ReadDir(path)
	if err != nil {
		report.incomplete("STORE_ARTIFACT_READ_FAILED", "store", tool+"@"+tag, path, "cannot enumerate artifact directory: %v", err)
		return result
	}
	if len(children) != 1 {
		report.add(SeverityError, "STORE_ARTIFACT_LAYOUT_INVALID", "store", tool+"@"+tag, path, "artifact directory must contain exactly one regular file; found %d entries", len(children))
		return result
	}
	child := children[0]
	binaryPath := filepath.Join(path, child.Name())
	result.path = binaryPath
	result.present = true
	if child.Type()&os.ModeSymlink != 0 || !child.Type().IsRegular() {
		report.add(SeverityError, "STORE_ARTIFACT_NOT_REGULAR", "store", tool+"@"+tag, binaryPath, "artifact is not a regular file")
		return result
	}
	if child.Name() != tool {
		report.add(SeverityError, "STORE_ARTIFACT_NAME_MISMATCH", "store", tool+"@"+tag, binaryPath, "artifact basename %q does not match tool name", child.Name())
	}
	info, err := child.Info()
	if err != nil {
		report.add(SeverityError, "STORE_ARTIFACT_STAT_FAILED", "store", tool+"@"+tag, binaryPath, "cannot inspect artifact: %v", err)
		return result
	}
	if !info.Mode().IsRegular() {
		report.add(SeverityError, "STORE_ARTIFACT_NOT_REGULAR", "store", tool+"@"+tag, binaryPath, "artifact is not a regular file")
		return result
	}
	result.valid = child.Name() == tool
	if info.Mode()&0o111 == 0 {
		report.add(SeverityError, "STORE_ARTIFACT_NOT_EXECUTABLE", "store", tool+"@"+tag, binaryPath, "artifact has no executable bit")
		result.valid = false
	}
	if hash {
		sha, err := store.SHA256File(binaryPath)
		if err != nil {
			report.add(SeverityError, "STORE_ARTIFACT_HASH_FAILED", "store", tool+"@"+tag, binaryPath, "cannot hash artifact: %v", err)
			return result
		}
		result.sha256 = sha
		result.hashOK = true
	}
	return result
}

func crossCheckEntries(report *Report, entries []manifest.Entry, live map[string]fileAudit, tools map[string]toolAudit) {
	for i, entry := range entries {
		if err := store.ValidateName(entry.Name); err != nil {
			continue
		}
		tool, ok := tools[entry.Name]
		if !ok {
			continue
		}
		liveState := live[liveKey(i, entry)]
		if entry.Tag == "original" {
			compareActiveArtifact(report, entry, liveState, tool.original, "original")
			continue
		}
		if artifact, exists := tool.versions[entry.Tag]; exists {
			compareActiveArtifact(report, entry, liveState, artifact, entry.Tag)
			continue
		}
		if liveState.hashOK && tool.original.hashOK && strings.EqualFold(liveState.sha256, tool.original.sha256) {
			report.add(SeverityInfo, "ADOPT_BASELINE_NOT_MATERIALIZED", "store", entry.Name, tool.original.path, "active tag %q has no version directory, but live bytes match the immutable original backup", entry.Tag)
			continue
		}
		report.add(SeverityError, "STORE_ACTIVE_VERSION_MISSING", "store", entry.Name+"@"+entry.Tag, filepath.Join(filepath.Dir(filepath.Dir(tool.original.path)), entry.Tag), "manifest active tag has no matching version directory and cannot be proven to be the adopt baseline")
	}
}

func compareActiveArtifact(report *Report, entry manifest.Entry, live, artifact fileAudit, tag string) {
	if !artifact.present || !artifact.valid || !artifact.hashOK {
		return
	}
	if validSHA256(entry.SHA256) && !strings.EqualFold(artifact.sha256, entry.SHA256) {
		report.add(SeverityError, "STORE_ACTIVE_SHA256_MISMATCH", "store", entry.Name+"@"+tag, artifact.path, "store artifact sha256 is %s, manifest records %s", artifact.sha256, entry.SHA256)
	}
	if live.hashOK && !strings.EqualFold(artifact.sha256, live.sha256) {
		report.add(SeverityError, "STORE_LIVE_SHA256_MISMATCH", "store", entry.Name+"@"+tag, artifact.path, "store artifact sha256 %s does not match live sha256 %s", artifact.sha256, live.sha256)
	}
}

func scanManifestTemps(report *Report, root string, entries []os.DirEntry) {
	for _, entry := range entries {
		name := entry.Name()
		if isManifestTemp(name) {
			report.add(SeverityWarning, "MANIFEST_TEMP_PRESENT", "manifest", name, filepath.Join(root, name), "manifest staging file remains; doctor did not remove or restore it")
		}
	}
}

func isManifestTemp(name string) bool {
	if !strings.HasSuffix(name, ".tmp") {
		return false
	}
	return strings.HasPrefix(name, "manifest-") ||
		strings.HasPrefix(name, ".manifest.json-") ||
		strings.HasPrefix(name, ".manifest.json.bak-")
}

func scanTransactions(report *Report, root string) {
	path := filepath.Join(root, "transactions")
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			report.incomplete("TRANSACTION_DIR_STAT_FAILED", "transaction", "", path, "cannot inspect transaction directory: %v", err)
		}
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		report.add(SeverityError, "TRANSACTION_DIR_SYMLINK", "transaction", "", path, "transaction directory must not be a symlink")
		return
	}
	if !info.IsDir() {
		report.add(SeverityError, "TRANSACTION_DIR_NOT_DIRECTORY", "transaction", "", path, "transaction path is not a directory")
		return
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		report.incomplete("TRANSACTION_DIR_READ_FAILED", "transaction", "", path, "cannot enumerate transaction directory: %v", err)
		return
	}
	pending := 0
	for _, entry := range entries {
		entryPath := filepath.Join(path, entry.Name())
		if strings.HasPrefix(entry.Name(), statejournal.QuarantinedPrefix()) {
			if statejournal.IsValidQuarantineContainer(path, entry.Name()) {
				report.add(SeverityWarning, "TRANSACTION_QUARANTINED_PRESENT", "transaction", entry.Name(), entryPath, "quarantined transaction entry is retained for diagnosis; inspect it, then remove it manually when you are satisfied it is no longer needed")
			} else {
				report.add(SeverityError, "TRANSACTION_QUARANTINED_INVALID", "transaction", entry.Name(), entryPath, "quarantined-like entry failed layout validation; inspect it manually before recovery can proceed")
			}
			continue
		}
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			report.add(SeverityError, "TRANSACTION_ENTRY_INVALID", "transaction", entry.Name(), entryPath, "transaction entry must be a real directory")
			if strings.HasPrefix(entry.Name(), "pending-") {
				pending++
			}
			continue
		}
		switch {
		case strings.HasPrefix(entry.Name(), "pending-"):
			pending++
			intent := filepath.Join(entryPath, "intent.json")
			commit := filepath.Join(entryPath, "COMMIT")
			intentOK := regularFile(intent)
			commitOK := regularFile(commit)
			report.add(SeverityError, "TRANSACTION_PENDING", "transaction", entry.Name(), entryPath, "pending transaction detected (intent=%t, commit_marker=%t); doctor does not recover it", intentOK, commitOK)
		case strings.HasPrefix(entry.Name(), ".building-"):
			report.add(SeverityWarning, "TRANSACTION_BUILDING_PRESENT", "transaction", entry.Name(), entryPath, "incomplete transaction build directory is present; doctor did not remove it")
		case strings.HasPrefix(entry.Name(), "completed-"):
			report.add(SeverityWarning, "TRANSACTION_COMPLETED_PRESENT", "transaction", entry.Name(), entryPath, "completed transaction record awaits cleanup by a locked mutating command")
		default:
			report.add(SeverityError, "TRANSACTION_ENTRY_UNKNOWN", "transaction", entry.Name(), entryPath, "unrecognized transaction directory entry; inspect it manually or upgrade hukou before recovery can proceed")
		}
	}
	if pending > 1 {
		report.add(SeverityError, "TRANSACTION_MULTIPLE_PENDING", "transaction", "", path, "%d pending transactions require explicit recovery ordering", pending)
	}
}

func scanLiveTemps(report *Report, entries []manifest.Entry) {
	parents := make(map[string]struct{})
	for _, entry := range entries {
		if entry.Path == "" || !filepath.IsAbs(entry.Path) {
			continue
		}
		parents[filepath.Dir(filepath.Clean(entry.Path))] = struct{}{}
	}
	dirs := make([]string, 0, len(parents))
	for dir := range parents {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			report.incomplete("LIVE_PARENT_READ_FAILED", "live", "", dir, "deep audit cannot enumerate registered live parent: %v", err)
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			switch {
			case strings.HasPrefix(entry.Name(), ".hukou-tmp-"):
				report.add(SeverityWarning, "LIVE_TEMP_PRESENT", "live", entry.Name(), path, "activation temporary name is present; without a bound transaction doctor will not remove it")
			case strings.HasPrefix(entry.Name(), ".hukou-rollback-"):
				report.add(SeverityWarning, "LIVE_ROLLBACK_SNAPSHOT_PRESENT", "live", entry.Name(), path, "rollback snapshot is present and may be recovery evidence; doctor will not remove it")
			case strings.HasPrefix(entry.Name(), ".hukou-txn-"):
				report.add(SeverityWarning, "LIVE_TRANSACTION_TEMP_PRESENT", "live", entry.Name(), path, "transaction recovery temporary name is present; doctor is read-only and will not remove it. If no transaction is active, delete it manually")
			}
		}
	}
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func validRepo(value string) bool {
	parts := strings.Split(value, "/")
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" && strings.TrimSpace(value) == value
}

func validRFC3339(value string) bool {
	if value == "" {
		return false
	}
	_, err := time.Parse(time.RFC3339, value)
	return err == nil
}

func liveKey(index int, entry manifest.Entry) string {
	return fmt.Sprintf("%d\x00%s\x00%s", index, entry.Name, entry.Path)
}

func findingCount(report Report, severity Severity) int {
	count := 0
	for _, finding := range report.Findings {
		if finding.Severity == severity {
			count++
		}
	}
	return count
}
