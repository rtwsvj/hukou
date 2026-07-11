package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/provenance"
	"github.com/rtwsvj/hukou/internal/scan"
)

func sampleReport() Report {
	return Report{
		Rows: []Row{
			{
				Binary: scan.Binary{
					Name: "ls", Path: "/bin/ls", RealPath: "/bin/ls",
					Kind: scan.KindMachO, Shadowed: false,
				},
				Attribution: provenance.Attribution{
					Source: "system", Package: "ls", Confidence: "exact",
					Evidence: "path prefix /bin",
				},
			},
			{
				Binary: scan.Binary{
					Name: "foo", Path: "/opt/foo", RealPath: "/opt/foo",
					Kind: scan.KindOther, Shadowed: false,
				},
				Attribution: provenance.Attribution{
					Source: "unknown", Package: "foo", Confidence: "inferred",
					Evidence: "no prior detector matched",
				},
			},
			{
				Binary: scan.Binary{
					Name: "ls", Path: "/usr/local/bin/ls", RealPath: "/usr/local/bin/ls",
					Kind: scan.KindMachO, Shadowed: true,
				},
				Attribution: provenance.Attribution{
					Source: "unknown", Package: "ls", Confidence: "inferred",
					Evidence: "no prior detector matched",
				},
			},
		},
		Skipped:     1,
		TotalWalked: 3,
	}
}

func TestWriteTable_summary(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteTable(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "NAME") {
		t.Fatalf("missing header: %s", out)
	}
	if !strings.Contains(out, "summary: total=3 sources=2 unknown=2 shadowed=1") {
		t.Fatalf("missing/wrong summary line:\n%s", out)
	}
	if !strings.Contains(out, "skipped=1") {
		t.Fatalf("expected skipped in summary:\n%s", out)
	}
	if !strings.Contains(out, "system=1") || !strings.Contains(out, "unknown=2") {
		t.Fatalf("missing by-source breakdown:\n%s", out)
	}
}

func TestWriteJSON_roundtrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("json decode: %v\n%s", err, buf.String())
	}
	if decoded.Summary.Total != 3 {
		t.Fatalf("summary.total=%d want 3", decoded.Summary.Total)
	}
	if decoded.Summary.Unknown != 2 {
		t.Fatalf("summary.unknown=%d want 2", decoded.Summary.Unknown)
	}
	if decoded.Summary.Shadowed != 1 {
		t.Fatalf("summary.shadowed=%d want 1", decoded.Summary.Shadowed)
	}
	if decoded.Summary.SourceN != 2 {
		t.Fatalf("summary.source_count=%d want 2", decoded.Summary.SourceN)
	}
	if len(decoded.Rows) != 3 {
		t.Fatalf("rows=%d", len(decoded.Rows))
	}
}

func TestSummarize(t *testing.T) {
	r := sampleReport()
	Summarize(&r)
	if r.Summary.Total != 3 || r.Summary.Unknown != 2 || r.Summary.Shadowed != 1 {
		t.Fatalf("%+v", r.Summary)
	}
}
