package cmd

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
	"github.com/spf13/cobra"
)

var (
	adoptLocal bool
	adoptForce bool
	adoptTag   string
)

var adoptCmd = &cobra.Command{
	Use:   "adopt <name|path> [owner/repo]",
	Short: "收编 PATH 中的二进制进 hukou 管理",
	Long: `adopt 登记一个已存在的可执行文件：
  - 裸名字会在 PATH 中定位（exec.LookPath）
  - 路径参数直接使用该路径
  - Go 二进制可从 buildinfo 的 ModulePath 推导出 github.com/owner/repo
  - 其他二进制必须显式提供 owner/repo，或使用 --local 标记为本地管理`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runAdopt,
}

func init() {
	adoptCmd.Flags().BoolVar(&adoptLocal, "local", false, "本地项目/无 repo，tag 默认为 local")
	adoptCmd.Flags().BoolVar(&adoptForce, "force", false, "强制收编已被其他管理器认领的二进制")
	adoptCmd.Flags().StringVar(&adoptTag, "tag", "", "登记版本标签")
	rootCmd.AddCommand(adoptCmd)
}

func runAdopt(cmd *cobra.Command, args []string) error {
	target := args[0]
	var repoArg string
	if len(args) > 1 {
		repoArg = args[1]
	}
	return doAdopt(cmd.OutOrStdout(), cmd.ErrOrStderr(), target, repoArg, adoptLocal, adoptTag, adoptForce)
}

func doAdopt(stdout, stderr io.Writer, target, repoArg string, local bool, tag string, force bool) error {
	return doAdoptWithDeps(stdout, stderr, target, repoArg, local, tag, force, runSecurityGate, saveManifest)
}

func doAdoptWithDeps(stdout, stderr io.Writer, target, repoArg string, local bool, tag string, force bool, securityGate func(string) (*provenance.Attribution, error), save func(*manifest.Manifest) error) error {
	binPath, err := resolveAdoptTarget(target)
	if err != nil {
		return fail(fmt.Errorf("locate target: %w", err))
	}
	lock, err := acquireMutationLock()
	if err != nil {
		return fail(fmt.Errorf("acquire state lock: %w", err))
	}
	defer releaseMutationLock(lock, stderr)

	info, err := os.Stat(binPath)
	if err != nil {
		return fail(fmt.Errorf("stat %s: %w", binPath, err))
	}
	if !info.Mode().IsRegular() {
		return fail(fmt.Errorf("%s is not a regular file", binPath))
	}
	if info.Mode()&0o111 == 0 {
		return fail(fmt.Errorf("%s is not executable", binPath))
	}
	if info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return fail(fmt.Errorf("%s uses privileged/special mode bits that hukou does not preserve", binPath))
	}

	name := filepath.Base(binPath)
	if err := store.ValidateName(name); err != nil {
		return fail(fmt.Errorf("invalid tool name: %w", err))
	}
	sha, err := store.SHA256File(binPath)
	if err != nil {
		return fail(fmt.Errorf("sha256 %s: %w", binPath, err))
	}

	var repo, upstream string
	if local {
		repo = ""
		if tag == "" {
			tag = "local"
		}
	} else {
		if repoArg != "" {
			repo = repoArg
		} else {
			if goInfo, ok := provenance.ReadGoBinary(binPath); ok {
				upstream = goInfo.ModulePath
				repo = modulePathToRepo(goInfo.ModulePath)
				if tag == "" && goInfo.Version != "" && goInfo.Version != "(devel)" {
					tag = goInfo.Version
				}
			}
		}
		if repo == "" {
			return fail(fmt.Errorf("无法推导 repo，请显式提供 owner/repo 或使用 --local"))
		}
		if tag == "" {
			tag = "adopted"
		}
	}
	if err := store.ValidateTag(tag); err != nil {
		return fail(fmt.Errorf("invalid adoption tag: %w", err))
	}

	attr, err := securityGate(binPath)
	if err != nil {
		return fail(err)
	}
	if attr == nil {
		return fail(fmt.Errorf("安全归属检查未返回结果"))
	}
	if !allowedAdoptSource(attr.Source) && !force {
		return fail(fmt.Errorf("%s 已被 %s 认领（%s）；使用 --force 强制收编", name, attr.Source, attr.Evidence))
	}

	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	if existing := m.Get(name); existing != nil {
		return fail(fmt.Errorf("%s 已收编，登记路径为 %s；拒绝覆盖现有条目", name, existing.Path))
	}
	cleanPath := filepath.Clean(binPath)
	for _, existing := range m.Entries {
		if filepath.Clean(existing.Path) == cleanPath {
			return fail(fmt.Errorf("路径 %s 已登记为 %s；拒绝重复收编", binPath, existing.Name))
		}
	}

	s := newStore()
	if err := s.GC(); err != nil {
		return fail(err)
	}

	if err := s.AdoptOriginal(name, binPath); err != nil {
		return fail(fmt.Errorf("backup original: %w", err))
	}
	origPath := filepath.Join(storeRoot(), name, "original", name)
	backupSHA, backupErr := store.SHA256File(origPath)
	liveSHA, liveErr := store.SHA256File(binPath)
	backupInfo, backupStatErr := os.Stat(origPath)
	liveInfo, liveStatErr := os.Stat(binPath)
	modeChanged := backupStatErr == nil && liveStatErr == nil &&
		(backupInfo.Mode().Perm() != info.Mode().Perm() || liveInfo.Mode().Perm() != info.Mode().Perm())
	if backupErr != nil || liveErr != nil || backupStatErr != nil || liveStatErr != nil || backupSHA != sha || liveSHA != sha || modeChanged {
		_ = os.Remove(origPath)
		return fail(fmt.Errorf("binary changed while creating original backup; refusing inconsistent adoption (backup_err=%v live_err=%v backup_stat_err=%v live_stat_err=%v)", backupErr, liveErr, backupStatErr, liveStatErr))
	}

	entry := manifest.Entry{
		Name:      name,
		Path:      binPath,
		Repo:      repo,
		Tag:       tag,
		SHA256:    sha,
		Upstream:  upstream,
		AdoptedAt: rfc3339Now(),
		UpdatedAt: rfc3339Now(),
	}
	m.Put(entry)
	if err := save(m); err != nil {
		_ = os.Remove(origPath)
		return fail(fmt.Errorf("save manifest: %w", err))
	}

	fmt.Fprintf(stdout, "已收编 %s (%s) → %s\n", name, tag, binPath)
	if repo != "" {
		fmt.Fprintf(stdout, "repo: %s\n", repo)
	}
	return nil
}

func resolveAdoptTarget(target string) (string, error) {
	if filepath.IsAbs(target) || strings.ContainsAny(target, `/\`) {
		return filepath.Abs(target)
	}
	return exec.LookPath(target)
}
