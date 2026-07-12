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
	"github.com/rtwsvj/hukou/internal/store"
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
	if err := s.GC(); err != nil {
		return fail(err)
	}

	for _, e := range targets {
		if err := upgradeOne(stdout, stderr, s, client, m, &e, dryRun, assetFilter); err != nil {
			fmt.Fprintf(stderr, "警告: %s 升级失败: %v\n", e.Name, err)
		}
	}
	return nil
}

func upgradeOne(stdout, stderr io.Writer, s *store.Store, client *ghrelease.Client, m *manifest.Manifest, e *manifest.Entry, dryRun bool, assetFilter string) error {
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
	for _, a := range release.Assets {
		if a.Name == chosen {
			assetURL = a.BrowserDownloadURL
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

	assetPath, err := client.Download(assetURL, tmpDir)
	if err != nil {
		return fmt.Errorf("download asset: %w", err)
	}
	defer os.Remove(assetPath)

	// ghrelease.Download writes to a temp file without the original extension.
	// archive.Extract decides the format by extension, so preserve it.
	if ext := archiveExt(chosen); ext != "" && !strings.HasSuffix(strings.ToLower(filepath.Base(assetPath)), ext) {
		newPath := assetPath + ext
		if err := os.Rename(assetPath, newPath); err != nil {
			return fmt.Errorf("preserve extension: %w", err)
		}
		assetPath = newPath
	}

	checksums, err := downloadChecksums(client, release.Assets, chosen, tmpDir)
	if err != nil {
		return err
	}

	if checksums != nil {
		if err := verify.VerifyAsset(assetPath, chosen, checksums); err != nil {
			if !errors.Is(err, verify.ErrNoChecksum) {
				return fmt.Errorf("verify: %w", err)
			}
		}
	}

	extractDir, err := os.MkdirTemp(tmpDir, "extract-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)

	extractedPath, err := archive.Extract(assetPath, extractDir, e.Name)
	if err != nil {
		return fmt.Errorf("extract: %w", err)
	}

	if err := s.Put(e.Name, release.TagName, extractedPath); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	pathInfo, err := os.Lstat(e.Path)
	if err != nil {
		return fmt.Errorf("lstat %s: %w", e.Path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink == 0 {
		// First upgrade: the live file at e.Path is not yet managed by a symlink.
		origPath := filepath.Join(s.Root, e.Name, "original", e.Name)
		if _, err := os.Stat(origPath); os.IsNotExist(err) {
			if err := s.AdoptOriginal(e.Name, e.Path); err != nil {
				return fmt.Errorf("adopt original: %w", err)
			}
		} else {
			// original/ already contains the adopt-time backup; move the
			// current live binary into a version directory and activate the
			// newly downloaded version.
			backupDir := filepath.Join(s.Root, e.Name, e.Tag)
			if err := os.MkdirAll(backupDir, 0o755); err != nil {
				return err
			}
			backupPath := filepath.Join(backupDir, e.Name)
			if err := moveFile(e.Path, backupPath); err != nil {
				return fmt.Errorf("backup current version: %w", err)
			}
			if err := s.Activate(e.Name, release.TagName, e.Path); err != nil {
				_ = moveFile(backupPath, e.Path)
				return fmt.Errorf("activate: %w", err)
			}
		}
	} else {
		if err := s.Activate(e.Name, release.TagName, e.Path); err != nil {
			return fmt.Errorf("activate: %w", err)
		}
	}

	e.Tag = release.TagName
	activeSHA, err := store.SHA256File(e.Path)
	if err != nil {
		return fmt.Errorf("sha256 active binary: %w", err)
	}
	e.SHA256 = activeSHA
	e.UpdatedAt = rfc3339Now()
	m.Put(*e)
	if err := saveManifest(m); err != nil {
		return fmt.Errorf("save manifest: %w", err)
	}

	if err := s.Prune(e.Name, 3); err != nil {
		fmt.Fprintf(stderr, "警告: %s 清理旧版本失败: %v\n", e.Name, err)
	}

	fmt.Fprintf(stdout, "已升级 %s: %s → %s\n", e.Name, e.Tag, release.TagName)
	return nil
}

func downloadChecksums(client *ghrelease.Client, assets []ghrelease.Asset, chosen, tmpDir string) (map[string]string, error) {
	checksumName := findChecksumAsset(chosen, assets)
	if checksumName == "" {
		return nil, nil
	}
	var checksumURL string
	for _, a := range assets {
		if a.Name == checksumName {
			checksumURL = a.BrowserDownloadURL
			break
		}
	}
	if checksumURL == "" {
		return nil, nil
	}
	checksumPath, err := client.Download(checksumURL, tmpDir)
	if err != nil {
		return nil, fmt.Errorf("download checksum: %w", err)
	}
	defer os.Remove(checksumPath)

	f, err := os.Open(checksumPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return verify.ParseChecksums(f), nil
}

func findChecksumAsset(chosen string, assets []ghrelease.Asset) string {
	for _, a := range assets {
		if strings.Contains(a.Name, "checksums") {
			return a.Name
		}
	}
	for _, a := range assets {
		if a.Name == chosen+".sha256" || a.Name == chosen+".sha256sum" {
			return a.Name
		}
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
