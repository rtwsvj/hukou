package cmd

import (
	"fmt"
	"io"

	"github.com/rtwsvj/hukou/internal/buildinfo"
	"github.com/rtwsvj/hukou/internal/supportbundle"
	"github.com/spf13/cobra"
)

var (
	supportBundleOutput string
	supportBundleFormat string
)

var supportCmd = &cobra.Command{
	Use:   "support",
	Short: "Create privacy-preserving local support diagnostics",
	Args:  cobra.NoArgs,
}

var supportBundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Build an offline, redacted support report",
	Long: `Build an offline support report without uploading anything.
The report excludes absolute paths, repositories, environment variables,
usernames, HOME, binaries, and transaction payloads.`,
	Args: cobra.NoArgs,
	RunE: runSupportBundle,
}

func init() {
	supportBundleCmd.Flags().StringVar(&supportBundleOutput, "output", "", "write the JSON report to FILE with 0600 permissions")
	supportBundleCmd.Flags().StringVar(&supportBundleFormat, "format", "", "write to stdout (supported: json)")
	supportCmd.AddCommand(supportBundleCmd)
	rootCmd.AddCommand(supportCmd)
}

func runSupportBundle(cmd *cobra.Command, _ []string) error {
	return fail(doSupportBundle(cmd.OutOrStdout(), dataRoot(), supportBundleOutput, supportBundleFormat, supportbundle.Build{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Date:    buildinfo.Date,
	}))
}

func doSupportBundle(stdout io.Writer, root, output, format string, build supportbundle.Build) error {
	if output == "" && format == "" {
		return fmt.Errorf("use exactly one of --output FILE or --format json")
	}
	if output != "" && format != "" {
		return fmt.Errorf("--output and --format are mutually exclusive")
	}
	if format != "" && format != "json" {
		return fmt.Errorf("unsupported support bundle format %q (expected json)", format)
	}
	report := supportbundle.Collect(root, build)
	if output != "" {
		if err := supportbundle.WriteFile(output, report); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Support bundle written: %s\n", output)
		return err
	}
	return supportbundle.WriteJSON(stdout, report)
}
