package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
	"github.com/spf13/cobra"
)

var (
	scanJSON        bool
	scanUnknownOnly bool
	scanSource      string
	scanDirs        []string
)

var scanCmd = &cobra.Command{
	Use:   "scan",
	Short: "扫描 PATH 中的可执行文件并判定安装来源",
	Long: `遍历 PATH（及可选 --dir）中的可执行文件，按责任链判定归属来源，
输出表格或 JSON。纯本地只读，不联网、不写用户目录。`,
	RunE: runScan,
}

func init() {
	scanCmd.Flags().BoolVar(&scanJSON, "json", false, "以 JSON 输出完整结构")
	scanCmd.Flags().BoolVar(&scanUnknownOnly, "unknown-only", false, "只列出无主（unknown）二进制")
	scanCmd.Flags().StringVar(&scanSource, "source", "", "只列出指定来源（如 brew、system、unknown）")
	scanCmd.Flags().StringArrayVar(&scanDirs, "dir", nil, "PATH 之外追加扫描目录（可多次）")
	rootCmd.AddCommand(scanCmd)
}

func runScan(cmd *cobra.Command, args []string) error {
	pathEnv := os.Getenv("PATH")
	pathDirs := scan.SplitPATH(pathEnv)
	pathDirs = append(pathDirs, scanDirs...)

	result, err := scan.Walk(pathDirs)
	if err != nil {
		return fail(err)
	}

	env := provenance.DefaultEnv()
	runner := provenance.DefaultRunner()
	if err := runner.Load(env); err != nil {
		return fail(fmt.Errorf("load detectors: %w", err))
	}

	rows := make([]output.Row, 0, len(result.Binaries))
	for _, b := range result.Binaries {
		attr := runner.Match(b)
		if attr == nil {
			// Should not happen if unknown is always last; guard anyway.
			attr = &provenance.Attribution{
				Source:     "unknown",
				Confidence: "inferred",
				Evidence:   "no detector matched",
			}
		}
		if scanUnknownOnly && attr.Source != "unknown" {
			continue
		}
		if scanSource != "" && !strings.EqualFold(attr.Source, scanSource) {
			continue
		}
		rows = append(rows, output.Row{Binary: b, Attribution: *attr})
	}

	report := output.Report{
		Rows:        rows,
		Skipped:     result.Skipped,
		ScanErrors:  result.Errors,
		TotalWalked: len(result.Binaries),
	}

	w := cmd.OutOrStdout()
	if scanJSON {
		return fail(output.WriteJSON(w, report))
	}
	return fail(output.WriteTable(w, report))
}
