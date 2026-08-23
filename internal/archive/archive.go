// Package archive extracts binary assets from various archive formats.
//
// Supported formats: tar.gz/tgz, zip, single-file gz, tar.xz/txz, single-file
// zst, and bare files. Known-but-unpackable formats (bz2 variants, bare xz,
// tar-over-zstd containers, exotic installers) fail closed with a clear
// error rather than being treated as bare binaries. The xz and zstd codecs
// are admitted under the dependency allowlist in
// docs/adr/ADR-0008-archive-codec-policy.md (reader-only, pure Go).
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"github.com/klauspost/compress/zstd"
	"github.com/rtwsvj/hukou/internal/i18n"
	"github.com/ulikunitz/xz"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxEntryBytes is the maximum size of a single extracted archive entry (512 MiB).
const MaxEntryBytes int64 = 512 << 20

// MaxTotalBytes is the maximum total extracted bytes across all entries (512 MiB).
const MaxTotalBytes int64 = 512 << 20

// MaxHeaderEntries bounds how many non-directory archive members the
// discovery pass will examine (10000). Legitimate release archives carry a
// handful of binaries; anything larger is treated as hostile and refused
// before any body is read.
const MaxHeaderEntries = 10000

// MaxExecCandidates bounds how many executable-like entries the discovery
// pass will accumulate (256). Beyond that the ambiguity itself is treated as
// hostile rather than reported entry by entry.
const MaxExecCandidates = 256

// MaxDeclaredBytes is the maximum cumulative declared uncompressed size of
// the regular entries examined during discovery (4 GiB), taken straight from
// the tar/zip headers. No decompression is needed to know it, so this bounds
// the decompression work a lying-header bomb can force before extraction
// ever starts.
const MaxDeclaredBytes int64 = 4 << 30

// Extract unpacks the archive read from f into destDir and returns the path
// of the extracted binary.
//
// wantName is the expected binary base name (without directory). Extraction
// picks the archive member using the following precedence:
//  1. An entry whose base name equals wantName or wantName+".exe".
//  2. The sole "executable-looking" entry: its file mode has any executable
//     bit set, or its base name has no extension. If more than one such entry
//     exists, an error listing the candidates is returned.
//
// All entries are checked for directory traversal attacks: the cleaned entry
// path must stay inside destDir. Each entry and the total extracted size are
// capped at 512 MiB.
//
// Extract reads exclusively through the provided descriptor: it never opens
// anything by path, so a caller can verify the bytes by hashing the same
// handle and be certain extraction consumed exactly those bytes (no
// pathname-reopen race between verification and extraction). Multi-pass
// formats re-read the SAME descriptor after Seek(0); on return the offset is
// left wherever the last pass ended, so callers must Seek(0) before reading
// again. The caller retains ownership of f and closes it.
func Extract(f *os.File, destDir, wantName string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", i18n.Wrapf("create dest dir: %w", err)
	}

	switch DetectFormat(f.Name()) {
	case FormatTarGz:
		return extractTarGz(f, destDir, wantName)
	case FormatTarXz:
		return extractTarXz(f, destDir, wantName)
	case FormatZst:
		return extractZst(f, destDir, wantName)
	case FormatUnsupported:
		return "", i18n.Errorf("unsupported archive format: %s", filepath.Base(f.Name()))
	case FormatZip:
		return extractZip(f, destDir, wantName)
	case FormatGz:
		return extractGz(f, destDir, wantName)
	default:
		return copyBareFile(f, destDir, wantName)
	}
}

// safeJoin returns target if it is strictly inside destDir after cleaning.
// It rejects absolute paths, paths escaping destDir, and symlinks are not
// resolved (the caller controls destDir).
func safeJoin(destDir, target string) (string, error) {
	if filepath.IsAbs(target) {
		return "", i18n.Errorf("absolute archive entry: %q", target)
	}
	clean := filepath.Clean(filepath.Join(destDir, target))
	prefix := destDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if clean != destDir && !strings.HasPrefix(clean, prefix) {
		return "", i18n.Errorf("path traversal attempt: %q", target)
	}
	return clean, nil
}

