package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/archive"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/updatecheck"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/spf13/cobra"
)

var (
	upgradeAll             bool
	upgradeDryRun          bool
	upgradeAsset           string
	upgradeAllowUnverified bool
)

// upgradeTestHookAfterStoreNewVersion, when non-nil, runs immediately after
// the new release binary has been committed to the immutable store and before
// any subsequent validation or transaction capture. It exists exclusively so
// adversarial tests can inject deterministically inside that window (for
// example, tampering with the just-stored artifact) while driving the real
// upgrade flow end to end. Production code never sets it.
var upgradeTestHookAfterStoreNewVersion func(name, tag string)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade [name ...]",
	Short: "Upgrade adopted tools from GitHub releases",
	Long: `Check adopted tools against their configured GitHub release policy,
download assets, verify publisher SHA-256 checksums (fail-closed when a
release has no checksum asset unless --allow-unverified is set), and replace
the live executable atomically. --dry-run only reports; --all checks every
entry; local entries are skipped.`,
	Args: cobra.ArbitraryArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeAll, "all", false, "upgrade all hukou-adopted tools")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "report without replacing executables")
	upgradeCmd.Flags().StringVar(&upgradeAsset, "asset", "", "filter asset names by substring (prefix with ^ to exclude)")
	upgradeCmd.Flags().BoolVar(&upgradeAllowUnverified, "allow-unverified", false, "allow install when the release publishes no checksum asset (fail-closed by default)")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	token := firstEnv("GITHUB_TOKEN", "GH_TOKEN")
	client := ghrelease.New(token)
	// Surface advisory download retries (system-proxy fallback) on stderr.
	client.Log = func(msg string) {
		fmt.Fprintf(cmd.ErrOrStderr(), "%s\n", msg)
	}
	return doUpgrade(cmd.OutOrStdout(), cmd.ErrOrStderr(), args, upgradeAll, upgradeDryRun, upgradeAsset, client, upgradeAllowUnverified)
}

func doUpgrade(stdout, stderr io.Writer, names []string, all, dryRun bool, assetFilter string, client *ghrelease.Client, allowUnverified bool) error {
	return doUpgradeWithSave(stdout, stderr, names, all, dryRun, assetFilter, client, allowUnverified, saveManifest)
}

func doUpgradeWithSave(stdout, stderr io.Writer, names []string, all, dryRun bool, assetFilter string, client *ghrelease.Client, allowUnverified bool, save func(*manifest.Manifest) error) error {
	if all && len(names) > 0 {
		return fail(i18n.Errorf("tool names and --all cannot be used together"))
	}
	if !dryRun {
		lock, err := acquireMutationLock(stderr)
		if err != nil {
			return fail(i18n.Wrapf("acquire state lock: %w", err))
		}
		defer releaseMutationLock(lock, stderr)
	} else if err := ensureDryRunTransactionClean(); err != nil {
		return fail(err)
	}

	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	if len(m.Entries) == 0 {
		fmt.Fprintln(stdout, i18n.T("No adopted tools"))
		return nil
	}

	var targets []manifest.Entry
	switch {
	case all:
		targets = append([]manifest.Entry(nil), m.Entries...)
	case len(names) > 0:
		for _, n := range names {
			e := m.Get(n)
			if e == nil {
				return fail(i18n.Errorf("adopted tool %q not found", n))
			}
			targets = append(targets, *e)
		}
	default:
		return fail(i18n.Errorf("specify at least one tool name or use --all"))
	}

	s := newStore()
	if !dryRun {
		if err := s.GC(); err != nil {
			return fail(err)
		}
	}

	// Deduplicate targets by name so a repeated argument cannot make a later
	// iteration operate on a snapshot this batch already upgraded.
	// (--all targets come from a validated manifest and are already unique.)
	seen := make(map[string]struct{}, len(targets))
	unique := targets[:0]
	for _, e := range targets {
		if _, dup := seen[e.Name]; dup {
			continue
		}
		seen[e.Name] = struct{}{}
		unique = append(unique, e)
	}
	targets = unique

	var failures []error
	for _, e := range targets {
		// Each snapshot entry is authoritative for its own upgrade: upgradeOne
		// mutates only the entry it was given (and that entry's manifest
		// record), so no earlier iteration can invalidate this copy. Holding
		// the copy directly avoids one m.Get linear scan per target inside the
		// batch loop; a failure that leaves unclean transaction state still
		// stops the batch below.
		entry := e
		if err := upgradeOne(stdout, stderr, s, client, m, &entry, dryRun, assetFilter, allowUnverified, save); err != nil {
			fmt.Fprintf(stderr, "%s\n", i18n.T("Warning: failed to upgrade %s: %v", entry.Name, err))
			failure := i18n.Wrapf("%s: %w", err, entry.Name)
			if !dryRun {
				if cleanErr := statejournal.CheckClean(dataRoot()); cleanErr != nil {
					stopErr := i18n.Wrapf("state remains unresolved; stop the upgrade batch before further network or store activity: %w", cleanErr)
					fmt.Fprintf(stderr, "%s\n", i18n.T("Warning: %v", stopErr))
					failure = errors.Join(failure, stopErr)
					failures = append(failures, failure)
					break
				}
			}
			failures = append(failures, failure)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(stderr, "%s\n", i18n.T("%d upgrade(s) failed:", len(failures)))
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %v\n", f)
		}
		return i18n.Wrapf("%d upgrade(s) failed: %w", errors.Join(failures...), len(failures))
	}
	return nil
}

