package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandOutput(t *testing.T) {
	var out bytes.Buffer
	versionCmd.SetOut(&out)
	versionCmd.Run(versionCmd, nil)
	if got := out.String(); !strings.Contains(got, "hukou ") || !strings.Contains(got, "commit ") || !strings.Contains(got, "built ") {
		t.Fatalf("unexpected version output: %q", got)
	}
}
