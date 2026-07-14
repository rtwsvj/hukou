package cmd

import (
	"errors"
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/doctor"
	"github.com/spf13/cobra"
)

var (
	doctorJSON bool
	doctorDeep bool
)

var errDoctorFindings = errors.New("hukou doctor found state problems")

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "只读检查 manifest、store 与活跃文件的一致性",
	Long: `doctor 对 hukou 本地状态执行只读诊断。
默认不创建目录、不获取写锁、不清理临时文件，也不联网。
--deep 额外计算保留版本摘要并检查已登记活跃目录中的 hukou 临时文件。`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "输出稳定 JSON 报告")
	doctorCmd.Flags().BoolVar(&doctorDeep, "deep", false, "深度检查保留版本与活跃目录临时文件")
	rootCmd.AddCommand(doctorCmd)
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	return doDoctor(cmd.OutOrStdout(), dataRoot(), doctorJSON, doctorDeep)
}

func doDoctor(stdout io.Writer, root string, jsonOutput, deep bool) error {
	report := doctor.Scan(doctor.Options{DataRoot: root, Deep: deep})
	var err error
	if jsonOutput {
		err = doctor.WriteJSON(stdout, report)
	} else {
		err = doctor.WriteText(stdout, report)
	}
	if err != nil {
		return fmt.Errorf("render doctor report: %w", err)
	}
	if !report.Healthy() {
		return fmt.Errorf("%w: status=%s", errDoctorFindings, report.Status)
	}
	return nil
}