func upgradeOne(stdout, stderr io.Writer, s *store.Store, client *ghrelease.Client, m *manifest.Manifest, e *manifest.Entry, dryRun bool, assetFilter string, allowUnverified bool, save func(*manifest.Manifest) error) error {
	checked, err := updatecheck.New(client).Check(*e, assetFilter)
	if err != nil {
		return err
	}
	if checked.Status == updatecheck.StatusLocal {
		fmt.Fprintf(stdout, "%s\n", i18n.T("Skipped %s: local entry", e.Name))
		return nil
	}
	if checked.Status == updatecheck.StatusCurrent {
		fmt.Fprintf(stdout, "%s\n", i18n.T("%s is up to date (%s)", e.Name, e.Tag))
		return nil
	}
	if checked.Status != updatecheck.StatusOutdated {
		return i18n.Errorf("cannot upgrade %s: update status %s", e.Name, checked.Status)
	}
	release := checked.Release
	chosen := checked.Asset
	note := checked.Note

	if dryRun {
		fmt.Fprintf(stdout, "%s", i18n.T("Would upgrade %s: %s -> %s using asset %s", e.Name, e.Tag, checked.LatestTag, chosen))
		if note != "" {
			fmt.Fprintf(stdout, " (%s)", note)
		}
		if findChecksumAsset(chosen, release.Assets) == "" {
			if allowUnverified {
				fmt.Fprintf(stdout, "%s", i18n.T(" [UNVERIFIED: no publisher checksum; --allow-unverified]"))
			} else {
				fmt.Fprintln(stdout)
				return i18n.Errorf("%s: release has no publisher checksum asset; refuse to install without verification (pass --allow-unverified to override)", e.Name)
			}
		}
		fmt.Fprintln(stdout)
		writeReleaseNotes(stdout, release)
		return nil
	}

	writeReleaseNotes(stdout, release)

	var assetURL string
	var assetSize int64
	for _, a := range release.Assets {
		if a.Name == chosen {
			assetURL = a.BrowserDownloadURL
			assetSize = a.Size
			break
		}
	}
	if assetURL == "" {
		return i18n.Errorf("asset %s not found in release", chosen)
	}

	tmpDir := filepath.Join(s.Root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	assetPath, err := client.Download(assetURL, tmpDir, assetSize)
	if err != nil {
		return i18n.Wrapf("download asset: %w", err)
	}
	// Closure captures the final path after any rename that preserves extension.
	finalAssetPath := assetPath
	defer func() { _ = os.Remove(finalAssetPath) }()

	// ghrelease.Download writes to a temp file without the original extension.
	// archive.Extract decides the format by extension, so preserve it.
	if ext := archiveExt(chosen); ext != "" && !strings.HasSuffix(strings.ToLower(filepath.Base(finalAssetPath)), ext) {
		newPath := finalAssetPath + ext
		if err := os.Rename(finalAssetPath, newPath); err != nil {
			return i18n.Wrapf("preserve extension: %w", err)
		}
		finalAssetPath = newPath
	}

	checksumAsset, checksums, err := downloadChecksums(client, release.Assets, chosen, tmpDir)
	if err != nil {
		return err
	}

	// Hash the downloaded asset exactly once. The same digest both records the
	// asset SHA-256 in the manifest and drives the publisher-checksum comparison
	// below, so the cross-boundary verification reuses it rather than re-reading
	// the whole asset a second time.
	assetSHA, err := store.SHA256File(finalAssetPath)
	if err != nil {
		return i18n.Wrapf("sha256 downloaded asset: %w", err)
	}

	// Fail closed: a publisher checksum must match when present; when absent,
	// refuse unless the operator explicitly opted into --allow-unverified.
	// There is no silent-pass path when tools or data are missing.
	checksumVerified, err := resolvePublisherChecksum(assetSHA, chosen, checksumAsset, checksums, allowUnverified)
	if err != nil {
		return err
	}
	if checksumVerified {
		fmt.Fprintf(stderr, "%s\n", i18n.T("checksum ok: %s sha256=%s (from %s)", chosen, assetSHA, checksumAsset))
	} else {
		// allow-unverified path only — resolvePublisherChecksum returns an error
		// for every other unverified case.
		fmt.Fprintf(stderr, "%s\n", i18n.T("WARNING: UNVERIFIED install of %s asset %s (sha256=%s); release published no checksum asset; --allow-unverified was set", e.Name, chosen, assetSHA))
		fmt.Fprintf(stdout, "%s\n", i18n.T("UNVERIFIED: %s installed without publisher checksum verification (asset sha256=%s)", e.Name, assetSHA))
	}

	extractDir, err := os.MkdirTemp(tmpDir, "extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)

	extractedPath, err := archive.Extract(finalAssetPath, extractDir, wantArchiveExe(e))
	if err != nil {
		return i18n.Wrapf("extract: %w", err)
	}
	// The store version directory must hold the artifact under the TOOL name,
	// not the archive-internal name (which --exe may differ from). Rename the
	// extracted member inside the scratch dir before ingestion so doctor's
	// store checks and rollback lookups stay name-consistent.
	storePath := filepath.Join(filepath.Dir(extractedPath), e.Name)
	if err := os.Rename(extractedPath, storePath); err != nil {
		return i18n.Wrapf("rename extracted binary to tool name: %w", err)
	}
	extractedPath = storePath
	extractedInfo, err := os.Stat(extractedPath)
	if err != nil {
		return i18n.Wrapf("stat extracted binary: %w", err)
	}
	if !extractedInfo.Mode().IsRegular() || extractedInfo.Mode()&0o111 == 0 {
		return i18n.Errorf("extracted asset is not an executable regular file: %s", extractedPath)
	}
	if archive.DetectFormat(chosen) == archive.FormatBare {
		kind, err := scan.DetectKind(extractedPath)
		if err != nil {
			return i18n.Wrapf("inspect bare executable: %w", err)
		}
		if kind == scan.KindOther {
			return i18n.Errorf("selected bare asset is not a recognized executable or shebang script: %s", chosen)
		}
	}

	newVersionSHA, err := s.PutWithDigest(e.Name, release.TagName, extractedPath)
	if err != nil {
		return i18n.Wrapf("store: %w", err)
	}
	if upgradeTestHookAfterStoreNewVersion != nil {
		upgradeTestHookAfterStoreNewVersion(e.Name, release.TagName)
	}

	oldTag := e.Tag
	oldEntry := manifest.PrepareEntry(*e)
	latestSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return i18n.Wrapf("re-read current file before activation: %w", err)
	}
	if latestSHA != e.SHA256 {
		return i18n.Errorf("current file changed during the upgrade; refusing overwrite")
	}
	pathInfo, err := os.Lstat(e.Path)
	if err != nil {
		return i18n.Wrapf("lstat %s: %w", err, e.Path)
	}
	currentTargetInfo, err := os.Stat(e.Path)
	if err != nil {
		return i18n.Wrapf("stat current executable target: %w", err)
	}
	if e.Tag != "original" {
		// Every logical parent must have a content-addressed store source before
		// the new activation is published. Put follows a live symlink to its
		// regular target, so legacy symlink entries remain rollback-capable.
		if err := s.Put(e.Name, e.Tag, e.Path); err != nil {
			return i18n.Wrapf("backup current version: %w", err)
		}
	}

	originalSource := e.Path
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		originalSource, err = filepath.EvalSymlinks(e.Path)
		if err != nil {
			return i18n.Wrapf("resolve current executable symlink: %w", err)
		}
	}
	originalPath, err := s.PrepareOriginalPath(e.Name, e.Name)
	if err != nil {
		return i18n.Wrapf("prepare original path: %w", err)
	}
	needsOriginal := false
	if _, err := os.Lstat(originalPath); errors.Is(err, os.ErrNotExist) {
		needsOriginal = true
	} else if err != nil {
		return i18n.Wrapf("inspect original backup: %w", err)
	}
	originalEntries, err := os.ReadDir(filepath.Dir(originalPath))
	if err != nil {
		return i18n.Wrapf("inspect original backup namespace: %w", err)
	}
	if needsOriginal {
		if len(originalEntries) != 0 {
			return i18n.Errorf("original backup namespace is not empty")
		}
	} else if len(originalEntries) != 1 || originalEntries[0].Name() != filepath.Base(originalPath) || !originalEntries[0].Type().IsRegular() {
		return i18n.Errorf("original backup namespace has unexpected contents")
	}

	targetSource, err := s.ActivationSource(e.Name, release.TagName)
	if err != nil {
		return i18n.Wrapf("resolve activation source: %w", err)
	}
	// The store just copied the extracted binary into targetSource and returned
	// its content digest, cross-checked against a fresh read of the source. That
	// digest is exactly the store artifact's SHA-256, so reuse it instead of
	// hashing the immutable store file again. The transaction journal still
	// captures targetSource independently below (captureRegular), and
	// validateTransactionStateSHA re-checks that capture against targetSHA, so the
	// activation remains fully verified.
	targetSHA := newVersionSHA
	newEntry := manifest.PrepareEntry(oldEntry)
	eventID, err := activation.NewID()
	if err != nil {
		return i18n.Wrapf("prepare upgrade activation: %w", err)
	}
	if err := activation.RecordUpgrade(&newEntry, eventID, release.TagName, targetSHA, rfc3339Now()); err != nil {
		return i18n.Wrapf("prepare upgrade activation: %w", err)
	}
	newEntry.AssetName = chosen
	newEntry.AssetSHA256 = assetSHA
	newEntry.ChecksumAsset = checksumAsset
	newEntry.ChecksumVerified = checksumVerified
	afterManifest := cloneManifest(m)
	afterManifest.Put(newEntry)
	manifestBytes, err := encodeManifest(afterManifest)
	if err != nil {
		return i18n.Wrapf("encode target manifest: %w", err)
	}
	specs := make([]statejournal.Spec, 0, 3)
	if needsOriginal {
		specs = append(specs, statejournal.Spec{Role: "original", Path: originalPath, After: statejournal.RegularFile(originalSource)})
	}
	specs = append(specs,
		statejournal.Spec{Role: "live", Path: e.Path, After: statejournal.RegularFile(targetSource)},
		statejournal.Spec{Role: "manifest", Path: manifestPath(), After: statejournal.RegularBytes(manifestBytes, 0o600)},
	)
	tx, err := statejournal.Begin(dataRoot(), "upgrade", e.Name, specs)
	if err != nil {
		return i18n.Wrapf("prepare upgrade transaction: %w", err)
	}
	if err := validateTransactionStateSHA(tx, "live", oldEntry.SHA256, false); err != nil {
		return abortStateTransaction(tx, i18n.Wrapf("current file changed while preparing the transaction; refusing overwrite: %w", err))
	}
	beforeLive, _ := tx.Before("live")
	if pathInfo.Mode()&os.ModeSymlink == 0 && beforeLive.Mode != uint32(pathInfo.Mode().Perm()) {
		return abortStateTransaction(tx, i18n.Errorf("current file permissions changed while preparing the transaction; refusing overwrite"))
	}
	if err := validateTransactionStateSHA(tx, "live", targetSHA, true); err != nil {
		return abortStateTransaction(tx, err)
	}
	if needsOriginal {
		before, _ := tx.Before("original")
		if before.Kind != statejournal.KindAbsent {
			return abortStateTransaction(tx, i18n.Errorf("original backup appeared concurrently: %s", originalPath))
		}
		if err := validateTransactionStateSHA(tx, "original", oldEntry.SHA256, true); err != nil {
			return abortStateTransaction(tx, err)
		}
		afterOriginal, _ := tx.After("original")
		if afterOriginal.Mode != uint32(currentTargetInfo.Mode().Perm()) {
			return abortStateTransaction(tx, i18n.Errorf("original snapshot mode differs from live transaction state"))
		}
		if err := tx.Apply("original"); err != nil {
			return abortStateTransaction(tx, i18n.Wrapf("adopt original: %w", err))
		}
		originalEntries, entriesErr := os.ReadDir(filepath.Dir(originalPath))
		if entriesErr != nil || len(originalEntries) != 1 || originalEntries[0].Name() != filepath.Base(originalPath) || !originalEntries[0].Type().IsRegular() {
			return abortStateTransaction(tx, i18n.Errorf("original backup namespace changed during upgrade: %v", entriesErr))
		}
	}
	stressPause("pre-activate")
	if err := tx.Apply("live"); err != nil {
		return abortStateTransaction(tx, i18n.Wrapf("current file changed before activation or the target could not be activated; refusing overwrite: %w", err))
	}
	if err := tx.Verify("manifest", false); err != nil {
		return abortStateTransaction(tx, i18n.Wrapf("manifest changed during upgrade; refusing overwrite: %w", err))
	}
	*e = newEntry
	m.Put(newEntry)
	if err := save(m); err != nil {
		*e = oldEntry
		m.Put(oldEntry)
		return abortStateTransaction(tx, i18n.Wrapf("save manifest: %w", err))
	}
	if err := commitStateTransaction(tx, stderr); err != nil {
		refreshErr := refreshManifest(m)
		if current := m.Get(e.Name); current != nil {
			*e = *current
		}
		return errors.Join(i18n.Wrapf("commit upgrade transaction: %w", err), refreshErr)
	}
	finalizeStateTransaction(tx, stderr, e.Name, "upgrade")

	if err := pruneActivationHistory(s, m, *e); err != nil {
		fmt.Fprintf(stderr, "%s\n", i18n.T("Warning: failed to prune old versions for %s: %v", e.Name, err))
	}

	if checksumVerified {
		fmt.Fprintf(stdout, "%s\n", i18n.T("Upgraded %s: %s -> %s (checksum verified)", e.Name, oldTag, release.TagName))
	} else {
		fmt.Fprintf(stdout, "%s\n", i18n.T("Upgraded %s: %s -> %s (UNVERIFIED; no publisher checksum)", e.Name, oldTag, release.TagName))
	}
	return nil
}