func extractTarGz(f *os.File, destDir, wantName string) (string, error) {
	// First pass: inspect headers without writing anything.
	target, err := findTarTarget(f, wantName)
	if err != nil {
		return "", err
	}

	// Second pass over the SAME descriptor: rewind and extract the chosen
	// entry without ever reopening the archive by path.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", i18n.Wrapf("rewind archive: %w", err)
	}
	return extractTarEntry(f, destDir, target)
}

// extractTarXz extracts a tar archive compressed with xz (.tar.xz/.txz).
//
// Like extractTarGz this is two passes over the SAME verified descriptor,
// but xz readers are single-shot: once a pass has drained the stream to EOF
// the decoder cannot be rewound, so pass 2 seeks back to offset 0 and opens
// a fresh decoder. Both passes still consume only the bytes already hashed
// by the caller — nothing is reopened by path.
func extractTarXz(f *os.File, destDir, wantName string) (string, error) {
	// First pass: inspect headers without writing anything.
	target, err := findTarTargetXz(f, wantName)
	if err != nil {
		return "", err
	}

	// Second pass: rewind and extract the chosen entry with a fresh xz
	// stream.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", i18n.Wrapf("rewind archive: %w", err)
	}
	return extractTarEntryXz(f, destDir, target)
}

// extractZst decodes a single-file zstd stream (.zst/.tzst) and renames the
// decoded output to wantName, mirroring extractGz for the zstd codec
// admitted by docs/adr/ADR-0008-archive-codec-policy.md.
func extractZst(f *os.File, destDir, wantName string) (string, error) {
	zr, err := zstd.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open zstd: %w", err)
	}
	defer zr.Close()

	outPath, err := safeJoin(destDir, wantName)
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(zr, MaxEntryBytes+1))
	if err != nil {
		out.Close()
		os.Remove(outPath)
		return "", err
	}
	if n > MaxEntryBytes {
		out.Close()
		os.Remove(outPath)
		return "", i18n.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return "", err
	}
	return outPath, nil
}

type tarEntry struct {
	name string
	mode int64
}

func findTarTarget(f *os.File, wantName string) (string, error) {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open gzip: %w", err)
	}
	defer gz.Close()
	return findTarTargetStream(gz, wantName)
}

// findTarTargetXz runs the discovery pass over an xz-compressed tar stream.
// The decoder is single-shot, but that is fine here: discovery reads each
// byte exactly once.
func findTarTargetXz(f *os.File, wantName string) (string, error) {
	xr, err := xz.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open xz: %w", err)
	}
	return findTarTargetStream(xr, wantName)
}

// findTarTargetStream applies the documented selection rule (exact basename
// match first, then the sole executable-looking entry) to a decompressed
// tar stream. It enforces every discovery budget: MaxHeaderEntries,
// MaxDeclaredBytes, and MaxExecCandidates.
func findTarTargetStream(r io.Reader, wantName string) (string, error) {
	// Discovery-pass budgets: refuse pathological archives before any body
	// is read. Fail closed, naming the violated budget.
	var examined int
	var declared int64

	tr := tar.NewReader(r)

	var exact *tarEntry
	var candidates []tarEntry
	// MaxExecCandidates overflow is recorded, not fatal: the documented
	// selection rule gives an exact basename match precedence over the
	// executable-candidate heuristic, so the scan continues and the budget
	// only fails the archive when no exact match exists.
	candidateOverflow := false

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", i18n.Wrapf("read tar header: %w", err)
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		examined++
		if examined > MaxHeaderEntries {
			return "", i18n.Errorf("archive member budget exceeded: more than %d entries examined", MaxHeaderEntries)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		declared += h.Size
		if declared < 0 || declared > MaxDeclaredBytes {
			return "", i18n.Errorf("archive declared-size budget exceeded: entries declare %d total bytes, limit is %d", declared, MaxDeclaredBytes)
		}

		name := filepath.Clean(h.Name)
		base := filepath.Base(name)

		if baseMatches(base, wantName) {
			if exact != nil {
				return "", i18n.Errorf("multiple entries match %q: %q and %q", wantName, exact.name, h.Name)
			}
			cp := tarEntry{name: h.Name, mode: h.Mode}
			exact = &cp
			continue
		}
		if isExecutableLike(base, h.Mode) {
			if len(candidates) >= MaxExecCandidates {
				candidateOverflow = true
				continue
			}
			candidates = append(candidates, tarEntry{name: h.Name, mode: h.Mode})
		}
	}

	if exact != nil {
		return exact.name, nil
	}
	if candidateOverflow {
		return "", i18n.Errorf("archive candidate budget exceeded: more than %d executable-looking entries", MaxExecCandidates)
	}

	if len(candidates) == 0 {
		return "", i18n.Errorf("no binary found in archive")
	}
	if len(candidates) > 1 {
		return "", i18n.Errorf("multiple executable candidates: %s", formatNames(candidates))
	}
	return candidates[0].name, nil
}

