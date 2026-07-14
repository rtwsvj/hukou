package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rtwsvj/hukou/internal/activation"
	"github.com/rtwsvj/hukou/internal/manifest"
	"github.com/rtwsvj/hukou/internal/state"
	"github.com/rtwsvj/hukou/internal/store"
	statejournal "github.com/rtwsvj/hukou/internal/transaction"
)

func TestPolicyCommandsRegistered(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"policy", "show"})
	if err != nil {
		t.Fatal(err)
	}
	if command != policyShowCmd || command.Flags().Lookup("json") == nil {
		t.Fatal("policy show command or flags are not registered")
	}
	command, _, err = rootCmd.Find([]string{"policy", "set"})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mode", "channel", "pin", "unpin", "rollback-depth"} {
		if command.Flags().Lookup(name) == nil {
			t.Fatalf("policy set flag --%s is not registered", name)
		}
	}
}

func TestPolicyShowJSONIsStableAndSorted(t *testing.T) {
	root, _ := writePolicyFixture(t, []manifest.Entry{
		policyFixtureEntry(t, "zeta", manifest.UpdatePolicy{Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelPrerelease}),
		policyFixtureEntry(t, "alpha", manifest.UpdatePolicy{Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable, PinnedTag: "v1.0.0"}),
	})
	t.Setenv("HUKOU_DATA_DIR", root)

	var output bytes.Buffer
	if err := doPolicyShow(&output, "", true); err != nil {
		t.Fatal(err)
	}
	var report policyReport
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != 1 || len(report.Policies) != 2 {
		t.Fatalf("report=%+v", report)
	}
	if report.Policies[0].Name != "alpha" || report.Policies[1].Name != "zeta" {
		t.Fatalf("policy order=%+v", report.Policies)
	}
	alpha := report.Policies[0]
	if alpha.PinnedTag != "v1.0.0" || alpha.RollbackDepth != manifest.DefaultRollbackDepth || alpha.RollbackDepthSource != "manifest" {
		t.Fatalf("alpha=%+v", alpha)
	}
	for _, field := range []string{`"schema_version"`, `"pinned_tag"`, `"rollback_depth"`, `"rollback_depth_source"`} {
		if !strings.Contains(output.String(), field) {
			t.Fatalf("stable field %s missing from %s", field, output.String())
		}
	}
}

func TestPolicySetAtomicallyPersistsWithoutTouchingLiveBinary(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.DefaultUpdatePolicy())
	root, lives := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	live := lives["tool"]
	beforeBody, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	options := policySetOptions{
		Mode:             string(manifest.UpdateModeLegacy),
		ModeSet:          true,
		Channel:          string(manifest.UpdateChannelPrerelease),
		ChannelSet:       true,
		Pin:              "v1.0.0-beta.1",
		PinSet:           true,
		RollbackDepth:    5,
		RollbackDepthSet: true,
	}
	if err := doPolicySet(&output, &output, "tool", options); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Policy updated for tool") {
		t.Fatalf("output=%q", output.String())
	}

	loaded, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Get("tool")
	if got == nil || got.UpdatePolicy.Mode != manifest.UpdateModeLegacy || got.UpdatePolicy.Channel != manifest.UpdateChannelPrerelease || got.UpdatePolicy.PinnedTag != "v1.0.0-beta.1" {
		t.Fatalf("entry=%+v", got)
	}
	if got.Retention == nil || got.Retention.RollbackDepth != 5 {
		t.Fatalf("retention=%+v", got.Retention)
	}
	if got.UpdatedAt != entry.UpdatedAt || got.Tag != entry.Tag || got.SHA256 != entry.SHA256 {
		t.Fatalf("binary state fields changed: before=%+v after=%+v", entry, got)
	}
	afterBody, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Stat(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterBody) != string(beforeBody) || !os.SameFile(beforeInfo, afterInfo) || afterInfo.Mode() != beforeInfo.Mode() {
		t.Fatalf("live binary changed during policy set")
	}
}