// resolvePublisherChecksum enforces the fail-closed publisher-checksum policy
// for a downloaded release asset.
//
//   - When a checksum asset is present, the selected asset must have a valid
//     matching entry. Mismatch, missing entry, or invalid digest all fail.
//     --allow-unverified does not bypass a present-but-unusable checksum.
//   - When no checksum asset is present, installation is refused unless
//     allowUnverified is true. There is no silent pass path.
//
// On success, verified is true only when a publisher digest was compared and
// matched. Callers must record assetSHA / checksumAsset / verified into the
// manifest audit fields (AssetSHA256, ChecksumAsset, ChecksumVerified).
func resolvePublisherChecksum(assetSHA, assetName, checksumAsset string, checksums map[string]string, allowUnverified bool) (verified bool, err error) {
	if checksumAsset != "" {
		if err := verify.VerifyAssetDigest(assetSHA, assetName, checksums); err != nil {
			return false, i18n.Wrapf("verify checksum from %s: %w", err, checksumAsset)
		}
		return true, nil
	}
	if allowUnverified {
		return false, nil
	}
	return false, i18n.Errorf("release has no publisher checksum asset for %s; refuse to install without verification (pass --allow-unverified to override)", assetName)
}

func pruneActivationHistory(s *store.Store, m *manifest.Manifest, entry manifest.Entry) error {
	// A journal that could still reference a retained artifact takes priority
	// over garbage collection. This also covers the edge where COMMIT succeeded
	// but final journal cleanup failed: zero versions are deleted until recovery
	// and cleanup have completed under a later mutation lock.
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return i18n.Wrapf("skip pruning while transaction state is not clean: %w", err)
	}
	retention := m.EffectiveRetention(&entry)
	ancestors, err := activation.Ancestors(entry, len(entry.Activations))
	if err != nil {
		return err
	}
	refs := make([]store.VersionRef, 0, len(ancestors))
	for _, event := range ancestors {
		refs = append(refs, store.VersionRef{Tag: event.Tag, SHA256: event.SHA256})
	}
	return s.PruneHistory(store.PruneRequest{
		Name:            entry.Name,
		Current:         store.VersionRef{Tag: entry.Tag, SHA256: entry.SHA256},
		PinnedTag:       entry.UpdatePolicy.PinnedTag,
		Ancestors:       refs,
		RetainAncestors: retention.RollbackDepth,
	})
}