func extractTarEntry(f *os.File, destDir, wantEntry string) (string, error) {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open gzip: %w", err)
	}
	defer gz.Close()
	return extractTarEntryStream(gz, destDir, wantEntry)
}

// extractTarEntryXz extracts the chosen entry from an xz-compressed tar
// stream using a fresh decoder; see extractTarXz for why pass 2 cannot
// reuse pass 1's reader.
func extractTarEntryXz(f *os.File, destDir, wantEntry string) (string, error) {
	xr, err := xz.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open xz: %w", err)
	}
	return extractTarEntryStream(xr, destDir, wantEntry)
}

// extractTarEntryStream walks a decompressed tar stream and writes exactly
// one regular entry — the one matching wantEntry — through writeLimited,
// which enforces safeJoin and the per-entry/total byte caps.
func extractTarEntryStream(r io.Reader, destDir, wantEntry string) (string, error) {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", i18n.Errorf("entry %q disappeared", wantEntry)
		}
		if err != nil {
			return "", i18n.Wrapf("read tar header: %w", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		if filepath.Clean(h.Name) != filepath.Clean(wantEntry) {
			continue
		}
		// Extracted files are normalized to 0o755: the archive's own mode bits
		// (including any write/setuid games) are never trusted.
		if _, err := writeLimited(tr, destDir, h.Name, 0o755, 0); err != nil {
			return "", err
		}
		return safeJoin(destDir, h.Name)
	}
}

func extractZip(f *os.File, destDir, wantName string) (string, error) {
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	zr, err := zip.NewReader(f, info.Size())
	if err != nil {
		return "", i18n.Wrapf("open zip: %w", err)
	}

	target, err := findZipTarget(zr.File, wantName)
	if err != nil {
		return "", err
	}

	outPath, _, err := writeZipEntryLimited(target, destDir, 0)
	if err != nil {
		return "", err
	}
	return outPath, nil
}

// findZipTarget inspects ZIP metadata without writing anything. Selection and
// extraction are intentionally separate so ambiguous archives cannot leave
// partially extracted files behind.
func findZipTarget(files []*zip.File, wantName string) (*zip.File, error) {
	// Discovery-pass budgets, mirroring the tar path: refuse pathological
	// archives from central-directory metadata alone, before any body read.
	var examined int
	var declared uint64
	var exact *zip.File
	var candidates []*zip.File
	// Deferred like the tar path: candidate overflow must not mask a later
	// exact basename match.
	zipCandidateOverflow := false

	for _, zf := range files {
		if zf.FileInfo().IsDir() {
			continue
		}
		examined++
		if examined > MaxHeaderEntries {
			return nil, i18n.Errorf("archive member budget exceeded: more than %d entries examined", MaxHeaderEntries)
		}
		if zf.Mode()&os.ModeSymlink != 0 {
			continue
		}
		declared += zf.UncompressedSize64
		if declared > uint64(MaxDeclaredBytes) {
			return nil, i18n.Errorf("archive declared-size budget exceeded: entries declare %d total bytes, limit is %d", declared, uint64(MaxDeclaredBytes))
		}
		name := filepath.Clean(filepath.FromSlash(zf.Name))
		base := filepath.Base(name)

		if baseMatches(base, wantName) {
			if exact != nil {
				return nil, i18n.Errorf("multiple entries match %q: %q and %q", wantName, exact.Name, zf.Name)
			}
			exact = zf
			continue
		}

		if isExecutableLike(base, int64(zf.Mode())) {
			if len(candidates) >= MaxExecCandidates {
				// Deferred, not fatal: an exact basename match later in
				// the central directory still wins (documented precedence).
				zipCandidateOverflow = true
				continue
			}
			candidates = append(candidates, zf)
		}
	}

	if exact != nil {
		return exact, nil
	}
	if zipCandidateOverflow {
		return nil, i18n.Errorf("archive candidate budget exceeded: more than %d executable-looking entries", MaxExecCandidates)
	}

	if len(candidates) == 0 {
		return nil, i18n.Errorf("no binary found in archive")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name
		}
		return nil, i18n.Errorf("multiple executable candidates: %s", strings.Join(names, ", "))
	}
	return candidates[0], nil
}

