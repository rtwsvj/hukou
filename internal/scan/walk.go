package scan

import (
	"os"
	"path/filepath"
	"strings"
)

// Result is the outcome of walking PATH directories.
type Result struct {
	Binaries   []Binary
	Skipped    int         // entries skipped (non-regular, open failures counted as included, etc.)
	Errors     []string    // non-fatal walk diagnostics (legacy string form)
	FileErrors []FileError // per-file path+reason details
	Warnings   []string    // e.g. empty PATH segments skipped
}

// SplitPATH splits a PATH environment value into directories.
// Empty segments are dropped (deliberate deviation from POSIX empty=cwd);
// callers that need a warning should use SplitPATHWithWarnings.
func SplitPATH(pathEnv string) []string {
	dirs, _ := SplitPATHWithWarnings(pathEnv)
	return dirs
}

// SplitPATHWithWarnings is like SplitPATH but also returns warnings for
// empty segments that were skipped (POSIX would treat them as ".").
// Relative segments are normalized with filepath.Abs.
func SplitPATHWithWarnings(pathEnv string) (dirs []string, warnings []string) {
	if pathEnv == "" {
		return nil, nil
	}
	parts := strings.Split(pathEnv, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			warnings = append(warnings,
				"empty PATH segment skipped (deliberate: not treated as current directory, unlike POSIX)")
			continue
		}
		if !filepath.IsAbs(p) {
			if abs, err := filepath.Abs(p); err == nil {
				p = abs
			}
		}
		out = append(out, p)
	}
	return out, warnings
}

// Walk scans dirs in order. First occurrence of each base name is active;
// later same-named executables are kept with Shadowed=true.
// Only mode&0111 files are considered (Stat follows symlinks).
// Kind is detected from a 4-byte header; large files are not fully read.
// Execute-only (no read) regular files are still recorded as KindOther and
// occupy the seen name slot. Non-regular files (FIFO/socket/device) are
// never opened. Duplicate directories (symlink or same inode / case-fold)
// are scanned once.
func Walk(dirs []string) (*Result, error) {
	res := &Result{}
	seen := make(map[string]struct{})
	var scannedDirs []dirIdentity

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		// Absolute-ize relative PATH segments (also done in SplitPATHWithWarnings).
		if !filepath.IsAbs(dir) {
			if abs, err := filepath.Abs(dir); err == nil {
				dir = abs
			}
		}

		// Directory-level dedup: EvalSymlinks + SameFile against already scanned.
		if skip, reason := shouldSkipDir(dir, scannedDirs); skip {
			res.Errors = append(res.Errors, reason)
			continue
		}
		id, err := identifyDir(dir)
		if err != nil {
			// Dedup identity failed — still scan, but record the failure.
			res.FileErrors = append(res.FileErrors, FileError{
				Path: dir, Reason: "identify dir: " + err.Error(),
			})
		} else {
			scannedDirs = append(scannedDirs, id)
		}

		entries, err := os.ReadDir(dir)
		if err != nil {
			res.Skipped++
			msg := "readdir " + dir + ": " + err.Error()
			res.Errors = append(res.Errors, msg)
			res.FileErrors = append(res.FileErrors, FileError{Path: dir, Reason: err.Error()})
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
				res.FileErrors = append(res.FileErrors, FileError{Path: full, Reason: err.Error()})
				continue
			}
			if info.IsDir() {
				continue
			}
			// Executable iff any of owner/group/other execute bits is set.
			if info.Mode()&0o111 == 0 {
				continue
			}

			// Non-regular (FIFO/socket/device): never Open — ReadFull can hang.
			if !info.Mode().IsRegular() {
				res.Skipped++
				reason := "non-regular file (not opened): " + info.Mode().Type().String()
				res.FileErrors = append(res.FileErrors, FileError{Path: full, Reason: reason})
				res.Errors = append(res.Errors, full+": "+reason)
				continue
			}

			kind, err := DetectKind(full)
			evidence := ""
			if err != nil {
				// Execute bit set but unreadable: still record, occupy seen slot
				// (matches shell shadowed semantics). Kind=Other with evidence.
				kind = KindOther
				evidence = "unreadable: " + err.Error()
			}

			realPath, err := filepath.EvalSymlinks(full)
			if err != nil {
				realPath = ""
				res.FileErrors = append(res.FileErrors, FileError{
					Path: full, Reason: "eval symlinks: " + err.Error(),
				})
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
				Evidence: evidence,
			})
		}
	}
	return res, nil
}

// dirIdentity holds resolved path + FileInfo for SameFile comparison.
type dirIdentity struct {
	resolved string
	info     os.FileInfo
}

func identifyDir(dir string) (dirIdentity, error) {
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		resolved = filepath.Clean(dir)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		info, err = os.Stat(dir)
		if err != nil {
			return dirIdentity{}, err
		}
		resolved = filepath.Clean(dir)
	}
	return dirIdentity{resolved: resolved, info: info}, nil
}

func shouldSkipDir(dir string, scanned []dirIdentity) (bool, string) {
	id, err := identifyDir(dir)
	if err != nil {
		return false, ""
	}
	for _, prev := range scanned {
		if prev.resolved == id.resolved {
			return true, "duplicate PATH directory skipped (same resolved path): " + dir
		}
		if prev.info != nil && id.info != nil && os.SameFile(prev.info, id.info) {
			return true, "duplicate PATH directory skipped (same file): " + dir
		}
	}
	return false, ""
}

// IsExecutable reports whether mode has any execute bit (owner/group/other).
func IsExecutable(mode os.FileMode) bool {
	return mode&0o111 != 0
}
