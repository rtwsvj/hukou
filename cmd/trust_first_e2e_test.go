package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	outputmodel "github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestE2E_TrustFirstReadsRefusePendingJournalWithoutRecovery(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "pending-tool", "before\n")
	var stdout bytes.Buffer
	if err := doAdopt(&stdout, &stdout, live, "", true, "local", false); err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(dataDir, "upgrade", "pending-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("pending\n"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}

	candidate := writeExecutable(t, t.TempDir(), "candidate", "candidate\n")
	gate := func(string) (*provenance.Attribution, error) {
		t.Fatal("pending journal must be rejected before adoption inspection")
		return nil, nil
	}
	stdout.Reset()
	err = doAdoptDryRun(&stdout, candidate, "", true, "local", false, false, gate)
	if err == nil || !strings.Contains(err.Error(), "dry-run cannot recover") {
		t.Fatalf("adopt dry-run did not reject pending journal: %v", err)
	}

	releases := outdatedReleaseSource(func(owner, repo string) (ghrelease.Release, error) {
		t.Fatal("pending journal must be rejected before release lookup")
		return ghrelease.Release{}, nil
	})
	stdout.Reset()
	err = doOutdated(&stdout, nil, true, "", releases)
	if err == nil || !strings.Contains(err.Error(), "dry-run cannot recover") {
		t.Fatalf("outdated did not reject pending journal: %v", err)
	}

	assertCommandFile(t, live, "pending\n")
	status, inspectErr := statejournal.Inspect(dataDir)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("read-only commands changed pending journal: status=%+v err=%v", status, inspectErr)
	}
}

func TestE2E_OutdatedMatchesUpgradeDryRunWithoutDownloading(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "shared-check", "v1\n")
	var stdout bytes.Buffer
	if err := doAdopt(&stdout, &stdout, live, "owner/repo", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	assetName := fmt.Sprintf("shared-check-%s-%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	var metadataRequests atomic.Int32
	var downloadRequests atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo/releases/latest", "/repos/owner/repo/releases":
			metadataRequests.Add(1)
			writeFakeReleaseMetadata(t, w, r, ghrelease.Release{
				TagName: "v2.0.0",
				Assets: []ghrelease.Asset{{
					Name:               assetName,
					BrowserDownloadURL: server.URL + "/assets/" + assetName,
					Size:               10,
				}},
			})
		case "/assets/" + assetName:
			downloadRequests.Add(1)
			_, _ = w.Write([]byte("unexpected"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testGHClient(server)

	stdout.Reset()
	if err := doOutdated(&stdout, []string{"shared-check"}, true, "", client); err != nil {
		t.Fatal(err)
	}
	var report outputmodel.OutdatedReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Results) != 1 || report.Results[0].LatestTag != "v2.0.0" || report.Results[0].Asset != assetName {
		t.Fatalf("unexpected outdated decision: %+v", report)
	}

	stdout.Reset()
	if err := doUpgrade(&stdout, &stdout, []string{"shared-check"}, false, true, "", client); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "v1.0.0 -> v2.0.0") || !strings.Contains(stdout.String(), assetName) {
		t.Fatalf("upgrade dry-run disagreed with outdated: %s", stdout.String())
	}
	if metadataRequests.Load() != 2 || downloadRequests.Load() != 0 {
		t.Fatalf("metadata=%d downloads=%d, want metadata=2 downloads=0", metadataRequests.Load(), downloadRequests.Load())
	}
}
