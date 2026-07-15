package cmd

import (
	"fmt"
	"io"
	"sort"
	"text/tabwriter"

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
		return fail(fmt.Errorf("state may be inconsistent: %w", err))
	}
	m, err := loadManifest()
	if err != nil {
		return fail(err)
	}
	if len(m.Entries) == 0 {
		fmt.Fprintln(stdout, "No tools have been adopted. Start with `hukou adopt <name|path>`.")
		return nil
	}

	s := newStore()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTAG\tREPO\tPATH\tVERSIONS")

	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Name < m.Entries[j].Name })

	for _, e := range m.Entries {
		if _, err := s.Original(e.Name); err != nil {
			return fail(fmt.Errorf("inspect original backup for %s: %w", e.Name, err))
		}
		versions, err := s.Versions(e.Name)
		if err != nil {
			return fail(fmt.Errorf("inspect store versions for %s: %w", e.Name, err))
		}
		repo := e.Repo
		if repo == "" {
			repo = "(local)"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", e.Name, e.Tag, repo, e.Path, len(versions))
	}
	return tw.Flush()
}
