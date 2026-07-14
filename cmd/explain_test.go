package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/output"
	"github.com/rtwsvj/hukou/internal/provenance"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestBuildExplainReportShowsActiveAndShadowedMatches(t *testing.T) {
	firstDir := t.TempDir()
	secondDir := t.TempDir()
	first := writeExecutable(t, firstDir, "demo-tool", "first\n")
	second := writeExecutable(t, secondDir, "demo-tool", "second\n")
	env := provenance.Env{
		Path:          firstDir + string(os.PathListSeparator) + secondDir,
		HukouManifest: filepath.Join(t.TempDir(), "missing-manifest.json"),
	}

	report, err := buildExplainReport("demo-tool", env)
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || len(report.Matches) != 2 || report.Active == nil {
		t.Fatalf("unexpected report: %+v", report)
	}
	if report.Active.Path != first {
		t.Fatalf("active path = %s, want %s", report.Active.Path, first)
	}
	if report.Matches[0].Shadowed {
		t.Fatal("first PATH match marked shadowed")
	}
	if !report.Matches[1].Shadowed || report.Matches[1].Path != second {
		t.Fatalf("second match = %+v", report.Matches[1])
	}
}

func TestBuildExplainReportExplicitPathDoesNotScanPATH(t *testing.T) {
	target := writeExecutable(t, t.TempDir(), "standalone", "body\n")
	env := provenance.Env{
		Path:          filepath.Join(t.TempDir(), "missing"),
		HukouManifest: filepath.Join(t.TempDir(), "missing-manifest.json"),
	}
	report, err := buildExplainReport(target, env)
	if err != nil {
		t.Fatal(err)
	}
	if report.Active == nil || len(report.Matches) != 1 || report.Active.Path != target {
		t.Fatalf("unexpected report: %+v", report)
	}
}

func TestBuildExplainReportMissingName(t *testing.T) {
	env := provenance.Env{Path: t.TempDir(), HukouManifest: filepath.Join(t.TempDir(), "missing.json")}
	if _, err := buildExplainReport("does-not-exist", env); err == nil {
		t.Fatal("expected missing executable error")
	}
}

func TestExplainJSONUsesStableSnakeCaseFields(t *testing.T) {
	target := writeExecutable(t, t.TempDir(), "json-tool", "body\n")
	report, err := buildExplainReport(target, provenance.Env{HukouManifest: filepath.Join(t.TempDir(), "missing.json")})
	if err != nil {
		t.Fatal(err)
	}
	var encoded bytes.Buffer
	if err := output.WriteExplainJSON(&encoded, report); err != nil {
		t.Fatal(err)
	}
	jsonOutput := encoded.String()
	for _, field := range []string{`"real_path"`, `"source"`, `"shadowed"`} {
		if !strings.Contains(jsonOutput, field) {
			t.Fatalf("missing %s in %s", field, jsonOutput)
		}
	}
	for _, leaked := range []string{`"RealPath"`, `"Source"`, `"Shadowed"`} {
		if strings.Contains(jsonOutput, leaked) {
			t.Fatalf("internal Go field %s leaked into %s", leaked, jsonOutput)
		}
	}
}

func TestExplainNameIsZeroWriteAndZeroNetwork(t *testing.T) {
	pathDir := t.TempDir()
	target := writeExecutable(t, pathDir, "read-only-tool", "unchanged\n")
	stateRoot := filepath.Join(t.TempDir(), "missing-hukou-state")
	beforeInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	beforeData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	beforeEntries, err := os.ReadDir(pathDir)
	if err != nil {
		t.Fatal(err)
	}

	oldTransport := http.DefaultTransport
	networkCalls := 0
	http.DefaultTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		networkCalls++
		return nil, fmt.Errorf("unexpected network request: %s", request.URL)
	})
	t.Cleanup(func() { http.DefaultTransport = oldTransport })

	report, err := buildExplainReport("read-only-tool", provenance.Env{
		Home:          t.TempDir(),
		Path:          pathDir,
		HukouManifest: filepath.Join(stateRoot, "manifest.json"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Active == nil || report.Active.Path != target {
		t.Fatalf("unexpected report: %+v", report)
	}
	if networkCalls != 0 {
		t.Fatalf("explain performed %d network request(s)", networkCalls)
	}
	if _, err := os.Lstat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("explain created hukou state: %v", err)
	}
	afterEntries, err := os.ReadDir(pathDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterEntries) != len(beforeEntries) {
		t.Fatalf("PATH directory entry count changed: before=%d after=%d", len(beforeEntries), len(afterEntries))
	}
	afterInfo, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	afterData, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeData, afterData) || beforeInfo.Mode() != afterInfo.Mode() || beforeInfo.Size() != afterInfo.Size() || !beforeInfo.ModTime().Equal(afterInfo.ModTime()) {
		t.Fatal("explain changed the inspected executable")
	}
}
