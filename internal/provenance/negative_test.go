package provenance

import (
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

// TestTier1Detectors_unrelatedBinaryNil: every Tier-1 detector (except unknown,
// which is the always-match fallback) must return nil for a binary sitting in
// an unrelated directory with empty/mismatched env roots.
func TestTier1Detectors_unrelatedBinaryNil(t *testing.T) {
	unrelated := scan.Binary{
		Name:     "unrelated-bin",
		Path:     "/tmp/hukou-neg-unrelated/unrelated-bin",
		RealPath: "/tmp/hukou-neg-unrelated/unrelated-bin",
	}
	// Empty env: no roots point at the unrelated path.
	env := Env{}

	detectors := []Detector{
		NewBrewDetector(),
		NewMacPortsDetector(),
		NewMiseDetector(),
		NewAsdfDetector(),
		NewCargoDetector(),
		NewRustupDetector(),
		NewNpmDetector(),
		NewPnpmDetector(),
		NewYarnDetector(),
		NewBunDetector(),
		NewPipUserDetector(),
		NewGemDetector(),
		NewNixDetector(),
		NewVoltaDetector(),
		NewDenoDetector(),
		NewDotnetDetector(),
		NewComposerDetector(),
		NewKrewDetector(),
		NewCurlInstallerDetector(),
		NewPipxDetector(),
		NewUVDetector(),
		NewGoDetector(),
		NewSystemDetector(),
		// unknown intentionally excluded: always returns non-nil
	}

	for _, d := range detectors {
		t.Run(d.Name(), func(t *testing.T) {
			if err := d.Load(env); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if a := d.Match(unrelated); a != nil {
				t.Fatalf("expected nil for unrelated binary, got %+v", a)
			}
		})
	}

	// Also: with roots set but binary outside them.
	t.Run("macports-outside-bin", func(t *testing.T) {
		d := NewMacPortsDetector()
		_ = d.Load(Env{MacPorts: "/opt/local"})
		// /opt/local/lib is NOT bin/sbin/libexec
		a := d.Match(scan.Binary{
			Name: "libfoo", Path: "/opt/local/lib/libfoo", RealPath: "/opt/local/lib/libfoo",
		})
		if a != nil {
			t.Fatalf("macports must not claim /opt/local/lib: %+v", a)
		}
	})

	t.Run("nix-without-bin-component", func(t *testing.T) {
		d := NewNixDetector()
		store := filepath.Join("/nix", "store")
		_ = d.Load(Env{NixStore: store})
		a := d.Match(scan.Binary{
			Name: "share-file",
			Path: filepath.Join(store, "abcd-ripgrep-14.1.0", "share", "doc"),
			RealPath: filepath.Join(store, "abcd-ripgrep-14.1.0", "share", "doc"),
		})
		if a != nil {
			t.Fatalf("nix must not claim paths without /bin/: %+v", a)
		}
	})

	t.Run("composer-outside-vendor-bin", func(t *testing.T) {
		d := NewComposerDetector()
		bin := "/home/u/.composer/vendor/bin"
		_ = d.Load(Env{ComposerBins: []string{bin}})
		// package tree, not vendor/bin
		a := d.Match(scan.Binary{
			Name: "phpunit",
			Path: "/home/u/.composer/vendor/phpunit/phpunit/phpunit",
			RealPath: "/home/u/.composer/vendor/phpunit/phpunit/phpunit",
		})
		if a != nil {
			t.Fatalf("composer must only claim vendor/bin: %+v", a)
		}
	})
}
