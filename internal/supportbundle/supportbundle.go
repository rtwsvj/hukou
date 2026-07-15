// Package supportbundle builds a privacy-preserving, offline diagnostic
// summary. It never includes raw paths, repository identifiers, environment
// variables, usernames, WAL contents, or binary contents.
package supportbundle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"

	"github.com/rtwsvj/hukou/internal/doctor"
	"github.com/rtwsvj/hukou/internal/durablefs"
	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

const (
	ReportSchemaVersion     = 1
	maxManifestSummaryBytes = 16 << 20
)

// Build identifies the hukou executable without consulting the network or
// environment. Values normally come from internal/buildinfo.
type Build struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Date    string `json:"date"`
}

type Platform struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type DoctorFinding struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Scope    string `json:"scope"`
}

type DoctorSummary struct {
	Status   string          `json:"status"`
	Complete bool            `json:"complete"`
	Errors   int             `json:"errors"`
	Warnings int             `json:"warnings"`
	Info     int             `json:"info"`
	Findings []DoctorFinding `json:"findings"`
}

type PolicySummary struct {
	Mode          string `json:"mode"`
	Channel       string `json:"channel"`
	Pinned        bool   `json:"pinned"`
	RollbackDepth int    `json:"rollback_depth"`
}

type OperationCount struct {
	Operation string `json:"operation"`
	Count     int    `json:"count"`
}

type HistorySummary struct {
	EventCount int              `json:"event_count"`
	Operations []OperationCount `json:"operations"`
}

// EntrySummary intentionally excludes names, tags, Repo, Path, upstream URLs,
// asset names, event IDs, and hashes derived from paths. Entry is a stable
// ordinal within this report, not a value copied from the manifest.
type EntrySummary struct {
	Entry   string         `json:"entry"`
	Policy  PolicySummary  `json:"policy"`
	History HistorySummary `json:"history"`
}

type ManifestSummary struct {
	Status        string         `json:"status"`
	SchemaVersion int            `json:"schema_version,omitempty"`
	EntryCount    int            `json:"entry_count"`
	Entries       []EntrySummary `json:"entries"`
}

type TransactionTopology struct {
	Status      string `json:"status"`
	Building    int    `json:"building"`
	Pending     int    `json:"pending"`
	Completed   int    `json:"completed"`
	Unknown     int    `json:"unknown"`
	Quarantined int    `json:"quarantined"`
}

type StoreTopology struct {
	Status             string `json:"status"`
	ToolDirectories    int    `json:"tool_directories"`
	VersionDirectories int    `json:"version_directories"`
	ArtifactFiles      int    `json:"artifact_files"`
	TemporaryEntries   int    `json:"temporary_entries"`
	Symlinks           int    `json:"symlinks"`
	OtherNodes         int    `json:"other_nodes"`
}

type Report struct {
	SchemaVersion int                 `json:"schema_version"`
	Build         Build               `json:"build"`
	Platform      Platform            `json:"platform"`
	Doctor        DoctorSummary       `json:"doctor"`
	Manifest      ManifestSummary     `json:"manifest"`
	Transactions  TransactionTopology `json:"transactions"`
	Store         StoreTopology       `json:"store"`
}

// Collect performs read-only local inspection. It does not acquire a lock,
// create the data root, read WAL payloads, inspect environment variables, or
// make a network request.
func Collect(dataRoot string, build Build) Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Build: Build{
			Version: safeBuildValue(build.Version),
			Commit:  safeBuildValue(build.Commit),
			Date:    safeBuildValue(build.Date),
		},
		Platform:     Platform{OS: runtime.GOOS, Arch: runtime.GOARCH},
		Doctor:       collectDoctor(dataRoot),
		Manifest:     collectManifest(dataRoot),
		Transactions: collectTransactions(dataRoot),
		Store:        collectStore(dataRoot),
	}
}

