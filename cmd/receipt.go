package cmd

// TOCTOU / concurrent replacement window (intentional, not a bug to "fix"):
//
// After SHA256File opens the live path — or after a store version path is
// hashed — another process may atomically replace that path (rename over).
// The hash then describes the old inode while the path name already points at
// a different file. Receipt output is a read-time snapshot of whatever was
// observed; it does not re-stat or re-open for identity after hashing, and it
// takes no lock on live paths or the store. We leave this window unfixed
// because a read-only inspection command must not acquire mutation locks or
// freeze user binaries merely to shrink a filesystem-inherent race.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

// receiptReportSchemaVersion pins the DependencyReceipt JSON surface. Bump only
// on breaking field renames/removals; additive fields may stay on the same
// version when documented.
const receiptReportSchemaVersion = 1

// Checksum status values surface the durable H1 install-time audit three-state
// as a stable snake_case string for receipts (the on-disk field remains
// checksum_verified plus optional asset evidence).
const (
	checksumStatusVerified         = "verified"
	checksumStatusUnverifiedBypass = "unverified_bypass"
	checksumStatusUnknown          = "unknown"
)

var (
	receiptJSON          bool
	receiptNoFailOnDrift bool
)

// errReceiptDrift is returned when at least one receipt reports live/manifest
// drift and --no-fail-on-drift was not set. Output is still written first.
var errReceiptDrift = i18n.Errorf("dependency receipt reports live drift")

// errReceiptErrors is returned when at least one receipt recorded store/read
// failures (e.g. Versions / ActivationSource / Original). Output is still
// written first so operators can inspect partial data. Unlike drift, this is
// not suppressed by --no-fail-on-drift.
var errReceiptErrors = i18n.Errorf("dependency receipt reports store or observation errors")

// errReceiptNotFound is returned when a requested tool name is absent from the
// manifest (or the manifest itself is missing and names were requested).
var errReceiptNotFound = i18n.Errorf("adopted tool not found")

var receiptCmd = &cobra.Command{
	Use:   "receipt [name ...]",
	Short: "Show local DependencyReceipts for adopted tools (read-only)",
	Long: `receipt aggregates a read-only DependencyReceipt for each adopted tool
from the local manifest, multi-version store, and transaction journal state.

It records the last confirmed version, the currently observed live file, live
vs manifest drift, the H1 checksum audit status, and store rollback targets.
No network access and no local writes. When any receipt reports drift the
command exits non-zero unless --no-fail-on-drift is set. Store read failures
(Versions / ActivationSource / Original) are recorded on that receipt under
errors and always exit non-zero after the report is written.`,
	Args: cobra.ArbitraryArgs,
	RunE: runReceipt,
}

func init() {
	receiptCmd.Flags().BoolVar(&receiptJSON, "json", false, "emit a stable JSON report")
	receiptCmd.Flags().BoolVar(&receiptNoFailOnDrift, "no-fail-on-drift", false, "exit 0 even when live files drift from the manifest")
	doctorCmd.AddCommand(receiptCmd)
}

func runReceipt(cmd *cobra.Command, args []string) error {
	return fail(doReceipt(cmd.OutOrStdout(), args, receiptJSON, receiptNoFailOnDrift))
}

// ReceiptReport is the stable --json envelope for `hukou doctor receipt`.
type ReceiptReport struct {
	SchemaVersion int                 `json:"schema_version"`
	Receipts      []DependencyReceipt `json:"receipts"`
}