func TestPolicySetSaveFailureLeavesManifestAndLiveUnchanged(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.DefaultUpdatePolicy())
	root, lives := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	manifestPath := filepath.Join(root, "manifest.json")
	beforeManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	beforeLive, err := os.ReadFile(lives["tool"])
	if err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected atomic save failure")
	var output bytes.Buffer
	err = doPolicySetWithSave(&output, &output, "tool", policySetOptions{
		Channel: string(manifest.UpdateChannelPrerelease), ChannelSet: true,
	}, func(*manifest.Manifest) error { return injected })
	if !errors.Is(err, injected) {
		t.Fatalf("error=%v", err)
	}
	afterManifest, _ := os.ReadFile(manifestPath)
	afterLive, _ := os.ReadFile(lives["tool"])
	if string(afterManifest) != string(beforeManifest) || string(afterLive) != string(beforeLive) {
		t.Fatal("failed policy save changed persisted state")
	}
	if output.Len() != 0 {
		t.Fatalf("success output emitted after failure: %q", output.String())
	}
}

func TestPolicySetFailsClosedOnLockContention(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.DefaultUpdatePolicy())
	root, _ := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	before, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	lock, err := state.Acquire(filepath.Join(root, "state.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Release()

	err = doPolicySet(ioDiscard{}, ioDiscard{}, "tool", policySetOptions{
		Channel: string(manifest.UpdateChannelPrerelease), ChannelSet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "state lock") {
		t.Fatalf("error=%v", err)
	}
	after, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	if string(after) != string(before) {
		t.Fatal("manifest changed while state lock was held")
	}
}

func TestPolicySetDoesNotRecoverPendingTransaction(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.DefaultUpdatePolicy())
	root, lives := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	live := lives["tool"]
	beforeManifest, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	beforeLive, _ := os.ReadFile(live)
	if _, err := statejournal.Begin(root, "policy-test", "tool", []statejournal.Spec{
		{Role: "live", Path: live, After: statejournal.RegularBytes([]byte("replacement\n"), 0o755)},
	}); err != nil {
		t.Fatal(err)
	}

	err := doPolicySet(ioDiscard{}, ioDiscard{}, "tool", policySetOptions{
		Channel: string(manifest.UpdateChannelPrerelease), ChannelSet: true,
	})
	if err == nil || !strings.Contains(err.Error(), "clean transaction state") {
		t.Fatalf("error=%v", err)
	}
	afterManifest, _ := os.ReadFile(filepath.Join(root, "manifest.json"))
	afterLive, _ := os.ReadFile(live)
	if string(afterManifest) != string(beforeManifest) || string(afterLive) != string(beforeLive) {
		t.Fatal("policy set recovered a transaction or changed state")
	}
}

func TestPolicyValidationAndMissingEntryAreZeroWrite(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HUKOU_DATA_DIR", missingRoot)
	if err := doPolicySet(ioDiscard{}, ioDiscard{}, "missing", policySetOptions{Mode: "calendar", ModeSet: true}); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if _, err := os.Lstat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("invalid policy set created data root: %v", err)
	}
	if err := doPolicySet(ioDiscard{}, ioDiscard{}, "missing", policySetOptions{Mode: "semver", ModeSet: true}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing entry error=%v", err)
	}
	if _, err := os.Lstat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("missing entry created data root: %v", err)
	}
}

