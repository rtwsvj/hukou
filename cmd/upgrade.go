package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/rtwsvj/hukou/internal/archive"
	"github.com/rtwsvj/hukou/internal/assetpick"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/spf13/cobra"
)

var (
	upgradeAll    bool
	upgradeDryRun bool
	upgradeAsset  string
)

var upgradeCmd = &cobra.Command{
	Use:   "upgrade [name ...]",
	Short: "升级已收编工具到最新 GitHub release",
	Long: `对已收编的工具查询 GitHub 最新 release，下载并原子替换。
--dry-run 只报告不动手；--all 升级全部条目；local 条目自动跳过。`,
	Args: cobra.ArbitraryArgs,
	RunE: runUpgrade,
}

func init() {
	upgradeCmd.Flags().BoolVar(&upgradeAll, "all", false, "升级全部已收编条目")
	upgradeCmd.Flags().BoolVar(&upgradeDryRun, "dry-run", false, "只报告不替换")
	upgradeCmd.Flags().StringVar(&upgradeAsset, "asset", "", "资产名子串过滤（前缀 ^ 取反）")
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
		return fail(fmt.Errorf("名称列表与 --all 不能同时使用"))
	}
	if !dryRun {
		lock, err := acquireMutationLock()
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
		fmt.Fprintln(stdout, "没有已收编的工具")
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
				return fail(fmt.Errorf("未找到 %s", n))
			}
			targets = append(targets, *e)
		}
	default:
		return fail(fmt.Errorf("请指定名称或使用 --all"))
	}

	s := newStore()
	if !dryRun {
		if err := s.GC(); err != nil {
			return fail(err)
		}
	}

	var failures []error
	for _, e := range targets {
		// Re-load entry pointer from manifest so concurrent-looking updates
		// within the loop see the latest state after each successful upgrade.
		entry := e
		if live := m.Get(e.Name); live != nil {
			entry = *live
		}
		if err := upgradeOne(stdout, stderr, s, client, m, &entry, dryRun, assetFilter, save); err != nil {
			fmt.Fprintf(stderr, "警告: %s 升级失败: %v\n", entry.Name, err)
			failure := fmt.Errorf("%s: %w", entry.Name, err)
			if !dryRun {
				if cleanErr := statejournal.CheckClean(dataRoot()); cleanErr != nil {
					stopErr := fmt.Errorf("state remains unresolved; stop the upgrade batch before further network or store activity: %w", cleanErr)
					fmt.Fprintf(stderr, "警告: %v\n", stopErr)
					failure = errors.Join(failure, stopErr)
					failures = append(failures, failure)
					break
				}
			}
			failures = append(failures, failure)
		}
	}
	if len(failures) > 0 {
		fmt.Fprintf(stderr, "升级失败 %d 项:\n", len(failures))
		for _, f := range failures {
			fmt.Fprintf(stderr, "  - %v\n", f)
		}
		return fmt.Errorf("%d upgrade(s) failed: %w", len(failures), errors.Join(failures...))
	}
	return nil
}

