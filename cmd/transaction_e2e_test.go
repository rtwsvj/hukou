package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/rtwsvj/hukou/internal/ghrelease"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/provenance"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestMutationLockRecoversPreparedJournalBeforeLoadingState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "prepared-tool", "before\n")
	var output bytes.Buffer
	if err := doAdopt(&output, &output, live, "", true, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	manifestFile := filepath.Join(dataDir, "manifest.json")
	tx, err := statejournal.Begin(dataDir, "upgrade", "prepared-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after\n"), 0o755)},
		{Role: "manifest", Path: manifestFile, After: statejournal.RegularBytes([]byte("not-yet-applied"), 0o600)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	// A no-op rollback still acquires the mutation lock. Recovery must happen
	// before it loads the manifest and decides the requested tag is current.
	if err := doRollback(&output, &output, "prepared-tool", "v1.0.0"); err != nil {
		t.Fatalf("rollback after prepared crash: %v\n%s", err, output.String())
	}
	assertCommandFile(t, live, "before\n")
	m, err := manifest.Load(manifestFile)
	if err != nil || m.Get("prepared-tool") == nil || m.Get("prepared-tool").Tag != "v1.0.0" {
		t.Fatalf("manifest not rolled back before command: entry=%+v err=%v", m.Get("prepared-tool"), err)
	}
	assertCommandJournalClean(t, dataDir)
}

func TestMutationLockRollsCommittedJournalForwardBeforeLoadingState(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "committed-tool", "before\n")
	var output bytes.Buffer
	if err := doAdopt(&output, &output, live, "", true, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	manifestFile := filepath.Join(dataDir, "manifest.json")
	beforeManifest, err := os.ReadFile(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(manifestFile)
	if err != nil {
		t.Fatal(err)
	}
	afterSHA := sha256Bytes([]byte("after\n"))
	afterEntry := *m.Get("committed-tool")
	afterEntry.Tag = "v2.0.0"
	afterEntry.SHA256 = afterSHA
	m.Put(afterEntry)
	afterManifest, err := encodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(dataDir, "upgrade", "committed-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after\n"), 0o755)},
		{Role: "manifest", Path: manifestFile, After: statejournal.RegularBytes(afterManifest, 0o600)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("manifest"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Model a power-loss visibility combination where COMMIT and live survived
	// but the manifest directory entry presents its old state.
	if err := os.WriteFile(manifestFile, beforeManifest, 0o600); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	if err := doRollback(&output, &output, "committed-tool", "v2.0.0"); err != nil {
		t.Fatalf("rollback after committed crash: %v\n%s", err, output.String())
	}
	assertCommandFile(t, live, "after\n")
	m, err = manifest.Load(manifestFile)
	if err != nil || m.Get("committed-tool") == nil || m.Get("committed-tool").Tag != "v2.0.0" || m.Get("committed-tool").SHA256 != afterSHA {
		t.Fatalf("manifest not rolled forward before command: entry=%+v err=%v", m.Get("committed-tool"), err)
	}
	assertCommandJournalClean(t, dataDir)
}

func TestUpgradeDryRunRefusesPendingJournalWithoutRecovering(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "dry-run-tool", "before\n")
	var output bytes.Buffer
	if err := doAdopt(&output, &output, live, "", true, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	tx, err := statejournal.Begin(dataDir, "upgrade", "dry-run-tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("after\n"), 0o755)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Apply("live"); err != nil {
		t.Fatal(err)
	}

	output.Reset()
	err = doUpgrade(&output, &output, []string{"dry-run-tool"}, false, true, "", ghrelease.New(""))
	if err == nil || !strings.Contains(err.Error(), "dry-run cannot recover") {
		t.Fatalf("expected read-only pending error, got %v\n%s", err, output.String())
	}
	assertCommandFile(t, live, "after\n")
	status, inspectErr := statejournal.Inspect(dataDir)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("dry-run changed journal: status=%+v err=%v", status, inspectErr)
	}
}

