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
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/updatecheck"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/spf13/cobra"
)

var (
	upgradeAll    bool
	upgradeDryRun bool
	upgradeAsset  string
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
download verified assets, and replace the live executable atomically.
--dry-run only reports; --all checks every entry; local entries are skipped.`,
	Args: cobra.ArbitraryArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeAll, "all", false, "upgrade all hukou-adopted tools")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "report without replacing executables")
	upgradeCmd.Flags().StringVar(&upgradeAsset, "asset", "", "filter asset names by substring (prefix with ^ to exclude)")
	rootCmd.AddCommand(upgradeCmd)
}

func runUpgrade(cmd *cobra.Command, args []string) error {
	token := firstEnv("GITHUB_TOKEN", "GH_TOKEN")
	client := ghrelease.New(token)
	return doUpgrade(cmd.OutOrStdout(), cmd.ErrOrStderr(), args, upgradeAll, upgradeDryRun, upgradeAsset, client)
}

func doUpgrade(stdout, stderr io.Writer, names []string, all, dryRun bool, assetFilter string, client *ghrelease.Client) error {
	return doUpgradeWithSave(stdout, stderr, names, all, dryRun, assetFilter, client, saveManifest)
}

func doUpgradeWithSave(stdout, stderr io.Writer, names []string, all, dryRun bool, assetFilter string, client *ghrelease.Client, save func(*manifest.Manifest) error) error {
	if all && len(names) > 0 {
		return fail(fmt.Errorf("tool names and --all cannot be used together"))
	}
	if !dryRun {
		lock, err := acquireMutationLock(stderr)
		if err != nil {
			return fail(fmt.Errorf("acquire state lock: %w", err))
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
		fmt.Fprintln(stdout, "No adopted tools")
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
				return fail(fmt.Errorf("adopted tool %q not found", n))
			}
			targets = append(targets, *e)
		}
	default:
		return fail(fmt.Errorf("specify at least one tool name or use --all"))
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
		if err := upgradeOne(stdout, stderr, s, client, m, &entry, dryRun, assetFilter, save); err != nil {
			fmt.Fprintf(stderr, "Warning: failed to upgrade %s: %v\n", entry.Name, err)
			failure := fmt.Errorf("%s: %w", entry.Name, err)
			if !dryRun {
				if cleanErr := statejournal.CheckClean(dataRoot()); cleanErr != nil {
					stopErr := fmt.Errorf("state remains unresolved; stop the upgrade batch before further network or store activity: %w", cleanErr)
					fmt.Fprintf(stderr, "Warning: %v\n", stopErr)
					failure = errors.Join(failure, stopErr)
					failures = append(failures, failure)
					break
				}
			}
			failures = append(failures, failure)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(stderr, "%d upgrade(s) failed:\n", len(failures))
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %v\n", f)
		}
		return fmt.Errorf("%d upgrade(s) failed: %w", len(failures), errors.Join(failures...))
	}
	return nil
}

func upgradeOne(stdout, stderr io.Writer, s *store.Store, client *ghrelease.Client, m *manifest.Manifest, e *manifest.Entry, dryRun bool, assetFilter string, save func(*manifest.Manifest) error) error {
	checked, err := updatecheck.New(client).Check(*e, assetFilter)
	if err != nil {
		return err
	}
	if checked.Status == updatecheck.StatusLocal {
		fmt.Fprintf(stdout, "Skipped %s: local entry\n", e.Name)
		return nil
	}
	if checked.Status == updatecheck.StatusCurrent {
		fmt.Fprintf(stdout, "%s is up to date (%s)\n", e.Name, e.Tag)
		return nil
	}
	if checked.Status != updatecheck.StatusOutdated {
		return fmt.Errorf("cannot upgrade %s: update status %s", e.Name, checked.Status)
	}
	release := checked.Release
	chosen := checked.Asset
	note := checked.Note

	if dryRun {
		fmt.Fprintf(stdout, "Would upgrade %s: %s -> %s using asset %s", e.Name, e.Tag, checked.LatestTag, chosen)
		if note != "" {
			fmt.Fprintf(stdout, " (%s)", note)
		}
		fmt.Fprintln(stdout)
		return nil
	}

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
		return fmt.Errorf("asset %s not found in release", chosen)
	}

	tmpDir := filepath.Join(s.Root, ".tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return err
	}

	assetPath, err := client.Download(assetURL, tmpDir, assetSize)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	// Closure captures the final path after any rename that preserves extension.
	finalAssetPath := assetPath
	defer func() { _ = os.Remove(finalAssetPath) }()

	// ghrelease.Download writes to a temp file without the original extension.
	// archive.Extract decides the format by extension, so preserve it.
	if ext := archiveExt(chosen); ext != "" && !strings.HasSuffix(strings.ToLower(filepath.Base(finalAssetPath)), ext) {
		newPath := finalAssetPath + ext
		if err := os.Rename(finalAssetPath, newPath); err != nil {
			return fmt.Errorf("preserve extension: %w", err)
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
		return fmt.Errorf("sha256 downloaded asset: %w", err)
	}

	checksumVerified := false
	if checksumAsset != "" {
		if err := verify.VerifyAssetDigest(assetSHA, chosen, checksums); err != nil {
			return fmt.Errorf("verify checksum from %s: %w", checksumAsset, err)
		}
		checksumVerified = true
	} else {
		fmt.Fprintf(stderr, "Warning: %s release has no checksum asset; hukou will record the downloaded asset SHA-256 but cannot verify a publisher-provided digest\n", e.Name)
	}

	extractDir, err := os.MkdirTemp(tmpDir, "extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)

	extractedPath, err := archive.Extract(finalAssetPath, extractDir, e.Name)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	extractedInfo, err := os.Stat(extractedPath)
	if err != nil {
		return fmt.Errorf("stat extracted binary: %w", err)
	}
	if !extractedInfo.Mode().IsRegular() || extractedInfo.Mode()&0o111 == 0 {
		return fmt.Errorf("extracted asset is not an executable regular file: %s", extractedPath)
	}
	if archive.DetectFormat(chosen) == archive.FormatBare {
		kind, err := scan.DetectKind(extractedPath)
		if err != nil {
			return fmt.Errorf("inspect bare executable: %w", err)
		}
		if kind == scan.KindOther {
			return fmt.Errorf("selected bare asset is not a recognized executable or shebang script: %s", chosen)
		}
	}

	newVersionSHA, err := s.PutWithDigest(e.Name, release.TagName, extractedPath)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if upgradeTestHookAfterStoreNewVersion != nil {
		upgradeTestHookAfterStoreNewVersion(e.Name, release.TagName)
	}

	oldTag := e.Tag
	oldEntry := manifest.PrepareEntry(*e)
	latestSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fmt.Errorf("re-read current file before activation: %w", err)
	}
	if latestSHA != e.SHA256 {
		return fmt.Errorf("current file changed during the upgrade; refusing overwrite")
	}
	pathInfo, err := os.Lstat(e.Path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", e.Path, err)
	}
	currentTargetInfo, err := os.Stat(e.Path)
	if err != nil {
		return fmt.Errorf("stat current executable target: %w", err)
	}
	if e.Tag != "original" {
		// Every logical parent must have a content-addressed store source before
		// the new activation is published. Put follows a live symlink to its
		// regular target, so legacy symlink entries remain rollback-capable.
		if err := s.Put(e.Name, e.Tag, e.Path); err != nil {
			return fmt.Errorf("backup current version: %w", err)
		}
	}

	originalSource := e.Path
	if pathInfo.Mode()&os.ModeSymlink != 0 {
		originalSource, err = filepath.EvalSymlinks(e.Path)
		if err != nil {
			return fmt.Errorf("resolve current executable symlink: %w", err)
		}
	}
	originalPath, err := s.PrepareOriginalPath(e.Name, e.Name)
	if err != nil {
		return fmt.Errorf("prepare original path: %w", err)
	}
	needsOriginal := false
	if _, err := os.Lstat(originalPath); errors.Is(err, os.ErrNotExist) {
		needsOriginal = true
	} else if err != nil {
		return fmt.Errorf("inspect original backup: %w", err)
	}
	originalEntries, err := os.ReadDir(filepath.Dir(originalPath))
	if err != nil {
		return fmt.Errorf("inspect original backup namespace: %w", err)
	}
	if needsOriginal {
		if len(originalEntries) != 0 {
			return fmt.Errorf("original backup namespace is not empty")
		}
	} else if len(originalEntries) != 1 || originalEntries[0].Name() != filepath.Base(originalPath) || !originalEntries[0].Type().IsRegular() {
		return fmt.Errorf("original backup namespace has unexpected contents")
	}

	targetSource, err := s.ActivationSource(e.Name, release.TagName)
	if err != nil {
		return fmt.Errorf("resolve activation source: %w", err)
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
		return fmt.Errorf("prepare upgrade activation: %w", err)
	}
	if err := activation.RecordUpgrade(&newEntry, eventID, release.TagName, targetSHA, rfc3339Now()); err != nil {
		return fmt.Errorf("prepare upgrade activation: %w", err)
	}
	newEntry.AssetName = chosen
	newEntry.AssetSHA256 = assetSHA
	newEntry.ChecksumAsset = checksumAsset
	newEntry.ChecksumVerified = checksumVerified
	afterManifest := cloneManifest(m)
	afterManifest.Put(newEntry)
	manifestBytes, err := encodeManifest(afterManifest)
	if err != nil {
		return fmt.Errorf("encode target manifest: %w", err)
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
		return fmt.Errorf("prepare upgrade transaction: %w", err)
	}
	if err := validateTransactionStateSHA(tx, "live", oldEntry.SHA256, false); err != nil {
		return abortStateTransaction(tx, fmt.Errorf("current file changed while preparing the transaction; refusing overwrite: %w", err))
	}
	beforeLive, _ := tx.Before("live")
	if pathInfo.Mode()&os.ModeSymlink == 0 && beforeLive.Mode != uint32(pathInfo.Mode().Perm()) {
		return abortStateTransaction(tx, fmt.Errorf("current file permissions changed while preparing the transaction; refusing overwrite"))
	}
	if err := validateTransactionStateSHA(tx, "live", targetSHA, true); err != nil {
		return abortStateTransaction(tx, err)
	}
	if needsOriginal {
		before, _ := tx.Before("original")
		if before.Kind != statejournal.KindAbsent {
			return abortStateTransaction(tx, fmt.Errorf("original backup appeared concurrently: %s", originalPath))
		}
		if err := validateTransactionStateSHA(tx, "original", oldEntry.SHA256, true); err != nil {
			return abortStateTransaction(tx, err)
		}
		afterOriginal, _ := tx.After("original")
		if afterOriginal.Mode != uint32(currentTargetInfo.Mode().Perm()) {
			return abortStateTransaction(tx, fmt.Errorf("original snapshot mode differs from live transaction state"))
		}
		if err := tx.Apply("original"); err != nil {
			return abortStateTransaction(tx, fmt.Errorf("adopt original: %w", err))
		}
		originalEntries, entriesErr := os.ReadDir(filepath.Dir(originalPath))
		if entriesErr != nil || len(originalEntries) != 1 || originalEntries[0].Name() != filepath.Base(originalPath) || !originalEntries[0].Type().IsRegular() {
			return abortStateTransaction(tx, fmt.Errorf("original backup namespace changed during upgrade: %v", entriesErr))
		}
	}
	if err := tx.Apply("live"); err != nil {
		return abortStateTransaction(tx, fmt.Errorf("current file changed before activation or the target could not be activated; refusing overwrite: %w", err))
	}
	if err := tx.Verify("manifest", false); err != nil {
		return abortStateTransaction(tx, fmt.Errorf("manifest changed during upgrade; refusing overwrite: %w", err))
	}
	*e = newEntry
	m.Put(newEntry)
	if err := save(m); err != nil {
		*e = oldEntry
		m.Put(oldEntry)
		return abortStateTransaction(tx, fmt.Errorf("save manifest: %w", err))
	}
	if err := commitStateTransaction(tx, stderr); err != nil {
		refreshErr := refreshManifest(m)
		if current := m.Get(e.Name); current != nil {
			*e = *current
		}
		return errors.Join(fmt.Errorf("commit upgrade transaction: %w", err), refreshErr)
	}
	finalizeStateTransaction(tx, stderr, e.Name, "upgrade")

	if err := pruneActivationHistory(s, m, *e); err != nil {
		fmt.Fprintf(stderr, "Warning: failed to prune old versions for %s: %v\n", e.Name, err)
	}

	fmt.Fprintf(stdout, "Upgraded %s: %s -> %s\n", e.Name, oldTag, release.TagName)
	return nil
}

func pruneActivationHistory(s *store.Store, m *manifest.Manifest, entry manifest.Entry) error {
	// A journal that could still reference a retained artifact takes priority
	// over garbage collection. This also covers the edge where COMMIT succeeded
	// but final journal cleanup failed: zero versions are deleted until recovery
	// and cleanup have completed under a later mutation lock.
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fmt.Errorf("skip pruning while transaction state is not clean: %w", err)
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
		return checksumName, nil, fmt.Errorf("checksum asset %s has no download URL", checksumName)
	}
	checksumPath, err := client.Download(checksumURL, tmpDir, checksumSize)
	if err != nil {
		return checksumName, nil, fmt.Errorf("download checksum %s: %w", checksumName, err)
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
		return checksumName, nil, fmt.Errorf("parse checksum %s: %w", checksumName, err)
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
