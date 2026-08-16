package cmd

import (
	"fmt"
	"io"
	"sort"

	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/output"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List adopted tools",
	Long:  "List each adopted tool, its active tag, repository, path, and retained-version count.",
	Args:  cobra.NoArgs,
	RunE:  runList,
}

func init() {
	rootCmd.AddCommand(listCmd)
}

func runList(cmd *cobra.Command, _ []string) error {
	return doList(cmd.OutOrStdout())
}

func doList(stdout io.Writer) error {
	if err := statejournal.CheckClean(dataRoot()); err != nil {
		return fail(i18n.Wrapf("state may be inconsistent: %w", err))
	}
	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	if len(m.Entries) == 0 {
		fmt.Fprintln(stdout, i18n.T("No tools have been adopted. Start with `hukou adopt <name|path>`."))
		return nil
	}

	s := newStore()
	t := output.NewTable(stdout,
		i18n.T("NAME"), i18n.T("TAG"), i18n.T("REPO"), i18n.T("PATH"), i18n.T("VERSIONS"),
	)

	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Name < m.Entries[j].Name })

	for _, e := range m.Entries {
		if _, err := s.Original(e.Name); err != nil {
			return fail(i18n.Wrapf("inspect original backup for %s: %w", err, e.Name))
		}
		versions, err := s.Versions(e.Name)
		if err != nil {
			return fail(i18n.Wrapf("inspect store versions for %s: %w", err, e.Name))
		}
		repo := e.Repo
		if repo == "" {
			repo = "(local)"
		}
		t.Row(e.Name, e.Tag, repo, e.Path, fmt.Sprintf("%d", len(versions)))
	}
	return t.Flush()
}
