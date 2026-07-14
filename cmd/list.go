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
	Short: "列出已收编工具",
	Long:  "以表格形式输出户口清单：名称、当前 tag、repo、路径、store 版本数。",
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
		fmt.Fprintln(stdout, "还没有收编任何工具。使用 `hukou adopt <name|path>` 开始。")
		return nil
	}

	s := newStore()
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tTAG\tREPO\tPATH\tVERSIONS")

	sort.Slice(m.Entries, func(i, j int) bool { return m.Entries[i].Name < m.Entries[j].Name })

	for _, e := range m.Entries {
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