func TestPolicyShowMissingRootIsReadOnly(t *testing.T) {
	missingRoot := filepath.Join(t.TempDir(), "missing")
	t.Setenv("HUKOU_DATA_DIR", missingRoot)
	var output bytes.Buffer
	if err := doPolicyShow(&output, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"schema_version": 1`) || !strings.Contains(output.String(), `"policies": []`) {
		t.Fatalf("output=%q", output.String())
	}
	if _, err := os.Lstat(missingRoot); !os.IsNotExist(err) {
		t.Fatalf("policy show created data root: %v", err)
	}
}

func TestPolicyNoopSkipsSave(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.DefaultUpdatePolicy())
	root, _ := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	saves := 0
	var output bytes.Buffer
	if err := doPolicySetWithSave(&output, &output, "tool", policySetOptions{
		Mode: string(manifest.UpdateModeSemver), ModeSet: true,
	}, func(*manifest.Manifest) error {
		saves++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if saves != 0 || !strings.Contains(output.String(), "Policy unchanged") {
		t.Fatalf("saves=%d output=%q", saves, output.String())
	}
}

func TestPolicyUnpinAndOptionConflicts(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.UpdatePolicy{
		Mode: manifest.UpdateModeSemver, Channel: manifest.UpdateChannelStable, PinnedTag: "v1.0.0",
	})
	root, _ := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	if err := doPolicySet(ioDiscard{}, ioDiscard{}, "tool", policySetOptions{Unpin: true}); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Get("tool").UpdatePolicy.PinnedTag; got != "" {
		t.Fatalf("pinned tag=%q after --unpin", got)
	}

	for name, options := range map[string]policySetOptions{
		"pin and unpin":  {Pin: "v2.0.0", PinSet: true, Unpin: true},
		"negative depth": {RollbackDepth: -1, RollbackDepthSet: true},
		"empty":          {},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePolicySetOptions(options); err == nil {
				t.Fatalf("options accepted: %+v", options)
			}
		})
	}
}

func TestPolicySetRejectsUnsafeSemverTransitionWithoutWriting(t *testing.T) {
	for _, test := range []struct {
		name      string
		tag       string
		local     bool
		wantError string
	}{
		{name: "non-semver current tag", tag: "release-2024", wantError: "not a strict Semantic Version"},
		{name: "local entry", tag: "local", local: true, wantError: "local entry"},
	} {
		t.Run(test.name, func(t *testing.T) {
			entry := policyFixtureEntry(t, "tool", manifest.UpdatePolicy{
				Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable,
			})
			entry.Tag = test.tag
			entry.Activations[0].Tag = test.tag
			if test.local {
				entry.Repo = ""
			}
			root, lives := writePolicyFixture(t, []manifest.Entry{entry})
			t.Setenv("HUKOU_DATA_DIR", root)
			manifestPath := filepath.Join(root, "manifest.json")
			beforeManifest, err := os.ReadFile(manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			beforeLive, err := os.ReadFile(lives["tool"])
			if err != nil {
				t.Fatal(err)
			}

			var output bytes.Buffer
			err = doPolicySet(&output, &output, "tool", policySetOptions{
				Mode: string(manifest.UpdateModeSemver), ModeSet: true,
			})
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("error=%v", err)
			}
			afterManifest, _ := os.ReadFile(manifestPath)
			afterLive, _ := os.ReadFile(lives["tool"])
			if string(afterManifest) != string(beforeManifest) || string(afterLive) != string(beforeLive) {
				t.Fatal("rejected semver transition changed persisted state")
			}
			if output.Len() != 0 {
				t.Fatalf("success output emitted after rejection: %q", output.String())
			}
		})
	}
}

func TestPolicySetAllowsSemverTransitionWithSortableCurrentTag(t *testing.T) {
	entry := policyFixtureEntry(t, "tool", manifest.UpdatePolicy{
		Mode: manifest.UpdateModeLegacy, Channel: manifest.UpdateChannelStable,
	})
	root, _ := writePolicyFixture(t, []manifest.Entry{entry})
	t.Setenv("HUKOU_DATA_DIR", root)
	if err := doPolicySet(ioDiscard{}, ioDiscard{}, "tool", policySetOptions{
		Mode: string(manifest.UpdateModeSemver), ModeSet: true,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := manifest.Load(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Get("tool").UpdatePolicy.Mode; got != manifest.UpdateModeSemver {
		t.Fatalf("mode=%q", got)
	}
}

func writePolicyFixture(t *testing.T, entries []manifest.Entry) (string, map[string]string) {
	t.Helper()
	root := t.TempDir()
	m := &manifest.Manifest{
		SchemaVersion: manifest.CurrentSchemaVersion,
		Retention:     manifest.DefaultRetentionPolicy(),
		Entries:       entries,
	}
	if err := m.Save(filepath.Join(root, "manifest.json")); err != nil {
		t.Fatal(err)
	}
	lives := make(map[string]string, len(entries))
	for _, entry := range entries {
		lives[entry.Name] = entry.Path
	}
	return root, lives
}

func policyFixtureEntry(t *testing.T, name string, policy manifest.UpdatePolicy) manifest.Entry {
	t.Helper()
	live := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(live, []byte(name+" binary\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sha, err := store.SHA256File(live)
	if err != nil {
		t.Fatal(err)
	}
	entry := manifest.Entry{
		Name:         name,
		Path:         live,
		Repo:         "owner/repo",
		Tag:          "v1.0.0",
		SHA256:       sha,
		AdoptedAt:    "2026-07-14T00:00:00Z",
		UpdatedAt:    "2026-07-14T00:00:00Z",
		UpdatePolicy: policy,
	}
	if err := activation.RecordAdopt(&entry, "fixture-"+name, entry.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	return entry
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
