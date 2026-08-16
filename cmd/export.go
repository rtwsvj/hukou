package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/manifest"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

// exportFileSchemaVersion pins the portable toolset-list JSON emitted by
// `hukou export`. Bump only on breaking field changes.
const exportFileSchemaVersion = 1

var exportOutput string

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the adopted toolset as a portable list",
	Long: `export writes a portable, machine-readable list of every adopted tool
(its repository, tag, archive-internal name, SHA-256, and update policy) so
the same set can be re-registered on another machine with "hukou import".

The list is a data file: it always renders as JSON (to stdout, or to --output
FILE with 0600 permissions). Export is read-only: it takes no lock, writes
nothing except the explicitly requested file, and makes no network request.
Entries without a repository (local adoptions) are included for the record but
cannot be reproduced by import, which skips them with a warning.`,
	Args: cobra.NoArgs,
	RunE: runExport,
}

func init() {
	exportCmd.Flags().StringVar(&exportOutput, "output", "", "write the toolset list to FILE with 0600 permissions (default: stdout)")
	rootCmd.AddCommand(exportCmd)
}

func runExport(cmd *cobra.Command, _ []string) error {
	return doExport(cmd.OutOrStdout(), exportOutput)
}

// exportDoc is the portable toolset-list document.
type exportDoc struct {
	SchemaVersion int           `json:"schema_version"`
	ExportedAt    string        `json:"exported_at"`
	Tools         []exportEntry `json:"tools"`
}

// exportEntry records one adopted tool with the metadata import needs.
type exportEntry struct {
	Name          string       `json:"name"`
	Type          string       `json:"type"` // "github" | "local"
	Repo          string       `json:"repo,omitempty"`
	Tag           string       `json:"tag"`
	SHA256        string       `json:"sha256"`
	AdoptedSHA256 string       `json:"adopted_sha256,omitempty"`
	ArchiveExe    string       `json:"archive_exe,omitempty"`
	UpdatePolicy  exportPolicy `json:"update_policy"`
	AdoptedAt     string       `json:"adopted_at,omitempty"`
}

// exportPolicy carries the update policy import re-applies when it is not the
// adopt default (semver/stable).
type exportPolicy struct {
	Mode      string `json:"mode"`
	Channel   string `json:"channel"`
	PinnedTag string `json:"pinned_tag,omitempty"`
}

func doExport(stdout io.Writer, output string) error {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fail(i18n.Wrapf("state may be inconsistent: %w", err))
	}
	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	if len(m.Entries) == 0 {
		return fail(i18n.Errorf("no adopted tools to export"))
	}
	doc := exportDoc{
		SchemaVersion: exportFileSchemaVersion,
		ExportedAt:    rfc3339Now(),
		Tools:         make([]exportEntry, 0, len(m.Entries)),
	}
	for _, e := range m.Entries {
		entry := exportEntry{
			Name:          e.Name,
			Tag:           e.Tag,
			SHA256:        e.SHA256,
			AdoptedSHA256: e.AdoptedSHA256,
			ArchiveExe:    e.ArchiveExe,
			AdoptedAt:     e.AdoptedAt,
			UpdatePolicy: exportPolicy{
				Mode:      string(e.UpdatePolicy.Mode),
				Channel:   string(e.UpdatePolicy.Channel),
				PinnedTag: e.UpdatePolicy.PinnedTag,
			},
		}
		if e.Repo != "" {
			entry.Type = "github"
			entry.Repo = e.Repo
		} else {
			entry.Type = "local"
		}
		doc.Tools = append(doc.Tools, entry)
	}
	sort.Slice(doc.Tools, func(i, j int) bool { return doc.Tools[i].Name < doc.Tools[j].Name })

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if output == "" {
		return enc.Encode(doc)
	}
	payload, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fail(i18n.Wrapf("encode toolset list: %w", err))
	}
	payload = append(payload, '\n')
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return fail(i18n.Wrapf("create toolset list directory: %w", err))
	}
	if err := os.WriteFile(output, payload, 0o600); err != nil {
		return fail(i18n.Wrapf("write toolset list: %w", err))
	}
	_, err = fmt.Fprintf(stdout, "%s\n", i18n.T("Toolset list written: %s", output))
	return err
}

var _ = manifest.DefaultRetentionPolicy // keep the manifest import honest
