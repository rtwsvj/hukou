package cmd

import (
	"io"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/spf13/cobra"
)

// suggestReportSchemaVersion pins the --json envelope of `hukou suggest`.
const suggestReportSchemaVersion = 1

var suggestJSON bool

var suggestCmd = &cobra.Command{
	Use:   "suggest <name|path>",
	Short: "Suggest GitHub repositories for an unmanaged binary (read-only)",
	Long: `suggest searches GitHub for repositories whose released binaries could
be the origin of the given executable, ranked by exact-name match, name
containment, description hits, then stars, and prints ready-to-run "hukou
adopt" commands carrying each candidate's latest release tag.

It is strictly read-only: no data directory, no lock, no writes of any kind.
The only network calls are one GitHub repository search plus a latest-release
lookup for the top candidates. A suggestion is never applied automatically —
you re-run adopt yourself with the repository you choose.`,
	Args: cobra.ExactArgs(1),
	RunE: runSuggest,
}

func init() {
	suggestCmd.Flags().BoolVar(&suggestJSON, "json", false, "emit a stable JSON report")
	rootCmd.AddCommand(suggestCmd)
}

func runSuggest(cmd *cobra.Command, args []string) error {
	client := ghrelease.New(firstEnv("GITHUB_TOKEN", "GH_TOKEN"))
	return doSuggest(cmd.OutOrStdout(), args[0], client, suggestJSON)
}

// suggestReport is the stable --json envelope.
type suggestReport struct {
	SchemaVersion int                `json:"schema_version"`
	Query         string             `json:"query"`
	Name          string             `json:"name"`
	Candidates    []suggestCandidate `json:"candidates"`
}

// suggestCandidate is one repository suggestion.
type suggestCandidate struct {
	Repo          string `json:"repo"`
	Stars         int    `json:"stars"`
	Description   string `json:"description,omitempty"`
	Archived      bool   `json:"archived"`
	Tag           string `json:"tag,omitempty"`
	TagPrerelease bool   `json:"tag_prerelease,omitempty"`
}

// suggestClient is the network seam: search + latest-release lookup.
type suggestClient interface {
	SearchRepositories(query string, limit int) ([]ghrelease.SearchRepoItem, error)
	Latest(owner, repo string) (ghrelease.Release, error)
}

func doSuggest(stdout io.Writer, target string, client suggestClient, jsonOutput bool) error {
	binPath, err := resolveAdoptTarget(target)
	if err != nil {
		return fail(i18n.Wrapf("locate target: %w", err))
	}
	name := filepath.Base(binPath)
	query := name + " in:name"
	hits, err := client.SearchRepositories(query, 10)
	if err != nil {
		return fail(i18n.Wrapf("search GitHub repositories: %w", err))
	}
	report := suggestReport{
		SchemaVersion: suggestReportSchemaVersion,
		Query:         query,
		Name:          name,
		Candidates:    make([]suggestCandidate, 0, len(hits)),
	}
	// Rank by how the name matched, then stars: exact repository name first,
	// then the name as a substring of the repository name, then description
	// hits, then everything else. Heuristics stay advisory: a renamed local
	// binary may never surface its true origin, which is exactly why a
	// suggestion is never applied automatically.
	matchTier := func(item ghrelease.SearchRepoItem) int {
		part := repoPart(item.FullName)
		switch {
		case part == name:
			return 0
		case strings.Contains(part, name):
			return 1
		case strings.Contains(strings.ToLower(item.Description), strings.ToLower(name)):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		ti, tj := matchTier(hits[i]), matchTier(hits[j])
		if ti != tj {
			return ti < tj
		}
		return hits[i].StargazersCount > hits[j].StargazersCount
	})
	exactFound := len(hits) > 0 && matchTier(hits[0]) == 0
	if len(hits) > 5 {
		hits = hits[:5]
	}
	for _, h := range hits {
		owner, repo, ok := splitRepo(h.FullName)
		if !ok {
			continue
		}
		cand := suggestCandidate{
			Repo:        h.FullName,
			Stars:       h.StargazersCount,
			Description: firstLine(h.Description),
			Archived:    h.Archived,
		}
		// A failed latest-release lookup still leaves a useful suggestion;
		// it just carries no tag.
		if rel, err := client.Latest(owner, repo); err == nil {
			cand.Tag = rel.TagName
			cand.TagPrerelease = rel.Prerelease
		}
		report.Candidates = append(report.Candidates, cand)
	}
	if jsonOutput {
		return output.WriteJSONValue(stdout, report)
	}
	if len(report.Candidates) == 0 {
		_, err := io.WriteString(stdout, i18n.T("no repository candidates found for %q", name)+"\n")
		return err
	}
	if !exactFound {
		if _, err := io.WriteString(stdout, i18n.T("no repository with an exact name match; showing related candidates only")+"\n"); err != nil {
			return err
		}
	}
	tbl := output.NewTable(stdout, i18n.T("REPO"), i18n.T("STARS"), i18n.T("TAG"), i18n.T("DESCRIPTION"))
	for _, c := range report.Candidates {
		tag := c.Tag
		if tag == "" {
			tag = "-"
		} else if c.TagPrerelease {
			tag += i18n.T(" (prerelease)")
		}
		if c.Archived {
			tag += i18n.T(" [archived]")
		}
		// Repo, tag, and description are GitHub-API-controlled text: sanitize
		// before they reach the terminal (table cells and the copyable adopt
		// command alike).
		tbl.Row(output.SanitizeField(c.Repo), i18n.T("%d", c.Stars), output.SanitizeField(tag), output.SanitizeField(c.Description))
	}
	if err := tbl.Flush(); err != nil {
		return fail(err)
	}
	io.WriteString(stdout, "\n")
	io.WriteString(stdout, i18n.T("copy a command to adopt:")+"\n")
	for _, c := range report.Candidates {
		if c.Tag == "" {
			continue
		}
		io.WriteString(stdout, "  "+i18n.T("hukou adopt %s %s --tag %s", name, output.SanitizeField(c.Repo), output.SanitizeField(c.Tag))+"\n")
	}
	return nil
}

// repoPart returns the part of owner/repo after the slash.
func repoPart(fullName string) string {
	if i := strings.Index(fullName, "/"); i >= 0 {
		return fullName[i+1:]
	}
	return fullName
}

// firstLine trims a multi-line description to its first line and caps it.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
