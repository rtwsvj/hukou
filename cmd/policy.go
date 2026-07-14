package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"text/tabwriter"

	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/state"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
	"github.com/spf13/cobra"
)

const policyReportSchemaVersion = 1

var (
	policyShowJSON         bool
	policySetMode          string
	policySetChannel       string
	policySetPin           string
	policySetUnpin         bool
	policySetRollbackDepth int
)

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Inspect or change update and rollback policy",
}

var policyShowCmd = &cobra.Command{
	Use:   "show [name]",
	Short: "Show effective policy without changing state",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := ""
		if len(args) == 1 {
			name = args[0]
		}
		return fail(doPolicyShow(cmd.OutOrStdout(), name, policyShowJSON))
	},
}

var policySetCmd = &cobra.Command{
	Use:   "set <name>",
	Short: "Atomically change policy without touching the live binary",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		flags := cmd.Flags()
		options := policySetOptions{
			Mode:             policySetMode,
			ModeSet:          flags.Changed("mode"),
			Channel:          policySetChannel,
			ChannelSet:       flags.Changed("channel"),
			Pin:              policySetPin,
			PinSet:           flags.Changed("pin"),
			Unpin:            policySetUnpin,
			RollbackDepth:    policySetRollbackDepth,
			RollbackDepthSet: flags.Changed("rollback-depth"),
		}
		return fail(doPolicySet(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], options))
	},
}

func init() {
	policyShowCmd.Flags().BoolVar(&policyShowJSON, "json", false, "emit a stable JSON report")
	policySetCmd.Flags().StringVar(&policySetMode, "mode", "", "release ordering mode: semver or github-latest")
	policySetCmd.Flags().StringVar(&policySetChannel, "channel", "", "release channel: stable or prerelease")
	policySetCmd.Flags().StringVar(&policySetPin, "pin", "", "pin the exact GitHub release tag")
	policySetCmd.Flags().BoolVar(&policySetUnpin, "unpin", false, "remove the exact release pin")
	policySetCmd.Flags().IntVar(&policySetRollbackDepth, "rollback-depth", 0, "number of activation ancestors to retain")
	policyCmd.AddCommand(policyShowCmd, policySetCmd)
	rootCmd.AddCommand(policyCmd)
}

type policyReport struct {
	SchemaVersion int          `json:"schema_version"`
	Policies      []policyView `json:"policies"`
}

type policyView struct {
	Name                string                 `json:"name"`
	Mode                manifest.UpdateMode    `json:"mode"`
	Channel             manifest.UpdateChannel `json:"channel"`
	PinnedTag           string                 `json:"pinned_tag"`
	RollbackDepth       int                    `json:"rollback_depth"`
	RollbackDepthSource string                 `json:"rollback_depth_source"`
}

type policySetOptions struct {
	Mode             string
	ModeSet          bool
	Channel          string
	ChannelSet       bool
	Pin              string
	PinSet           bool
	Unpin            bool
	RollbackDepth    int
	RollbackDepthSet bool
}

func doPolicyShow(stdout io.Writer, name string, jsonOutput bool) error {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return err
	}
	m, err := loadManifest()
	if err != nil {
		return err
	}
	report, err := buildPolicyReport(m, name)
	if err != nil {
		return err
	}
	if jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	return writePolicyTable(stdout, report)
}

