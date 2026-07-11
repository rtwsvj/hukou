package scan

import (
	"os"
	"path/filepath"
	"strings"
)

// Result is the outcome of walking PATH directories.
type Result struct {
	Binaries []Binary
	Skipped  int      // unreadable entries skipped
	Errors   []string // non-fatal walk diagnostics
}

// SplitPATH splits a PATH environment value into directories,
// dropping empty segments.
func SplitPATH(pathEnv string) []string {
	if pathEnv == "" {
		return nil
	}
	parts := strings.Split(pathEnv, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// Walk scans dirs in order. First occurrence of each base name is active;
// later same-named executables are kept with Shadowed=true.
// Only mode&0111 files are considered (Stat follows symlinks).
// Kind is detected from a 4-byte header; large files are not fully read.
// Unreadable files are skipped and counted in Skipped.
func Walk(dirs []string) (*Result, error) {
	res := &Result{}
	seen := make(map[string]struct{})

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			res.Skipped++
			res.Errors = append(res.Errors, "readdir "+dir+": "+err.Error())
			continue
		}
		for _, ent := range entries {
			// Skip directory entries by name; Stat still used for symlink→dir.
			if ent.IsDir() {
				continue
			}
			name := ent.Name()
			full := filepath.Join(dir, name)

			// Stat follows symlinks so +x is taken from the target.
			info, err := os.Stat(full)
			if err != nil {
				res.Skipped++
				continue
			}
			if info.IsDir() {
				continue
			}
			// Executable iff any of owner/group/other execute bits is set.
			if info.Mode()&0o111 == 0 {
				continue
			}

			kind, err := DetectKind(full)
			if err != nil {
				res.Skipped++
				continue
			}

			realPath, err := filepath.EvalSymlinks(full)
			if err != nil {
				realPath = ""
			}

			_, shadowed := seen[name]
			if !shadowed {
				seen[name] = struct{}{}
			}

			res.Binaries = append(res.Binaries, Binary{
				Name:     name,
				Path:     full,
				RealPath: realPath,
				Kind:     kind,
				Shadowed: shadowed,
			})
		}
	}
	return res, nil
}

// IsExecutable reports whether mode has any execute bit (owner/group/other).
func IsExecutable(mode os.FileMode) bool {
	return mode&0o111 != 0
}
