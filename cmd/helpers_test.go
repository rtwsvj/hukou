package cmd

import (
	"path/filepath"
	"testing"
)

// resolveDataRoot: HUKOU_DATA_DIR wins, then XDG_DATA_HOME, then HOME's
// .local/share — and when nothing is available the resolution is an error,
// never a silent relative "./hukou".
func TestResolveDataRoot(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	home := func(h string, err error) func() (string, error) {
		return func() (string, error) { return h, err }
	}

	// HUKOU_DATA_DIR wins over everything.
	if got, err := resolveDataRoot(env(map[string]string{"HUKOU_DATA_DIR": "/explicit", "XDG_DATA_HOME": "/xdg"}), home("/home/u", nil)); err != nil || got != "/explicit" {
		t.Fatalf("explicit = %q, %v", got, err)
	}
	// XDG next.
	if got, err := resolveDataRoot(env(map[string]string{"XDG_DATA_HOME": "/xdg"}), home("/home/u", nil)); err != nil || got != filepath.Join("/xdg", "hukou") {
		t.Fatalf("xdg = %q, %v", got, err)
	}
	// HOME fallback.
	if got, err := resolveDataRoot(env(map[string]string{}), home("/home/u", nil)); err != nil || got != filepath.Join("/home/u", ".local", "share", "hukou") {
		t.Fatalf("home = %q, %v", got, err)
	}
	// Nothing available: an error, never the old relative "./hukou" fallback.
	if _, err := resolveDataRoot(env(map[string]string{}), home("", nil)); err == nil {
		t.Fatal("expected an error when HOME and XDG_DATA_HOME are both unavailable")
	}
}