func downloadChecksums(client *ghrelease.Client, assets []ghrelease.Asset, chosen, tmpDir string) (string, map[string]string, error) {
	checksumName := findChecksumAsset(chosen, assets)
	if checksumName == "" {
		return "", nil, nil
	}
	var checksumURL string
	var checksumSize int64
	for _, a := range assets {
		if a.Name == checksumName {
			checksumURL = a.BrowserDownloadURL
			checksumSize = a.Size
			break
		}
	}
	if checksumURL == "" {
		return checksumName, nil, i18n.Errorf("checksum asset %s has no download URL", checksumName)
	}
	checksumPath, err := client.Download(checksumURL, tmpDir, checksumSize)
	if err != nil {
		return checksumName, nil, i18n.Wrapf("download checksum %s: %w", err, checksumName)
	}
	defer os.Remove(checksumPath)

	f, err := os.Open(checksumPath)
	if err != nil {
		return checksumName, nil, err
	}
	defer f.Close()
	var checksums map[string]string
	if isExactChecksumAsset(chosen, checksumName) {
		checksums, err = verify.ParseChecksumSidecar(f, chosen)
	} else {
		checksums, err = verify.ParseChecksums(f)
	}
	if err != nil {
		return checksumName, nil, i18n.Wrapf("parse checksum %s: %w", err, checksumName)
	}
	return checksumName, checksums, nil
}