func upgradeOne(stdout, stderr io.Writer, s *store.Store, client *ghrelease.Client, m *manifest.Manifest, e *manifest.Entry, dryRun bool, assetFilter string, save func(*manifest.Manifest) error) error {
	if e.Repo == "" || e.Tag == "local" {
		fmt.Fprintf(stdout, "跳过 %s: local 条目\n", e.Name)
		return nil
	}

	owner, repoName, ok := splitRepo(e.Repo)
	if !ok {
		return fmt.Errorf("invalid repo %q", e.Repo)
	}

	currentSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fmt.Errorf("无法读取当前文件: %w", err)
	}
	if currentSHA != e.SHA256 {
		return fmt.Errorf("当前文件 sha256 与 manifest 不一致（可能被外部修改）")
	}

	release, err := client.Latest(owner, repoName)
	if err != nil {
		return err
	}
	if release.TagName == e.Tag {
		fmt.Fprintf(stdout, "%s 已是最新 (%s)\n", e.Name, e.Tag)
		return nil
	}

	assetNames := make([]string, len(release.Assets))
	for i, a := range release.Assets {
		assetNames[i] = a.Name
	}
	chosen, note, err := assetpick.Pick(assetNames, runtime.GOOS, runtime.GOARCH, assetFilter)
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintf(stdout, "将升级 %s: %s → %s, 选中资产 %s", e.Name, e.Tag, release.TagName, chosen)
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

	checksumVerified := false
	if checksumAsset != "" {
		if err := verify.VerifyAsset(finalAssetPath, chosen, checksums); err != nil {
			return fmt.Errorf("verify checksum from %s: %w", checksumAsset, err)
		}
		checksumVerified = true
	} else {
		fmt.Fprintf(stderr, "警告: %s release 未提供 checksum；将记录下载资产 SHA-256，但无法验证发布方摘要\n", e.Name)
	}

	assetSHA, err := store.SHA256File(finalAssetPath)
	if err != nil {
		return fmt.Errorf("sha256 downloaded asset: %w", err)
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

	if err := s.Put(e.Name, release.TagName, extractedPath); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	oldTag := e.Tag
	oldEntry := *e
	latestSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fmt.Errorf("re-read current file before activation: %w", err)
	}
	if latestSHA != e.SHA256 {
		return fmt.Errorf("当前文件在升级过程中被外部修改；拒绝覆盖")
	}
	pathInfo, err := os.Lstat(e.Path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", e.Path, err)
	}
	var originalPath string
	needsOriginal := false
	if pathInfo.Mode()&os.ModeSymlink == 0 {
		originalPath, err = s.PrepareOriginalPath(e.Name, e.Name)
		if err != nil {
			return fmt.Errorf("prepare original path: %w", err)
		}
		if _, err := os.Lstat(originalPath); errors.Is(err, os.ErrNotExist) {
			needsOriginal = true
		} else if err != nil {
			return fmt.Errorf("inspect original backup: %w", err)
		} else {
			// original/ already contains the adopt-time backup; copy the
			// current live binary into its validated version directory before
			// publishing the transaction intent.
			if e.Tag != "original" {
				if err := s.Put(e.Name, e.Tag, e.Path); err != nil {
					return fmt.Errorf("backup current version: %w", err)
				}
			}
		}
	}

	targetSource, err := s.ActivationSource(e.Name, release.TagName)
	if err != nil {
		return fmt.Errorf("resolve activation source: %w", err)
	}
	targetSHA, err := store.SHA256File(targetSource)
	if err != nil {
		return fmt.Errorf("sha256 activation source: %w", err)
	}
	newEntry := oldEntry
	newEntry.Tag = release.TagName
	newEntry.SHA256 = targetSHA
	newEntry.UpdatedAt = rfc3339Now()
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
		specs = append(specs, statejournal.Spec{Role: "original", Path: originalPath, After: statejournal.RegularFile(e.Path)})
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
		return abortStateTransaction(tx, fmt.Errorf("当前文件在创建事务日志时被外部修改；拒绝覆盖: %w", err))
	}
	beforeLive, _ := tx.Before("live")
	if pathInfo.Mode()&os.ModeSymlink == 0 && beforeLive.Mode != uint32(pathInfo.Mode().Perm()) {
		return abortStateTransaction(tx, fmt.Errorf("当前文件在创建事务日志时权限发生变化；拒绝覆盖"))
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
		if afterOriginal.Mode != beforeLive.Mode {
			return abortStateTransaction(tx, fmt.Errorf("original snapshot mode differs from live transaction state"))
		}
		if err := tx.Apply("original"); err != nil {
			return abortStateTransaction(tx, fmt.Errorf("adopt original: %w", err))
		}
	}
	if err := tx.Apply("live"); err != nil {
		return abortStateTransaction(tx, fmt.Errorf("当前文件在事务应用前被外部修改或无法激活；拒绝覆盖: activate: %w", err))
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
	if err := commitStateTransaction(tx); err != nil {
		refreshErr := refreshManifest(m)
		if current := m.Get(e.Name); current != nil {
			*e = *current
		}
		return errors.Join(fmt.Errorf("commit upgrade transaction: %w", err), refreshErr)
	}
	finalizeStateTransaction(tx, stderr, e.Name, "升级")

	if err := s.Prune(e.Name, 3, e.Tag, e.SHA256); err != nil {
		fmt.Fprintf(stderr, "警告: %s 清理旧版本失败: %v\n", e.Name, err)
	}

	fmt.Fprintf(stdout, "已升级 %s: %s → %s\n", e.Name, oldTag, release.TagName)
	return nil
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