func buildPolicyReport(m *manifest.Manifest, name string) (policyReport, error) {
	report := policyReport{SchemaVersion: policyReportSchemaVersion, Policies: make([]policyView, 0)}
	entries := append([]manifest.Entry(nil), m.Entries...)
	if name != "" {
		entry := m.Get(name)
		if entry == nil {
			return policyReport{}, fmt.Errorf("adopted tool %q not found", name)
		}
		entries = []manifest.Entry{*entry}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for _, entry := range entries {
		view, err := effectivePolicyView(m, entry)
		if err != nil {
			return policyReport{}, fmt.Errorf("%s: %w", entry.Name, err)
		}
		report.Policies = append(report.Policies, view)
	}
	return report, nil
}

func effectivePolicyView(m *manifest.Manifest, entry manifest.Entry) (policyView, error) {
	policy, err := versionpolicy.Normalize(entry.UpdatePolicy)
	if err != nil {
		return policyView{}, err
	}
	retention := m.EffectiveRetention(&entry)
	if retention.RollbackDepth < 0 {
		return policyView{}, fmt.Errorf("rollback depth must be non-negative")
	}
	source := "manifest"
	if entry.Retention != nil {
		source = "entry"
	}
	return policyView{
		Name:                entry.Name,
		Mode:                policy.Mode,
		Channel:             policy.Channel,
		PinnedTag:           policy.PinnedTag,
		RollbackDepth:       retention.RollbackDepth,
		RollbackDepthSource: source,
	}, nil
}

func writePolicyTable(stdout io.Writer, report policyReport) error {
	if len(report.Policies) == 0 {
		_, err := fmt.Fprintln(stdout, "No adopted tools.")
		return err
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tMODE\tCHANNEL\tPINNED TAG\tROLLBACK DEPTH\tSOURCE"); err != nil {
		return err
	}
	for _, policy := range report.Policies {
		pin := policy.PinnedTag
		if pin == "" {
			pin = "-"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\n",
			policy.Name,
			policy.Mode,
			policy.Channel,
			pin,
			policy.RollbackDepth,
			policy.RollbackDepthSource,
		); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func doPolicySet(stdout, stderr io.Writer, name string, options policySetOptions) error {
	return doPolicySetWithSave(stdout, stderr, name, options, saveManifest)
}

func doPolicySetWithSave(stdout, stderr io.Writer, name string, options policySetOptions, save func(*manifest.Manifest) error) error {
	if err := validatePolicySetOptions(options); err != nil {
		return err
	}

	// Avoid creating a data root or lock for an unknown entry. The manifest is
	// loaded again after locking, so this check is only an early no-write guard.
	preflight, err := loadManifest()
	if err != nil {
		return err
	}
	if preflight.Get(name) == nil {
		return fmt.Errorf("adopted tool %q not found", name)
	}
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fmt.Errorf("policy changes require clean transaction state: %w", err)
	}

	lock, err := state.Acquire(filepath.Join(dataRoot(), "state.lock"))
	if err != nil {
		return fmt.Errorf("acquire state lock: %w", err)
	}
	defer func() {
		if err := lock.Release(); err != nil && stderr != nil {
			fmt.Fprintf(stderr, "warning: release hukou state lock: %v\n", err)
		}
	}()

	// Policy changes intentionally do not invoke automatic WAL recovery because
	// recovery may mutate the live binary. A pending transaction fails closed.
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fmt.Errorf("policy changes require clean transaction state: %w", err)
	}
	m, err := loadManifest()
	if err != nil {
		return err
	}
	next := m.Clone()
	entry := next.Get(name)
	if entry == nil {
		return fmt.Errorf("adopted tool %q not found", name)
	}
	changed, err := applyPolicySet(entry, options)
	if err != nil {
		return err
	}
	view, err := effectivePolicyView(next, *entry)
	if err != nil {
		return err
	}
	if changed {
		if err := save(next); err != nil {
			return fmt.Errorf("save policy: %w", err)
		}
	}
	prefix := "Policy updated"
	if !changed {
		prefix = "Policy unchanged"
	}
	_, err = fmt.Fprintf(stdout, "%s for %s: mode=%s channel=%s pinned_tag=%s rollback_depth=%d\n",
		prefix,
		view.Name,
		view.Mode,
		view.Channel,
		printablePin(view.PinnedTag),
		view.RollbackDepth,
	)
	return err
}

func validatePolicySetOptions(options policySetOptions) error {
	if !options.ModeSet && !options.ChannelSet && !options.PinSet && !options.Unpin && !options.RollbackDepthSet {
		return errors.New("at least one policy option is required")
	}
	if options.PinSet && options.Unpin {
		return errors.New("--pin and --unpin cannot be used together")
	}
	if options.ModeSet {
		switch manifest.UpdateMode(options.Mode) {
		case manifest.UpdateModeSemver, manifest.UpdateModeLegacy:
		default:
			return fmt.Errorf("invalid --mode %q; expected semver or github-latest", options.Mode)
		}
	}
	if options.ChannelSet {
		switch manifest.UpdateChannel(options.Channel) {
		case manifest.UpdateChannelStable, manifest.UpdateChannelPrerelease:
		default:
			return fmt.Errorf("invalid --channel %q; expected stable or prerelease", options.Channel)
		}
	}
	if options.PinSet {
		if options.Pin == "" {
			return errors.New("--pin requires a non-empty tag")
		}
		if err := store.ValidateTag(options.Pin); err != nil {
			return fmt.Errorf("invalid --pin tag: %w", err)
		}
	}
	if options.RollbackDepthSet && options.RollbackDepth < 0 {
		return errors.New("--rollback-depth must be non-negative")
	}
	return nil
}

func applyPolicySet(entry *manifest.Entry, options policySetOptions) (bool, error) {
	policy, err := versionpolicy.Normalize(entry.UpdatePolicy)
	if err != nil {
		return false, err
	}
	if options.ModeSet && manifest.UpdateMode(options.Mode) == manifest.UpdateModeSemver {
		if entry.Repo == "" || entry.Tag == "local" {
			return false, fmt.Errorf("cannot set semver mode for local entry %q", entry.Name)
		}
		if !versionpolicy.IsSemanticTag(entry.Tag) {
			return false, fmt.Errorf("cannot set semver mode: current tag %q is not a strict Semantic Version", entry.Tag)
		}
	}
	changed := false
	if options.ModeSet && policy.Mode != manifest.UpdateMode(options.Mode) {
		policy.Mode = manifest.UpdateMode(options.Mode)
		changed = true
	}
	if options.ChannelSet && policy.Channel != manifest.UpdateChannel(options.Channel) {
		policy.Channel = manifest.UpdateChannel(options.Channel)
		changed = true
	}
	if options.PinSet && policy.PinnedTag != options.Pin {
		policy.PinnedTag = options.Pin
		changed = true
	}
	if options.Unpin && policy.PinnedTag != "" {
		policy.PinnedTag = ""
		changed = true
	}
	if _, err := versionpolicy.Normalize(policy); err != nil {
		return false, err
	}
	if entry.UpdatePolicy != policy {
		entry.UpdatePolicy = policy
		changed = true
	}
	if options.RollbackDepthSet {
		if entry.Retention == nil || entry.Retention.RollbackDepth != options.RollbackDepth {
			entry.Retention = &manifest.RetentionPolicy{RollbackDepth: options.RollbackDepth}
			changed = true
		}
	}
	return changed, nil
}

func printablePin(pin string) string {
	if pin == "" {
		return "none"
	}
	return pin
}