func collectDoctor(dataRoot string) DoctorSummary {
	report := doctor.Scan(doctor.Options{DataRoot: dataRoot})
	summary := DoctorSummary{
		Status:   string(report.Status),
		Complete: report.Complete,
		Errors:   report.Summary.Errors,
		Warnings: report.Summary.Warnings,
		Info:     report.Summary.Info,
		Findings: make([]DoctorFinding, 0, len(report.Findings)),
	}
	for _, finding := range report.Findings {
		summary.Findings = append(summary.Findings, DoctorFinding{
			Code:     finding.Code,
			Severity: string(finding.Severity),
			Scope:    finding.Scope,
		})
	}
	sort.Slice(summary.Findings, func(i, j int) bool {
		a, b := summary.Findings[i], summary.Findings[j]
		if a.Severity != b.Severity {
			return a.Severity < b.Severity
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Scope < b.Scope
	})
	return summary
}

func collectManifest(dataRoot string) ManifestSummary {
	summary := ManifestSummary{Status: "missing", Entries: make([]EntrySummary, 0)}
	path := filepath.Join(dataRoot, "manifest.json")
	info, err := os.Lstat(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			summary.Status = "unreadable"
		}
		return summary
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		summary.Status = "invalid"
		return summary
	}
	if info.Size() > maxManifestSummaryBytes {
		summary.Status = "invalid"
		return summary
	}
	m, err := manifest.Load(path)
	if err != nil {
		summary.Status = "invalid"
		return summary
	}
	summary.Status = "valid"
	summary.SchemaVersion = m.SchemaVersion
	entries := append([]manifest.Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	for index, entry := range entries {
		rollbackDepth := m.Retention.RollbackDepth
		if entry.Retention != nil {
			rollbackDepth = entry.Retention.RollbackDepth
		}
		operations := make(map[string]int)
		for _, event := range entry.Activations {
			operations[safeOperation(event.Operation)]++
		}
		operationNames := make([]string, 0, len(operations))
		for operation := range operations {
			operationNames = append(operationNames, operation)
		}
		sort.Strings(operationNames)
		counts := make([]OperationCount, 0, len(operationNames))
		for _, operation := range operationNames {
			counts = append(counts, OperationCount{Operation: operation, Count: operations[operation]})
		}
		summary.Entries = append(summary.Entries, EntrySummary{
			Entry: fmt.Sprintf("entry-%03d", index+1),
			Policy: PolicySummary{
				Mode:          safePolicyMode(string(entry.UpdatePolicy.Mode)),
				Channel:       safePolicyChannel(string(entry.UpdatePolicy.Channel)),
				Pinned:        entry.UpdatePolicy.PinnedTag != "",
				RollbackDepth: rollbackDepth,
			},
			History: HistorySummary{EventCount: len(entry.Activations), Operations: counts},
		})
	}
	summary.EntryCount = len(summary.Entries)
	return summary
}

func collectTransactions(dataRoot string) TransactionTopology {
	status, err := statejournal.Inspect(dataRoot)
	if err != nil {
		return TransactionTopology{Status: "unreadable"}
	}
	return TransactionTopology{
		Status:      "ok",
		Building:    len(status.Building),
		Pending:     len(status.Pending),
		Completed:   len(status.Completed),
		Unknown:     len(status.Unknown),
		Quarantined: len(status.Quarantined),
	}
}

func collectStore(dataRoot string) StoreTopology {
	result := StoreTopology{Status: "missing"}
	root := filepath.Join(dataRoot, "store")
	info, err := os.Lstat(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			result.Status = "unreadable"
		}
		return result
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		result.Status = "invalid"
		return result
	}
	result.Status = "ok"
	tools, err := os.ReadDir(root)
	if err != nil {
		result.Status = "unreadable"
		return result
	}
	for _, tool := range tools {
		toolPath := filepath.Join(root, tool.Name())
		toolInfo, err := os.Lstat(toolPath)
		if err != nil {
			result.Status = "incomplete"
			continue
		}
		if tool.Name() == ".tmp" {
			if toolInfo.Mode()&os.ModeSymlink != 0 || !toolInfo.IsDir() {
				countNode(toolInfo, &result)
				continue
			}
			tmp, err := os.ReadDir(toolPath)
			if err != nil {
				result.Status = "incomplete"
				continue
			}
			result.TemporaryEntries += len(tmp)
			continue
		}
		if toolInfo.Mode()&os.ModeSymlink != 0 || !toolInfo.IsDir() {
			countNode(toolInfo, &result)
			continue
		}
		result.ToolDirectories++
		versions, err := os.ReadDir(toolPath)
		if err != nil {
			result.Status = "incomplete"
			continue
		}
		for _, version := range versions {
			versionPath := filepath.Join(toolPath, version.Name())
			versionInfo, err := os.Lstat(versionPath)
			if err != nil {
				result.Status = "incomplete"
				continue
			}
			if versionInfo.Mode()&os.ModeSymlink != 0 || !versionInfo.IsDir() {
				countNode(versionInfo, &result)
				continue
			}
			result.VersionDirectories++
			artifacts, err := os.ReadDir(versionPath)
			if err != nil {
				result.Status = "incomplete"
				continue
			}
			for _, artifact := range artifacts {
				artifactInfo, err := os.Lstat(filepath.Join(versionPath, artifact.Name()))
				if err != nil {
					result.Status = "incomplete"
					continue
				}
				if artifactInfo.Mode().IsRegular() {
					result.ArtifactFiles++
				} else {
					countNode(artifactInfo, &result)
				}
			}
		}
	}
	return result
}

func countNode(info os.FileInfo, result *StoreTopology) {
	if info.Mode()&os.ModeSymlink != 0 {
		result.Symlinks++
		return
	}
	result.OtherNodes++
}

func safeOperation(value string) string {
	switch value {
	case "legacy", "adopt", "upgrade", "rollback", "repair":
		return value
	default:
		return "other"
	}
}

func safePolicyMode(value string) string {
	switch value {
	case "semver", "github-latest":
		return value
	default:
		return "invalid"
	}
}

func safePolicyChannel(value string) string {
	switch value {
	case "stable", "prerelease":
		return value
	default:
		return "invalid"
	}
}

func safeBuildValue(value string) string {
	if len(value) > 128 || strings.ContainsAny(value, `/\\`) {
		return "redacted"
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return "redacted"
		}
	}
	return value
}

func WriteJSON(w io.Writer, report Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// WriteFile writes only the explicitly requested output path and forces 0600
// permissions. It refuses to replace a symlink or non-regular node.
func WriteFile(path string, report Report) error {
	if path == "" {
		return fmt.Errorf("support bundle output path is required")
	}
	parent := filepath.Dir(filepath.Clean(path))
	parentInfo, err := os.Stat(parent)
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() {
		return fmt.Errorf("support bundle output parent is not a directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("support bundle output must be a regular file or missing")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var buf bytes.Buffer
	if err := WriteJSON(&buf, report); err != nil {
		return err
	}
	return durablefs.AtomicWriteFile(path, buf.Bytes(), 0o600)
}