// DependencyReceipt is the per-tool "living dependency receipt" prototype:
// last confirmed version, current observation, checksum audit status, and
// local rollback targets. Field names are snake_case and schema-stable.
type DependencyReceipt struct {
	Name            string           `json:"name"`
	Source          ReceiptSource    `json:"source"`
	AdoptedVersion  string           `json:"adopted_version"`
	CurrentObserved CurrentObserved  `json:"current_observed"`
	ChecksumStatus  string           `json:"checksum_status"`
	LastVerifiedAt  string           `json:"last_verified_at"`
	Drift           bool             `json:"drift"`
	RollbackTargets []RollbackTarget `json:"rollback_targets"`
	// Errors holds non-fatal-per-item failures (store Versions / ActivationSource
	// / Original, or incomplete rollback target hashing). Empty means success;
	// non-empty forces a non-zero command exit after the report is written.
	Errors []string `json:"errors,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// ReceiptSource describes where the adopted binary came from.
type ReceiptSource struct {
	// Type is one of: github, local, upstream.
	Type     string `json:"type"`
	Repo     string `json:"repo,omitempty"`
	URL      string `json:"url,omitempty"`
	Upstream string `json:"upstream,omitempty"`
}

// CurrentObserved is the live-path observation compared to the manifest.
type CurrentObserved struct {
	Path            string `json:"path"`
	SHA256          string `json:"sha256,omitempty"`
	ManifestSHA256  string `json:"manifest_sha256"`
	MatchesManifest bool   `json:"matches_manifest"`
	Present         bool   `json:"present"`
	Error           string `json:"error,omitempty"`
}

// RollbackTarget is one store-retained version that can be rolled back to.
type RollbackTarget struct {
	Tag    string `json:"tag"`
	SHA256 string `json:"sha256,omitempty"`
	Kind   string `json:"kind"` // "version" or "original"
}

func doReceipt(stdout io.Writer, names []string, jsonOutput, noFailOnDrift bool) error {
	report, err := collectReceiptReport(names)
	if err != nil {
		return err
	}

	if jsonOutput {
		if err := output.WriteJSONValue(stdout, report); err != nil {
			return err
		}
	} else if err := writeReceiptTable(stdout, report); err != nil {
		return err
	}

	// Store/read errors outrank drift: they always fail closed after output.
	if receiptHasErrors(report) {
		return errReceiptErrors
	}
	if !noFailOnDrift && receiptHasDrift(report) {
		return errReceiptDrift
	}
	return nil
}

// collectReceiptReport builds DependencyReceipts from local manifest/store only.
// collectReceiptReport builds DependencyReceipts from local manifest/store only;
// it performs live observation and drift detection. No stdout, no exit-code
// semantics.
func collectReceiptReport(names []string) (ReceiptReport, error) {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return ReceiptReport{}, err
	}
	m, err := loadManifest()
	if err != nil {
		return ReceiptReport{}, err
	}
	targets, err := receiptTargets(m, names)
	if err != nil {
		return ReceiptReport{}, err
	}

	s := newStore()
	report := ReceiptReport{
		SchemaVersion: receiptReportSchemaVersion,
		Receipts:      make([]DependencyReceipt, 0, len(targets)),
	}
	for _, entry := range targets {
		report.Receipts = append(report.Receipts, buildDependencyReceipt(s, entry))
	}
	sort.Slice(report.Receipts, func(i, j int) bool {
		return report.Receipts[i].Name < report.Receipts[j].Name
	})
	return report, nil
}

func receiptTargets(m *manifest.Manifest, names []string) ([]manifest.Entry, error) {
	if len(names) == 0 {
		return append([]manifest.Entry(nil), m.Entries...), nil
	}
	if len(m.Entries) == 0 {
		// Distinguish "no tools adopted / missing manifest" from a typo among
		// an existing registry so operators can tell empty state from a miss.
		return nil, i18n.Wrapf("%w: no adopted tools in manifest (missing or empty); requested %q", errReceiptNotFound, names[0])
	}
	targets := make([]manifest.Entry, 0, len(names))
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, duplicate := seen[name]; duplicate {
			continue
		}
		seen[name] = struct{}{}
		entry := m.Get(name)
		if entry == nil {
			return nil, i18n.Wrapf("%w: %q", errReceiptNotFound, name)
		}
		targets = append(targets, *entry)
	}
	return targets, nil
}

func buildDependencyReceipt(s *store.Store, entry manifest.Entry) DependencyReceipt {
	receipt := DependencyReceipt{
		Name:            entry.Name,
		Source:          receiptSource(entry),
		AdoptedVersion:  entry.Tag,
		ChecksumStatus:  receiptChecksumStatus(entry),
		LastVerifiedAt:  receiptLastVerifiedAt(entry),
		RollbackTargets: make([]RollbackTarget, 0),
		Errors:          make([]string, 0),
	}

	observed := CurrentObserved{
		Path:           entry.Path,
		ManifestSHA256: strings.ToLower(entry.SHA256),
	}
	liveSHA, err := store.SHA256File(entry.Path)
	if err != nil {
		observed.Present = false
		observed.MatchesManifest = false
		observed.Error = err.Error()
		receipt.Drift = true
		receipt.Note = "live file missing or unreadable"
	} else {
		observed.Present = true
		observed.SHA256 = liveSHA
		observed.MatchesManifest = strings.EqualFold(liveSHA, entry.SHA256)
		receipt.Drift = !observed.MatchesManifest
		if receipt.Drift {
			receipt.Note = "live file sha256 does not match manifest"
		}
	}
	receipt.CurrentObserved = observed
	targets, storeErrs := collectRollbackTargets(s, entry)
	receipt.RollbackTargets = targets
	if len(storeErrs) > 0 {
		receipt.Errors = append(receipt.Errors, storeErrs...)
		if receipt.Note == "" {
			receipt.Note = "store read errors while collecting rollback targets"
		}
	}
	return receipt
}

func receiptSource(entry manifest.Entry) ReceiptSource {
	if entry.Repo != "" {
		return ReceiptSource{
			Type: "github",
			Repo: entry.Repo,
			URL:  "https://github.com/" + entry.Repo,
		}
	}
	if entry.Upstream != "" {
		return ReceiptSource{
			Type:     "upstream",
			Upstream: entry.Upstream,
		}
	}
	return ReceiptSource{Type: "local"}
}

// receiptChecksumStatus maps durable H1 audit fields onto a stable three-state
// string. verified = publisher checksum passed; unverified_bypass = asset was
// installed without publisher success (explicit bypass or cleared post-rollback
// is distinguished by presence of asset evidence); unknown = local/legacy with
// no release-asset audit trail.
func receiptChecksumStatus(entry manifest.Entry) string {
	if entry.ChecksumVerified {
		return checksumStatusVerified
	}
	if entry.AssetName != "" || entry.AssetSHA256 != "" || entry.ChecksumAsset != "" {
		return checksumStatusUnverifiedBypass
	}
	return checksumStatusUnknown
}

func receiptLastVerifiedAt(entry manifest.Entry) string {
	if !entry.ChecksumVerified {
		return ""
	}
	// UpdatedAt is rewritten on every successful mutation that leaves the
	// entry verified; it is the best durable "last confirmation" timestamp
	// without scanning activation history for a field we do not yet store.
	return entry.UpdatedAt
}

// collectRollbackTargets lists store-retained rollback destinations and
// surfaces any Versions / ActivationSource / Original (or hash) failures so
// empty targets are distinguishable from store damage.
func collectRollbackTargets(s *store.Store, entry manifest.Entry) ([]RollbackTarget, []string) {
	targets := make([]RollbackTarget, 0)
	errs := make([]string, 0)

	versions, err := s.Versions(entry.Name)
	if err != nil {
		errs = append(errs, fmt.Sprintf("list store versions: %v", err))
	} else {
		for _, tag := range versions {
			if tag == entry.Tag {
				// Active tag is not a rollback destination.
				continue
			}
			target := RollbackTarget{Tag: tag, Kind: "version"}
			src, srcErr := s.ActivationSource(entry.Name, tag)
			if srcErr != nil {
				errs = append(errs, fmt.Sprintf("activation source %s@%s: %v", entry.Name, tag, srcErr))
			} else if sha, shaErr := store.SHA256File(src); shaErr != nil {
				errs = append(errs, fmt.Sprintf("hash store version %s@%s: %v", entry.Name, tag, shaErr))
			} else {
				target.SHA256 = sha
			}
			targets = append(targets, target)
		}
	}

	original, err := s.Original(entry.Name)
	if err != nil {
		errs = append(errs, fmt.Sprintf("inspect original backup: %v", err))
	} else if entry.Tag != "original" {
		targets = append(targets, RollbackTarget{
			Tag:    "original",
			SHA256: original.SHA256,
			Kind:   "original",
		})
	}

	// Retained release versions first (lexicographic tag), then original.
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			if targets[i].Kind == "version" {
				return true
			}
			if targets[j].Kind == "version" {
				return false
			}
		}
		return targets[i].Tag < targets[j].Tag
	})
	return targets, errs
}

func receiptHasDrift(report ReceiptReport) bool {
	for _, r := range report.Receipts {
		if r.Drift {
			return true
		}
	}
	return false
}

func receiptHasErrors(report ReceiptReport) bool {
	for _, r := range report.Receipts {
		if len(r.Errors) > 0 {
			return true
		}
	}
	return false
}

func writeReceiptTable(stdout io.Writer, report ReceiptReport) error {
	if len(report.Receipts) == 0 {
		_, err := fmt.Fprintln(stdout, i18n.T("No tools have been adopted. Start with `hukou adopt <name|path>`."))
		return err
	}
	t := output.NewTable(stdout,
		i18n.T("NAME"), i18n.T("VERSION"), i18n.T("SOURCE"), i18n.T("CHECKSUM"), i18n.T("DRIFT"),
		i18n.T("ERRORS"), i18n.T("LIVE SHA"), i18n.T("MANIFEST SHA"), i18n.T("ROLLBACK"), i18n.T("LAST VERIFIED"),
	)
	for _, r := range report.Receipts {
		source := r.Source.Type
		switch {
		case r.Source.Repo != "":
			source = r.Source.Repo
		case r.Source.Upstream != "":
			source = r.Source.Upstream
		}
		drift := i18n.T("no")
		if r.Drift {
			drift = i18n.T("yes")
		}
		errs := "-"
		if len(r.Errors) > 0 {
			errs = i18n.T("yes") + ":" + strings.Join(r.Errors, "; ")
		}
		live := r.CurrentObserved.SHA256
		if live == "" {
			live = "-"
		}
		manifestSHA := r.CurrentObserved.ManifestSHA256
		if manifestSHA == "" {
			manifestSHA = "-"
		}
		lastVerified := r.LastVerifiedAt
		if lastVerified == "" {
			lastVerified = "-"
		}
		rollback := formatRollbackSummary(r.RollbackTargets)
		t.Row(
			r.Name,
			r.AdoptedVersion,
			source,
			r.ChecksumStatus,
			drift,
			errs,
			shortSHA(live),
			shortSHA(manifestSHA),
			rollback,
			lastVerified,
		)
	}
	return t.Flush()
}

func formatRollbackSummary(targets []RollbackTarget) string {
	if len(targets) == 0 {
		return "-"
	}
	tags := make([]string, 0, len(targets))
	for _, t := range targets {
		tags = append(tags, t.Tag)
	}
	return strings.Join(tags, ",")
}

func shortSHA(sha string) string {
	if sha == "-" || len(sha) < 12 {
		return sha
	}
	return sha[:12]
}