func writeZipEntryLimited(zf *zip.File, destDir string, totalSoFar int64) (string, int64, error) {
	rc, err := zf.Open()
	if err != nil {
		return "", 0, err
	}
	defer rc.Close()
	name := filepath.FromSlash(zf.Name)
	// Mode normalized to 0o755 like the tar path; the archive's bits are
	// never trusted.
	n, err := writeLimited(rc, destDir, name, 0o755, totalSoFar)
	if err != nil {
		return "", 0, err
	}
	outPath, err := safeJoin(destDir, name)
	if err != nil {
		return "", 0, err
	}
	return outPath, n, nil
}

// writeLimited copies r into destDir/name with per-entry and total size caps.
// totalSoFar is the number of bytes already extracted in this archive call.
func writeLimited(r io.Reader, destDir, name string, mode os.FileMode, totalSoFar int64) (int64, error) {
	outPath, err := safeJoin(destDir, name)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return 0, err
	}
	remainingTotal := MaxTotalBytes - totalSoFar
	if remainingTotal <= 0 {
		return 0, i18n.Errorf("extract total size limit of %d bytes exceeded", MaxTotalBytes)
	}
	limit := MaxEntryBytes
	if remainingTotal < limit {
		limit = remainingTotal
	}

	mode &= 0o777
	if mode&0o111 == 0 {
		mode |= 0o111
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, io.LimitReader(r, limit+1))
	if err != nil {
		out.Close()
		os.Remove(outPath)
		return 0, err
	}
	if n > limit {
		out.Close()
		os.Remove(outPath)
		if limit < MaxEntryBytes {
			return 0, i18n.Errorf("extract total size limit of %d bytes exceeded", MaxTotalBytes)
		}
		return 0, i18n.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return 0, err
	}
	return n, nil
}

func extractGz(f *os.File, destDir, wantName string) (string, error) {
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", i18n.Wrapf("open gzip: %w", err)
	}
	defer gz.Close()

	outPath, err := safeJoin(destDir, wantName)
	if err != nil {
		return "", err
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(out, io.LimitReader(gz, MaxEntryBytes+1))
	if err != nil {
		out.Close()
		os.Remove(outPath)
		return "", err
	}
	if n > MaxEntryBytes {
		out.Close()
		os.Remove(outPath)
		return "", i18n.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return "", err
	}
	return outPath, nil
}

func copyBareFile(f *os.File, destDir, wantName string) (string, error) {
	outPath, err := safeJoin(destDir, wantName)
	if err != nil {
		return "", err
	}
	if err := copyFileLimited(f, outPath, 0o755); err != nil {
		return "", err
	}
	return outPath, nil
}

func copyFileLimited(src io.Reader, dst string, perm os.FileMode) error {
	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(df, io.LimitReader(src, MaxEntryBytes+1))
	if err != nil {
		df.Close()
		os.Remove(dst)
		return err
	}
	if n > MaxEntryBytes {
		df.Close()
		os.Remove(dst)
		return i18n.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	return df.Close()
}

func baseMatches(base, wantName string) bool {
	if base == wantName {
		return true
	}
	return base == wantName+".exe"
}

func isExecutableLike(base string, mode int64) bool {
	if mode&0o111 != 0 {
		return true
	}
	return !strings.Contains(base, ".")
}

func formatNames(candidates []tarEntry) string {
	names := make([]string, len(candidates))
	for i, c := range candidates {
		names[i] = c.name
	}
	return strings.Join(names, ", ")
}
