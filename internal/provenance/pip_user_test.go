package provenance

import (
	"path/filepath"
	"testing"

	"github.com/rtwsvj/hukou/internal/scan"
)

// TestPipUserDetector_layouts covers both install layouts the matcher must
// accept: the darwin versioned tree <base>/<version>/bin/<name> and the
// Linux flat tree <base>/bin/<name>. The matcher accepts either shape for
// either base, so a base swap never strands existing attributions.
func TestPipUserDetector_layouts(t *testing.T) {
	base := t.TempDir()

	cases := []struct {
		name    string
		env     Env
		binPath string
		want    *Attribution // nil = expect no match
	}{
		{
			name:    "darwin-versioned",
			env:     Env{PipUserBase: filepath.Join(base, "Library", "Python")},
			binPath: filepath.Join(base, "Library", "Python", "3.12", "bin", "flake8"),
			want: &Attribution{
				Source:     "pip-user",
				Package:    "flake8",
				Version:    "3.12",
				Confidence: "inferred",
			},
		},
		{
			name:    "linux-flat",
			env:     Env{PipUserBase: filepath.Join(base, ".local")},
			binPath: filepath.Join(base, ".local", "bin", "httpie"),
			want: &Attribution{
				Source:     "pip-user",
				Package:    "httpie",
				Confidence: "inferred",
			},
		},
		{
			name:    "cross-layout-flat-under-versioned-base",
			env:     Env{PipUserBase: filepath.Join(base, "Library", "Python")},
			binPath: filepath.Join(base, "Library", "Python", "bin", "flake8"),
			want: &Attribution{
				Source:     "pip-user",
				Package:    "flake8",
				Confidence: "inferred",
			},
		},
		{
			name:    "cross-layout-versioned-under-flat-base",
			env:     Env{PipUserBase: filepath.Join(base, ".local")},
			binPath: filepath.Join(base, ".local", "3.12", "bin", "flake8"),
			want: &Attribution{
				Source:     "pip-user",
				Package:    "flake8",
				Version:    "3.12",
				Confidence: "inferred",
			},
		},
		{
			name:    "flat-base-direct-child-not-bin-no-match",
			env:     Env{PipUserBase: filepath.Join(base, ".local")},
			binPath: filepath.Join(base, ".local", "stray"),
			want:    nil,
		},
		{
			name:    "outside-base-no-match",
			env:     Env{PipUserBase: filepath.Join(base, ".local")},
			binPath: filepath.Join(base, "elsewhere", "bin", "nope"),
			want:    nil,
		},
		{
			name:    "empty-base-inert",
			env:     Env{},
			binPath: filepath.Join(base, ".local", "bin", "httpie"),
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := NewPipUserDetector()
			if err := d.Load(tc.env); err != nil {
				t.Fatal(err)
			}
			b := scan.Binary{
				Name:     filepath.Base(tc.binPath),
				Path:     tc.binPath,
				RealPath: tc.binPath,
			}
			got := d.Match(b)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("unexpected match: %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatal("expected match, got nil")
			}
			if got.Source != tc.want.Source ||
				got.Package != tc.want.Package ||
				got.Version != tc.want.Version ||
				got.Confidence != tc.want.Confidence {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			if got.Evidence == "" {
				t.Fatalf("missing evidence: %+v", got)
			}
		})
	}
}

// TestPipUserDetector_symlinkResolution: a symlink on PATH pointing into the
// pip user tree attributes via its RealPath (pipx/uv-style layout).
func TestPipUserDetector_symlinkResolution(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "Library", "Python", "3.13", "bin", "ruff")
	link := filepath.Join(root, "link-ruff")
	d := NewPipUserDetector()
	if err := d.Load(Env{PipUserBase: filepath.Join(root, "Library", "Python")}); err != nil {
		t.Fatal(err)
	}
	b := scan.Binary{Name: "ruff", Path: link, RealPath: target}
	a := d.Match(b)
	if a == nil || a.Source != "pip-user" || a.Version != "3.13" {
		t.Fatalf("symlink attribution failed: %+v", a)
	}
}
