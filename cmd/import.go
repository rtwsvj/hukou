package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/spf13/cobra"
)

// importReportSchemaVersion pins the --json envelope of `hukou import`.
const importReportSchemaVersion = 1

var (
	importDryRun bool
	importForce  bool
	importJSON   bool
	importOnly   []string
)

var importCmd = &cobra.Command{
	Use:   "import <FILE>",
	Short: "Re-register tools listed in an exported toolset list",
	Long: `import re-registers, on this machine, the tools recorded in a list
written by "hukou export".

For every github entry the executable must already exist on PATH: import
re-runs the full adoption inspection (ownership gate included, override with
--force) and never downloads anything. Entries already adopted are skipped
with a warning; local entries (no repository) cannot be reproduced and are
skipped with a warning; a missing executable is a per-tool error. The exported
update policy (channel/pin) is re-applied when it differs from the adopt
default. --dry-run prints the whole plan and writes nothing; --only restricts
the run to the named tools. The exit status is non-zero when any tool fails.`,
	Args: cobra.ExactArgs(1),
	RunE: runImport,
}

func init() {
	importCmd.Flags().BoolVar(&importDryRun, "dry-run", false, "print the re-registration plan without writing anything")
	importCmd.Flags().BoolVar(&importForce, "force", false, "override another manager's ownership claim during re-registration")
	importCmd.Flags().BoolVar(&importJSON, "json", false, "emit a stable JSON report")
	importCmd.Flags().StringSliceVar(&importOnly, "only", nil, "restrict to these tool names (repeatable or comma-separated)")
	rootCmd.AddCommand(importCmd)
}

func runImport(cmd *cobra.Command, args []string) error {
	return doImport(cmd.OutOrStdout(), cmd.ErrOrStderr(), args[0], importDryRun, importForce, importJSON, importOnly)
}

type importReport struct {
	SchemaVersion int            `json:"schema_version"`
	Results       []importResult `json:"results"`
	Imported      int            `json:"imported"`
	Skipped       int            `json:"skipped"`
	Failed        int            `json:"failed"`
}

type importResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // "ok" | "skipped" | "error"
	Detail string `json:"detail,omitempty"`
}

func doImport(stdout, stderr io.Writer, file string, dryRun, force, jsonOutput bool, only []string) error {
	doc, err := readExportDoc(file)
	if err != nil {
		return fail(err)
	}
	onlySet := make(map[string]struct{}, len(only))
	for _, n := range only {
		onlySet[n] = struct{}{}
	}
	tools := doc.Tools
	if len(onlySet) > 0 {
		filtered := tools[:0:0]
		for _, t := range tools {
			if _, ok := onlySet[t.Name]; ok {
				filtered = append(filtered, t)
			}
		}
		tools = filtered
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })

	if dryRun {
		return doImportDryRun(stdout, stderr, tools)
	}
	return doImportReal(stdout, stderr, tools, force, jsonOutput)
}

func doImportDryRun(stdout, stderr io.Writer, tools []exportEntry) error {
	if err := ensureDryRunTransactionClean(); err != nil {
		return fail(err)
	}
	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	failed := 0
	for _, t := range tools {
		switch {
		case t.Type == "local":
			fmt.Fprintln(stdout, i18n.T("skip %s: local entry cannot be reproduced", t.Name))
		case m.Get(t.Name) != nil:
			fmt.Fprintln(stdout, i18n.T("skip %s: already adopted", t.Name))
		default:
			if _, err := resolveAdoptTarget(t.Name); err != nil {
				fmt.Fprintln(stdout, i18n.T("error: %s: executable not found on PATH", t.Name))
				failed++
				continue
			}
			fmt.Fprintln(stdout, i18n.T("Would import %s (%s @ %s)", t.Name, t.Repo, t.Tag))
			if policyNeedsApply(t.UpdatePolicy) {
				fmt.Fprintln(stdout, i18n.T("  would apply policy: mode=%s channel=%s pin=%s", t.UpdatePolicy.Mode, t.UpdatePolicy.Channel, t.UpdatePolicy.PinnedTag))
			}
		}
	}
	if failed > 0 {
		return fail(i18n.Errorf("%d tool(s) not found on PATH; nothing was written", failed))
	}
	return nil
}

