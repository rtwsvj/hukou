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
	Short: "Audit the manifest, store, journal, and live files without writing",
	Long: `doctor performs a read-only audit of hukou's local state.
By default it creates no directories, takes no mutation lock, removes no
temporary files, and makes no network request. --deep also hashes retained
versions and checks registered live directories for hukou temporary files.`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
}

func init() {
	doctorCmd.Flags().BoolVar(&doctorJSON, "json", false, "emit a stable JSON report")
	doctorCmd.Flags().BoolVar(&doctorDeep, "deep", false, "hash retained versions and inspect live-directory temporary files")
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
