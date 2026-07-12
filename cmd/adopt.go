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

func doAdopt(stdout, _ io.Writer, target, repoArg string, local bool, tag string, force bool) error {
	binPath, err := resolveAdoptTarget(target)
	if err != nil {
		return fail(fmt.Errorf("locate target: %w", err))
	}

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

	name := filepath.Base(binPath)
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

	if !local {
		attr, err := runSecurityGate(binPath)
		if err != nil {
			return fail(err)
		}
		if !allowedAdoptSource(attr.Source) && !force {
			return fail(fmt.Errorf("%s 已被 %s 认领（%s）；使用 --force 强制收编", name, attr.Source, attr.Evidence))
		}
	}

	if err := os.MkdirAll(dataRoot(), 0o755); err != nil {
		return fail(err)
	}
	s := newStore()
	if err := s.GC(); err != nil {
		return fail(err)
	}

	// Backup the current binary into store/<name>/original/ without touching
	// the original file itself.
	origDir := filepath.Join(storeRoot(), name, "original")
	if err := os.MkdirAll(origDir, 0o755); err != nil {
		return fail(err)
	}
	origPath := filepath.Join(origDir, name)
	if err := copyFile(binPath, origPath, info.Mode()); err != nil {
		return fail(fmt.Errorf("backup original: %w", err))
	}

	m, err := loadManifest()
	if err != nil {
		return fail(err)
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
	if err := saveManifest(m); err != nil {
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