func isExactChecksumAsset(chosen, checksumName string) bool {
	return strings.EqualFold(checksumName, chosen+".sha256") ||
		strings.EqualFold(checksumName, chosen+".sha256sum")
}

func findChecksumAsset(chosen string, assets []ghrelease.Asset) string {
	for _, suffix := range []string{".sha256", ".sha256sum"} {
		want := chosen + suffix
		for _, a := range assets {
			if strings.EqualFold(a.Name, want) {
				return a.Name
			}
		}
	}
	for _, a := range assets {
		lower := strings.ToLower(a.Name)
		if !strings.Contains(lower, "checksum") && !strings.Contains(lower, "sha256sum") {
			continue
		}
		if strings.HasSuffix(lower, ".sig") || strings.HasSuffix(lower, ".asc") || strings.HasSuffix(lower, ".pem") {
			continue
		}
		return a.Name
	}
	return ""
}

func archiveExt(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".tar.xz", ".txz", ".zip", ".gz"} {
		if strings.HasSuffix(lower, ext) {
			return ext
		}
	}
	return ""
}

// wantArchiveExe returns the archive-internal executable name an upgrade
// should select: the recorded archive_exe when set, otherwise the tool name.
func wantArchiveExe(e *manifest.Entry) string {
	if e.ArchiveExe != "" {
		return e.ArchiveExe
	}
	return e.Name
}

// writeReleaseNotes prints a bounded preview of the target release's notes,
// when the publisher provided any. It is shown both in --dry-run (before you
// decide) and in the real path (before the download starts), capped at 8 lines
// of 100 display columns per line.
func writeReleaseNotes(w io.Writer, release ghrelease.Release) {
	body := strings.TrimSpace(release.Body)
	if body == "" {
		return
	}
	lines := strings.Split(body, "\n")
	if len(lines) > 8 {
		lines = lines[:8]
	}
	fmt.Fprintln(w, i18n.T("release notes:"))
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			fmt.Fprintln(w, "  .")
			continue
		}
		if output.DisplayWidth(line) > 100 {
			line = output.TruncateDisplay(line, 100)
		}
		fmt.Fprintf(w, "  %s\n", line)
	}
}
