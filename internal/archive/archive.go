// Package archive extracts binary assets from various archive formats.
//
// Supported formats: tar.gz/tgz, zip, single-file gz (renamed to the wanted
// binary name), and bare files. tar.xz/txz are intentionally unsupported
// because the Go standard library does not include an xz decompressor; callers
// receive a clear error rather than treating either format as a bare binary.
package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MaxEntryBytes is the maximum size of a single extracted archive entry (512 MiB).
const MaxEntryBytes int64 = 512 << 20

// MaxTotalBytes is the maximum total extracted bytes across all entries (512 MiB).
const MaxTotalBytes int64 = 512 << 20

// Extract unpacks the archive at archivePath into destDir and returns the path
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
func Extract(archivePath, destDir, wantName string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create dest dir: %w", err)
	}

	switch DetectFormat(archivePath) {
	case FormatTarGz:
		return extractTarGz(archivePath, destDir, wantName)
	case FormatTarXz:
		return "", errors.New("xz 暂不支持（.tar.xz/.txz）")
	case FormatUnsupported:
		return "", fmt.Errorf("unsupported archive format: %s", filepath.Base(archivePath))
	case FormatZip:
		return extractZip(archivePath, destDir, wantName)
	case FormatGz:
		return extractGz(archivePath, destDir, wantName)
	default:
		return copyBareFile(archivePath, destDir, wantName)
	}
}

// safeJoin returns target if it is strictly inside destDir after cleaning.
// It rejects absolute paths, paths escaping destDir, and symlinks are not
// resolved (the caller controls destDir).
func safeJoin(destDir, target string) (string, error) {
	if filepath.IsAbs(target) {
		return "", fmt.Errorf("absolute archive entry: %q", target)
	}
	clean := filepath.Clean(filepath.Join(destDir, target))
	prefix := destDir
	if !strings.HasSuffix(prefix, string(filepath.Separator)) {
		prefix += string(filepath.Separator)
	}
	if clean != destDir && !strings.HasPrefix(clean, prefix) {
		return "", fmt.Errorf("path traversal attempt: %q", target)
	}
	return clean, nil
}

func extractTarGz(archivePath, destDir, wantName string) (string, error) {
	// First pass: inspect headers without writing anything.
	target, err := findTarTarget(archivePath, wantName)
	if err != nil {
		return "", err
	}

	// Second pass: extract the chosen entry.
	return extractTarEntry(archivePath, destDir, target)
}

type tarEntry struct {
	name string
	mode int64
}

func findTarTarget(archivePath, wantName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)

	var exact *tarEntry
	var candidates []tarEntry

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar header: %w", err)
		}

		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}

		name := filepath.Clean(h.Name)
		base := filepath.Base(name)

		if baseMatches(base, wantName) {
			if exact != nil {
				return "", fmt.Errorf("multiple entries match %q: %q and %q", wantName, exact.name, h.Name)
			}
			cp := tarEntry{name: h.Name, mode: h.Mode}
			exact = &cp
			continue
		}

		if isExecutableLike(base, h.Mode) {
			candidates = append(candidates, tarEntry{name: h.Name, mode: h.Mode})
		}
	}

	if exact != nil {
		return exact.name, nil
	}
	if len(candidates) == 0 {
		return "", fmt.Errorf("no binary found in archive")
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf("multiple executable candidates: %s", formatNames(candidates))
	}
	return candidates[0].name, nil
}

func extractTarEntry(archivePath, destDir, wantEntry string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("entry %q disappeared", wantEntry)
		}
		if err != nil {
			return "", fmt.Errorf("read tar header: %w", err)
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeRegA {
			continue
		}
		if filepath.Clean(h.Name) != filepath.Clean(wantEntry) {
			continue
		}
		if _, err := writeLimited(tr, destDir, h.Name, os.FileMode(h.Mode)&0o777, 0); err != nil {
			return "", err
		}
		return safeJoin(destDir, h.Name)
	}
}

func extractZip(archivePath, destDir, wantName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

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
	var exact *zip.File
	var candidates []*zip.File

	for _, zf := range files {
		if zf.FileInfo().IsDir() {
			continue
		}
		if zf.Mode()&os.ModeSymlink != 0 {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(zf.Name))
		base := filepath.Base(name)

		if baseMatches(base, wantName) {
			if exact != nil {
				return nil, fmt.Errorf("multiple entries match %q: %q and %q", wantName, exact.Name, zf.Name)
			}
			exact = zf
			continue
		}

		if isExecutableLike(base, int64(zf.Mode())) {
			candidates = append(candidates, zf)
		}
	}

	if exact != nil {
		return exact, nil
	}

	if len(candidates) == 0 {
		return nil, fmt.Errorf("no binary found in archive")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name
		}
		return nil, fmt.Errorf("multiple executable candidates: %s", strings.Join(names, ", "))
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
	n, err := writeLimited(rc, destDir, name, zf.Mode()&0o777, totalSoFar)
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
		return 0, fmt.Errorf("extract total size limit of %d bytes exceeded", MaxTotalBytes)
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
			return 0, fmt.Errorf("extract total size limit of %d bytes exceeded", MaxTotalBytes)
		}
		return 0, fmt.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return 0, err
	}
	return n, nil
}

func extractGz(archivePath, destDir, wantName string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("open gzip: %w", err)
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
		return "", fmt.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
	}
	if err := out.Close(); err != nil {
		os.Remove(outPath)
		return "", err
	}
	return outPath, nil
}

func copyBareFile(archivePath, destDir, wantName string) (string, error) {
	outPath, err := safeJoin(destDir, wantName)
	if err != nil {
		return "", err
	}
	if err := copyFileLimited(archivePath, outPath, 0o755); err != nil {
		return "", err
	}
	return outPath, nil
}

func copyFileLimited(src, dst string, perm os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	n, err := io.Copy(df, io.LimitReader(sf, MaxEntryBytes+1))
	if err != nil {
		df.Close()
		os.Remove(dst)
		return err
	}
	if n > MaxEntryBytes {
		df.Close()
		os.Remove(dst)
		return fmt.Errorf("archive entry exceeds size limit of %d bytes", MaxEntryBytes)
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
