package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// rootCmd is the base command for hukou.
var rootCmd = &cobra.Command{
	Use:   "hukou",
	Short: "给机器上所有 CLI 工具上户口：盘点、溯源、收编、安全升级",
	Long: `hukou（户口）扫描 PATH 中的可执行文件，判定安装来源，
并提供收编、可校验升级、版本回滚与本地状态追踪。`,
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.SilenceUsage = true
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
}

// fail prints err and returns a non-nil error for cobra.
func fail(err error) error {
	if err == nil {
		return nil
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	return err
}
