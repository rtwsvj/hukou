package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
)

// fakeSuggestClient implements suggestClient with canned data.
type fakeSuggestClient struct {
	items     []ghrelease.SearchRepoItem
	releases  map[string]ghrelease.Release
	latestErr error
	gotQuery  string
}

func (f *fakeSuggestClient) SearchRepositories(query string, limit int) ([]ghrelease.SearchRepoItem, error) {
	f.gotQuery = query
	return f.items, nil
}

func (f *fakeSuggestClient) Latest(owner, repo string) (ghrelease.Release, error) {
	if f.latestErr != nil {
		return ghrelease.Release{}, f.latestErr
	}
	if rel, ok := f.releases[owner+"/"+repo]; ok {
		return rel, nil
	}
	return ghrelease.Release{}, errors.New("not found")
}

func suggestFixture(t *testing.T) (*fakeSuggestClient, string) {
	t.Helper()
	bin := t.TempDir()
	path := writeExecutable(t, bin, "mytool", "v1\n")
	client := &fakeSuggestClient{
		items: []ghrelease.SearchRepoItem{
			{FullName: "popular/other", StargazersCount: 9000, Description: "unrelated\nsecond line"},
			{FullName: "owner/mytool", StargazersCount: 120, Description: "the right one"},
			{FullName: "archived/mytool", StargazersCount: 80, Archived: true},
		},
		releases: map[string]ghrelease.Release{
			"owner/mytool":    {TagName: "v1.2.3"},
			"popular/other":   {TagName: "v9.0.0"},
			"archived/mytool": {TagName: "v0.9.0-rc.1", Prerelease: true},
		},
	}
	return client, path
}

func TestSuggestRanksExactNameFirstAndPrintsAdoptCommands(t *testing.T) {
	client, path := suggestFixture(t)
	var out bytes.Buffer
	if err := doSuggest(&out, path, client, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "owner/mytool") || !strings.Contains(text, "hukou adopt mytool owner/mytool --tag v1.2.3") {
		t.Fatalf("suggestion output wrong:\n%s", text)
	}
	// exact-name matches must rank above a 9000-star unrelated repo
	if strings.Index(text, "owner/mytool") > strings.Index(text, "popular/other") {
		t.Fatalf("exact-name match not ranked first:\n%s", text)
	}
	if !strings.Contains(text, "(prerelease)") || !strings.Contains(text, "[archived]") {
		t.Fatalf("prerelease/archived markers missing:\n%s", text)
	}
	if client.gotQuery != "mytool in:name" {
		t.Fatalf("search query wrong: %q", client.gotQuery)
	}
}

func TestSuggestJSONEnvelope(t *testing.T) {
	client, path := suggestFixture(t)
	var out bytes.Buffer
	if err := doSuggest(&out, path, client, true); err != nil {
		t.Fatal(err)
	}
	var rep suggestReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("json invalid: %v\n%s", err, out.String())
	}
	if rep.SchemaVersion != suggestReportSchemaVersion || rep.Name != "mytool" || len(rep.Candidates) != 3 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if rep.Candidates[0].Repo != "owner/mytool" || rep.Candidates[0].Tag != "v1.2.3" {
		t.Fatalf("ranking/field wrong: %+v", rep.Candidates[0])
	}
	// Both exact-name matches rank before the unrelated high-star repo; the
	// archived one keeps its stable position after owner/mytool.
	if rep.Candidates[1].Repo != "archived/mytool" || !rep.Candidates[1].Archived || !rep.Candidates[1].TagPrerelease {
		t.Fatalf("archived/prerelease flags wrong: %+v", rep.Candidates[1])
	}
	if rep.Candidates[2].Repo != "popular/other" {
		t.Fatalf("unrelated repo should rank last: %+v", rep.Candidates[2])
	}
}

func TestSuggestZeroWritesAndNoDataRoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing-data-root")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	client, path := suggestFixture(t)
	var out bytes.Buffer
	if err := doSuggest(&out, path, client, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("suggest created data root: %v", err)
	}
}

func TestSuggestNoCandidates(t *testing.T) {
	bin := t.TempDir()
	path := writeExecutable(t, bin, "ghost", "v1\n")
	client := &fakeSuggestClient{}
	var out bytes.Buffer
	if err := doSuggest(&out, path, client, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "no repository candidates found") {
		t.Fatalf("empty-state text missing: %q", out.String())
	}
}

func TestSuggestMissingBinary(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	client := &fakeSuggestClient{}
	var out bytes.Buffer
	err := doSuggest(&out, "ghost", client, false)
	if err == nil || !strings.Contains(err.Error(), "locate target") {
		t.Fatalf("expected locate failure, got %v", err)
	}
}

func TestSuggestSearchFailureFailsClosed(t *testing.T) {
	bin := t.TempDir()
	path := writeExecutable(t, bin, "mytool", "v1\n")
	client := &fakeSuggestClient{}
	client.items = nil
	// simulate a failing search by using the real seam: replace with a failing
	// implementation via a small wrapper
	failing := &failSuggestClient{err: errors.New("boom")}
	var out bytes.Buffer
	err := doSuggest(&out, path, failing, false)
	if err == nil || !strings.Contains(err.Error(), "search GitHub repositories") {
		t.Fatalf("expected wrapped search failure, got %v", err)
	}
}

type failSuggestClient struct{ err error }

func (f *failSuggestClient) SearchRepositories(string, int) ([]ghrelease.SearchRepoItem, error) {
	return nil, f.err
}
func (f *failSuggestClient) Latest(string, string) (ghrelease.Release, error) {
	return ghrelease.Release{}, f.err
}