func doImportReal(stdout, stderr io.Writer, tools []exportEntry, force, jsonOutput bool) error {
	report := importReport{SchemaVersion: importReportSchemaVersion, Results: make([]importResult, 0, len(tools))}
	for _, t := range tools {
		res := importOne(t, force, stderr)
		report.Results = append(report.Results, res)
		switch res.Status {
		case "ok":
			report.Imported++
		case "skipped":
			report.Skipped++
		default:
			report.Failed++
		}
	}
	if jsonOutput {
		if err := output.WriteJSONValue(stdout, report); err != nil {
			return fail(err)
		}
	} else {
		tbl := output.NewTable(stdout, i18n.T("NAME"), i18n.T("STATUS"), i18n.T("DETAIL"))
		for _, r := range report.Results {
			tbl.Row(r.Name, r.Status, r.Detail)
		}
		if err := tbl.Flush(); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, i18n.T("imported=%d skipped=%d failed=%d", report.Imported, report.Skipped, report.Failed))
	}
	if report.Failed > 0 {
		return fail(i18n.Errorf("%d tool(s) failed to import", report.Failed))
	}
	return nil
}

func importOne(t exportEntry, force bool, stderr io.Writer) importResult {
	if t.Type == "local" {
		return importResult{Name: t.Name, Status: "skipped", Detail: i18n.T("local entry cannot be reproduced")}
	}
	m, err := loadManifest()
	if err != nil {
		return importResult{Name: t.Name, Status: "error", Detail: i18n.T("load manifest: %v", err)}
	}
	if m.Get(t.Name) != nil {
		return importResult{Name: t.Name, Status: "skipped", Detail: i18n.T("already adopted")}
	}
	binPath, err := resolveAdoptTarget(t.Name)
	if err != nil {
		return importResult{Name: t.Name, Status: "error", Detail: i18n.T("executable not found on PATH")}
	}
	// Never trust the exported tag blindly: the binary on this machine's PATH
	// may be a different build than on the export machine (common), and a
	// malicious list could record v999.0.0 to freeze upgrades. Re-hash the
	// real binary; on mismatch, warn and record the actual version instead.
	tag := t.Tag
	if t.SHA256 != "" {
		actualSHA, err := verify.SHA256File(binPath)
		if err != nil {
			return importResult{Name: t.Name, Status: "error", Detail: i18n.T("hash binary on PATH: %v", err)}
		}
		if actualSHA != t.SHA256 {
			tag = actualVersionTag(binPath)
			fmt.Fprintf(stderr, "%s\n", i18n.T("warning: %s: binary on PATH (sha256 %s) differs from the exported list (%s); recording actual version %q instead of tag %q", t.Name, actualSHA, t.SHA256, tag, t.Tag))
		}
	}
	var out, errBuf bytes.Buffer
	if err := doAdoptWithDeps(&out, &errBuf, binPath, t.Repo, false, tag, force, t.ArchiveExe, runSecurityGate, saveManifest); err != nil {
		fmt.Fprint(stderr, errBuf.String())
		return importResult{Name: t.Name, Status: "error", Detail: err.Error()}
	}
	if policyNeedsApply(t.UpdatePolicy) {
		options := policySetOptions{
			Mode:       t.UpdatePolicy.Mode,
			ModeSet:    t.UpdatePolicy.Mode != "",
			Channel:    t.UpdatePolicy.Channel,
			ChannelSet: t.UpdatePolicy.Channel != "",
			Pin:        t.UpdatePolicy.PinnedTag,
			PinSet:     t.UpdatePolicy.PinnedTag != "",
		}
		if err := doPolicySetWithSave(&out, &errBuf, t.Name, options, saveManifest); err != nil {
			fmt.Fprint(stderr, errBuf.String())
			return importResult{Name: t.Name, Status: "error", Detail: i18n.T("apply update policy: %v", err)}
		}
	}
	detail := t.Repo + " @ " + tag
	if policyNeedsApply(t.UpdatePolicy) {
		detail += i18n.T(" (policy reapplied)")
	}
	return importResult{Name: t.Name, Status: "ok", Detail: detail}
}

