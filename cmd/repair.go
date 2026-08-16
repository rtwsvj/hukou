package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/repair"
	"github.com/spf13/cobra"
)

var (
	repairPlanAction string
	repairPlanOutput string
	repairApplyPlan  string
)

var repairCmd = &cobra.Command{
	Use:   "repair",
	Short: "Apply a narrowly scoped repair from a fingerprint-bound plan",
	Args:  cobra.NoArgs,
}

var repairPlanCmd = &cobra.Command{
	Use:   "plan",
	Short: "Inspect local state and write a repair plan without changing it",
	Args:  cobra.NoArgs,
	RunE:  runRepairPlan,
}

var repairApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Revalidate and apply an existing repair plan under the state lock",
	Args:  cobra.NoArgs,
	RunE:  runRepairApply,
}

func init() {
	repairPlanCmd.Flags().StringVar(&repairPlanAction, "action", "", "repair action: recover-transaction or restore-manifest-backup")
	repairPlanCmd.Flags().StringVar(&repairPlanOutput, "output", "", "write the repair plan to this file")
	repairApplyCmd.Flags().StringVar(&repairApplyPlan, "plan", "", "path to a previously generated repair plan")
	repairCmd.AddCommand(repairPlanCmd, repairApplyCmd)
	rootCmd.AddCommand(repairCmd)
}

func runRepairPlan(cmd *cobra.Command, _ []string) error {
	return fail(doRepairPlan(cmd.OutOrStdout(), dataRoot(), repairPlanAction, repairPlanOutput, time.Now()))
}

func doRepairPlan(stdout io.Writer, root, action, output string, now time.Time) error {
	if action == "" {
		return i18n.Errorf("--action is required")
	}
	if output == "" {
		return i18n.Errorf("--output is required")
	}
	plan, err := repair.BuildPlan(root, repair.Action(action), now)
	if err != nil {
		return err
	}
	if err := repair.WritePlan(output, plan); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", i18n.T("Repair plan written: %s", output))
	return err
}

func runRepairApply(cmd *cobra.Command, _ []string) error {
	return fail(doRepairApply(cmd.OutOrStdout(), dataRoot(), repairApplyPlan))
}

func doRepairApply(stdout io.Writer, root, planPath string) error {
	if planPath == "" {
		return i18n.Errorf("--plan is required")
	}
	plan, err := repair.LoadPlan(planPath)
	if err != nil {
		return err
	}
	result, err := repair.Apply(root, plan)
	// Quarantine records are observable state changes that may have happened
	// even when Apply ultimately fails (e.g. unknown directories fail closed
	// after non-directory entries were already isolated), so report them on
	// both paths before surfacing the error.
	for _, record := range result.Quarantined {
		fmt.Fprintf(stdout, "%s\n", i18n.T("Quarantined unknown transaction entry %q as transactions/%s", record.Original, record.Quarantined))
	}
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "%s\n", i18n.T("Repair completed: %s", plan.Action))
	return err
}