func TestAdoptCommitGuardRefusesPostSaveExternalLiveDrift(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	live := writeExecutable(t, t.TempDir(), "guarded-tool", "before\n")
	var output bytes.Buffer
	err := doAdoptWithDeps(&output, &output, live, "", true, "local", false,
		func(string) (*provenance.Attribution, error) {
			return &provenance.Attribution{Source: "unknown", Evidence: "test"}, nil
		},
		func(m *manifest.Manifest) error {
			if err := saveManifest(m); err != nil {
				return err
			}
			return os.WriteFile(live, []byte("external\n"), 0o755)
		},
	)
	if err == nil || !strings.Contains(err.Error(), "unknown drift") {
		t.Fatalf("expected commit guard drift failure, got %v\n%s", err, output.String())
	}
	assertCommandFile(t, live, "external\n")
	status, inspectErr := statejournal.Inspect(dataDir)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("guard drift evidence not retained: status=%+v err=%v", status, inspectErr)
	}
}

func TestUpgradeAllStopsBeforeNextNetworkRequestWhenTransactionRemainsPending(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("HUKOU_DATA_DIR", dataDir)
	alphaLive := writeExecutable(t, t.TempDir(), "alpha", "alpha-v1\n")
	betaLive := writeExecutable(t, t.TempDir(), "beta", "beta-v1\n")
	var output bytes.Buffer
	if err := doAdopt(&output, &output, alphaLive, "owner/alpha", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}
	if err := doAdopt(&output, &output, betaLive, "owner/beta", false, "v1.0.0", false); err != nil {
		t.Fatal(err)
	}

	alphaAssetName := platformAssetName("alpha")
	alphaAsset := makeTarGz(t, "alpha", []byte("alpha-v2\n"))
	var alphaLatestCalls atomic.Int32
	var betaLatestCalls atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/alpha/releases/latest":
			alphaLatestCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v2.0.0",
				"assets": []map[string]any{{
					"name":                 alphaAssetName,
					"browser_download_url": server.URL + "/assets/" + alphaAssetName,
					"size":                 len(alphaAsset),
				}},
			})
		case "/repos/owner/beta/releases/latest":
			betaLatestCalls.Add(1)
			http.Error(w, "beta must not be queried while state is pending", http.StatusInternalServerError)
		case "/assets/" + alphaAssetName:
			_, _ = w.Write(alphaAsset)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	saveCalls := 0
	output.Reset()
	err := doUpgradeWithSave(&output, &output, nil, true, false, "", testGHClient(server), func(m *manifest.Manifest) error {
		saveCalls++
		if err := saveManifest(m); err != nil {
			return err
		}
		if err := os.WriteFile(alphaLive, []byte("external-writer\n"), 0o755); err != nil {
			return err
		}
		return os.Chmod(alphaLive, 0o755)
	})
	if err == nil || !strings.Contains(err.Error(), "state remains unresolved") {
		t.Fatalf("expected unresolved-state batch stop, got %v\n%s", err, output.String())
	}
	if saveCalls != 1 {
		t.Fatalf("manifest save calls=%d want 1", saveCalls)
	}
	if alphaLatestCalls.Load() != 1 || betaLatestCalls.Load() != 0 {
		t.Fatalf("latest calls: alpha=%d beta=%d", alphaLatestCalls.Load(), betaLatestCalls.Load())
	}
	if _, statErr := os.Stat(filepath.Join(dataDir, "store", "beta", "v2.0.0")); !os.IsNotExist(statErr) {
		t.Fatalf("second store version appeared despite pending transaction: %v", statErr)
	}
	status, inspectErr := statejournal.Inspect(dataDir)
	if inspectErr != nil || len(status.Pending) != 1 {
		t.Fatalf("pending evidence not retained: status=%+v err=%v", status, inspectErr)
	}
}

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func assertCommandFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("file %s=%q want %q err=%v", path, got, want, err)
	}
}

func assertCommandJournalClean(t *testing.T, dataDir string) {
	t.Helper()
	status, err := statejournal.Inspect(dataDir)
	if err != nil || status.NeedsRecovery() {
		t.Fatalf("journal not clean: status=%+v err=%v", status, err)
	}
}