// actualVersionTag derives the honest tag for a PATH binary whose hash does
// not match the export list: the Go build info version when the binary
// carries one, else the neutral "imported" — never the mismatched list tag.
func actualVersionTag(binPath string) string {
	if goInfo, ok := provenance.ReadGoBinary(binPath); ok && goInfo.Version != "" && goInfo.Version != "(devel)" {
		return goInfo.Version
	}
	return "imported"
}

// policyNeedsApply reports whether the exported policy differs from the adopt
// default (semver/stable, no pin), which import must re-apply explicitly.
func policyNeedsApply(p exportPolicy) bool {
	return p.Mode != "" && (p.Mode != "semver" || p.Channel != "stable" || p.PinnedTag != "")
}

// maxExportDocBytes bounds the toolset list import will read; 1 MiB is
// orders of magnitude beyond any realistic list.
const maxExportDocBytes = 1 << 20

// readExportDoc loads and strictly validates a toolset-list file: the file
// must be a regular file (no symlink) within the size cap, the schema
// version must be current, unknown fields and duplicate tool names are
// rejected, and every entry's repo/tag/archive name/policy values are checked
// before anything is imported.
func readExportDoc(file string) (*exportDoc, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, i18n.Wrapf("stat toolset list: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, i18n.Errorf("toolset list is not a regular file: %s", file)
	}
	if info.Size() > maxExportDocBytes {
		return nil, i18n.Errorf("toolset list exceeds the %d-byte limit: %s", maxExportDocBytes, file)
	}
	payload, err := os.ReadFile(file)
	if err != nil {
		return nil, i18n.Wrapf("read toolset list: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.DisallowUnknownFields()
	var doc exportDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, i18n.Errorf("decode toolset list: %v", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err == nil {
		return nil, i18n.Errorf("toolset list contains trailing JSON")
	} else if err != io.EOF {
		return nil, i18n.Errorf("decode toolset list: %v", err)
	}
	if doc.SchemaVersion != exportFileSchemaVersion {
		return nil, i18n.Errorf("unsupported toolset list schema_version %d (current %d)", doc.SchemaVersion, exportFileSchemaVersion)
	}
	if len(doc.Tools) == 0 {
		return nil, i18n.Errorf("toolset list contains no tools")
	}
	seen := make(map[string]struct{}, len(doc.Tools))
	for i, t := range doc.Tools {
		if err := store.ValidateName(t.Name); err != nil {
			return nil, i18n.Errorf("tool %d: %v", i, err)
		}
		if _, dup := seen[t.Name]; dup {
			return nil, i18n.Errorf("duplicate tool %q in toolset list", t.Name)
		}
		seen[t.Name] = struct{}{}
		switch t.Type {
		case "github":
			if err := manifest.ValidateRepository(t.Repo); err != nil {
				return nil, i18n.Errorf("tool %q: %v", t.Name, err)
			}
		case "local":
		default:
			return nil, i18n.Errorf("tool %q: unknown type %q (expected github or local)", t.Name, t.Type)
		}
		if err := store.ValidateTag(t.Tag); err != nil {
			return nil, i18n.Errorf("tool %q: %v", t.Name, err)
		}
		if t.ArchiveExe != "" {
			if err := manifest.ValidateArchiveExe(t.ArchiveExe); err != nil {
				return nil, i18n.Errorf("tool %q: %v", t.Name, err)
			}
		}
		switch t.UpdatePolicy.Mode {
		case "", "semver", "github-latest":
		default:
			return nil, i18n.Errorf("tool %q: unknown update mode %q", t.Name, t.UpdatePolicy.Mode)
		}
		switch t.UpdatePolicy.Channel {
		case "", "stable", "prerelease":
		default:
			return nil, i18n.Errorf("tool %q: unknown update channel %q", t.Name, t.UpdatePolicy.Channel)
		}
	}
	if statejournal.CheckClean(dataRoot()) != nil {
		if err := statejournal.CheckClean(dataRoot()); err != nil {
			return nil, i18n.Wrapf("state may be inconsistent: %w", err)
		}
	}
	return &doc, nil
}
