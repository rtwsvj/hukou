package cmd

import (
	"fmt"
	"io"
	"time"

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
	repairPlanCmd.Flags().StringVar(&repairPlanAction, "action", "", "repair action: recover-transaction, restore-manifest-backup, purge-quarantine, or clean-live-temps")
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
		return fmt.Errorf("--action is required")
	}
	if output == "" {
		return fmt.Errorf("--output is required")
	}
	plan, err := repair.BuildPlan(root, repair.Action(action), now)
	if err != nil {
		return err
	}
	if err := repair.WritePlan(output, plan); err != nil {
		return err
	}
	_, err = fmt.Fprintf(stdout, "Repair plan written: %s\n", output)
	return err
}

func runRepairApply(cmd *cobra.Command, _ []string) error {
	return fail(doRepairApply(cmd.OutOrStdout(), dataRoot(), repairApplyPlan))
}

func doRepairApply(stdout io.Writer, root, planPath string) error {
	if planPath == "" {
		return fmt.Errorf("--plan is required")
	}
	plan, err := repair.LoadPlan(planPath)
	if err != nil {
		return err
	}
	result, err := repair.Apply(root, plan)
	if err != nil {
		return err
	}
	for _, record := range result.Quarantined {
		fmt.Fprintf(stdout, "Quarantined unknown transaction entry %q as transactions/%s\n", record.Original, record.Quarantined)
	}
	for _, name := range result.PurgedQuarantine {
		fmt.Fprintf(stdout, "Purged quarantined entry transactions/%s\n", name)
	}
	for _, path := range result.RemovedLiveTemps {
		fmt.Fprintf(stdout, "Removed orphaned live temporary %s\n", path)
	}
	for _, path := range result.SkippedLiveTemps {
		fmt.Fprintf(stdout, "Skipped live temporary %s: it no longer matches the planned identity\n", path)
	}
	_, err = fmt.Fprintf(stdout, "Repair completed: %s\n", plan.Action)
	return err
}
