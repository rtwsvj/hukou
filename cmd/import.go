package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/rtwsvj/hukou/internal/verify"
	"github.com/rtwsvj/hukou/internal/versionpolicy"
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
default.

The exported tag is advisory only: import records the binary's honest local
version (from its Go build information) whenever one can be derived, and
replaces an exported semantic version tag with the neutral "imported"
sentinel when the binary carries no usable build metadata — offline import
cannot bind a digest to a release, so untrusted semantic tags are never
recorded. Non-semantic exported tags survive as advisory labels with an
explicit unverified-provenance warning.
--dry-run prints the whole plan and writes nothing; --only restricts
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
	// Never trust the exported tag blindly: import is documented offline, so
	// no release lookup can confirm that the recorded (tag, sha256) pair is
	// real. The digest alone is not provenance — a malicious list can pin
	// v999.0.0 to freeze upgrades while shipping the matching digest of the
	// binary it knows this machine runs. Import therefore records the honest
	// local version derived from the binary itself and treats the exported
	// tag as advisory:
	//   - when the binary carries a REAL Go build version, that version wins;
	//     if both it and the exported tag are clean semantic versions and they
	//     disagree, the actual version is recorded with a warning naming both;
	//   - otherwise (non-Go or "(devel)" builds) fall back to the exported
	//     tag only when its digest matches the binary, with an explicit
	//     unverified-provenance warning.
	actualSHA, err := verify.SHA256File(binPath)
	if err != nil {
		return importResult{Name: t.Name, Status: "error", Detail: i18n.T("hash binary on PATH: %v", err)}
	}
	tag, warn := resolveImportTag(t, binPath, actualSHA)
	if warn != "" {
		fmt.Fprintf(stderr, "%s\n", warn)
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

// actualVersionTag derives the honest version for a binary: the Go build info
// version when the binary carries a real one (not "(devel)"), else "".
func actualVersionTag(binPath string) string {
	if goInfo, ok := provenance.ReadGoBinary(binPath); ok && goInfo.Version != "" && goInfo.Version != "(devel)" {
		return goInfo.Version
	}
	return ""
}

// resolveImportTag decides which tag the manifest records for an imported
// tool and returns any provenance warning to print. Import is offline, so
// the exported tag is advisory: whenever the binary yields a real Go build
// version, that actual version wins. When no actual version can be derived
// (non-Go or "(devel)" builds), an exported SEMANTIC tag is untrusted
// provenance and is replaced by the neutral "imported" sentinel — recording
// "v999.0.0" would freeze the tool's upgrades forever. Only non-semantic
// exported tags survive, as advisory labels with an explicit warning.
func resolveImportTag(t exportEntry, binPath, actualSHA string) (tag string, warn string) {
	actual := actualVersionTag(binPath)
	if actual != "" {
		if t.Tag != actual && versionpolicy.IsSemanticTag(t.Tag) && versionpolicy.IsSemanticTag(actual) {
			return actual, i18n.T("warning: %s: exported tag %q and the binary's actual version %q are both semantic versions but disagree; recording the actual version %q", t.Name, t.Tag, actual, actual)
		}
		if t.Tag == actual {
			return actual, ""
		}
		if !strings.EqualFold(actualSHA, t.SHA256) {
			return actual, i18n.T("warning: %s: binary on PATH (sha256 %s) differs from the exported list (%s); recording actual version %q instead of tag %q", t.Name, actualSHA, t.SHA256, actual, t.Tag)
		}
		// Digest matches but the build info carries a different spelling of
		// the same or a non-semantic exported tag: the actual Go version is
		// still the more honest record when they differ textually.
		return actual, i18n.T("warning: %s: exported tag %q could not be verified offline; recording the binary's actual version %q", t.Name, t.Tag, actual)
	}
	// No derivable actual version (non-Go or "(devel)" builds). A digest
	// match proves which bytes are on PATH, not which upstream tag published
	// them, so a semver-looking exported tag is still untrusted provenance —
	// recording "v999.0.0" would freeze the tool's upgrades forever. Such
	// tags are replaced by the neutral "imported" sentinel; only
	// non-semantic exported tags survive as advisory labels, and never
	// silently.
	if versionpolicy.IsSemanticTag(t.Tag) {
		return "imported", i18n.T("warning: %s: binary carries no usable build metadata; untrusted semantic tag %q replaced by the neutral sentinel imported (offline import cannot bind a digest to a release)", t.Name, t.Tag)
	}
	return t.Tag, i18n.T("warning: %s: exported tag %q could not be verified offline; recording it as an advisory label only", t.Name, t.Tag)
}

// policyNeedsApply reports whether the exported policy differs from the adopt
// default (semver/stable, no pin), which import must re-apply explicitly.
func policyNeedsApply(p exportPolicy) bool {
	return p.Mode != "" && (p.Mode != "semver" || p.Channel != "stable" || p.PinnedTag != "")
}

// isValidDigest reports whether value is exactly 64 hexadecimal characters —
// the canonical SHA-256 digest shape hukou persists. The manifest package
// keeps an equivalent unexported check for entry validation; import re-checks
// the exported list here so a missing or malformed digest is refused before
// anything reaches the adopt path.
func isValidDigest(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

// maxExportDocBytes bounds the toolset list import will read; 1 MiB is
// orders of magnitude beyond any realistic list.
const maxExportDocBytes = 1 << 20

// readExportDoc loads and strictly validates a toolset-list file: the file
// must be a regular file (no symlink) within the size cap, the schema
// version must be current, unknown fields and duplicate tool names are
// rejected, and every entry's repo/tag/archive name/digest/policy values are
// checked before anything is imported. A github entry must carry a
// well-formed sha256 (64 hex characters) so the empty-digest trust bypass is
// structurally impossible; adopted_sha256 and pinned_tag are validated when
// present.
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
			if !isValidDigest(t.SHA256) {
				return nil, i18n.Errorf("tool %q: sha256 must be a 64-hex-character SHA-256 digest", t.Name)
			}
			if t.AdoptedSHA256 != "" && !isValidDigest(t.AdoptedSHA256) {
				return nil, i18n.Errorf("tool %q: adopted_sha256 must be a 64-hex-character SHA-256 digest", t.Name)
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
		if t.UpdatePolicy.PinnedTag != "" {
			if err := store.ValidateTag(t.UpdatePolicy.PinnedTag); err != nil {
				return nil, i18n.Errorf("tool %q: %v", t.Name, err)
			}
		}
	}
	if statejournal.CheckClean(dataRoot()) != nil {
		if err := statejournal.CheckClean(dataRoot()); err != nil {
			return nil, i18n.Wrapf("state may be inconsistent: %w", err)
		}
	}
	return &doc, nil
}
