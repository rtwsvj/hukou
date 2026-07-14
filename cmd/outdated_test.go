package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/store"
)

type outdatedReleaseSource func(owner, repo string) (ghrelease.Release, error)

func (f outdatedReleaseSource) Latest(owner, repo string) (ghrelease.Release, error) {
	return f(owner, repo)
}

func TestDoOutdatedJSON(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "outdated-tool", "v1\n")
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(filepath.Join(dataDir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Entry{
		Name:      "outdated-tool",
		Path:      live,
		Repo:      "owner/repo",
		Tag:       "v1.0.0",
		SHA256:    sha,
		AdoptedAt: "2026-07-14T00:00:00Z",
		UpdatedAt: "2026-07-14T00:00:00Z",
	}
	if err := activation.RecordAdopt(&entry, "fixture-outdated-tool", entry.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	m.Put(entry)
	if err := m.Save(filepath.Join(dataDir, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	asset := fmt.Sprintf("outdated-tool-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	source := outdatedReleaseSource(func(owner, repo string) (ghrelease.Release, error) {
		return ghrelease.Release{TagName: "v2.0.0", Assets: []ghrelease.Asset{{
			Name:               asset,
			BrowserDownloadURL: "https://example.test/assets/" + asset,
			Size:               1,
		}}}, nil
	})
	var stdout bytes.Buffer
	if err := doOutdated(&stdout, nil, true, "", source); err != nil {
		t.Fatal(err)
	}
	var report output.OutdatedReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode report: %v\n%s", err, stdout.String())
	}
	if report.SchemaVersion != 1 || len(report.Results) != 1 || report.Results[0].LatestTag != "v2.0.0" || report.Results[0].Asset != asset {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestDoOutdatedEmptyManifestDoesNotCreateDataRoot(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	var stdout bytes.Buffer
	source := outdatedReleaseSource(func(owner, repo string) (ghrelease.Release, error) {
		t.Fatal("empty manifest must not query releases")
		return ghrelease.Release{}, nil
	})
	if err := doOutdated(&stdout, nil, false, "", source); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("outdated created data root: %v", err)
	}
}
