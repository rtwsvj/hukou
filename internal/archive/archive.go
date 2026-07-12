// Package archive extracts binary assets from various archive formats.
//
// Supported formats: tar.gz/tgz, zip, single-file gz (renamed to the wanted
// binary name), and bare files. tar.xz is intentionally unsupported because
// the Go standard library does not include an xz decompressor; callers will
// receive a clear error.
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

// Extract unpacks the archive at archivePath into destDir and returns the path
// of the extracted binary.
//
// wantName is the expected binary base name (without directory). Extraction
// picks the archive member using the following precedence:
//   1. An entry whose base name equals wantName or wantName+".exe".
//   2. The sole "executable-looking" entry: its file mode has any executable
//      bit set, or its base name has no extension. If more than one such entry
//      exists, an error listing the candidates is returned.
//
// All entries are checked for directory traversal attacks: the cleaned entry
// path must stay inside destDir.
func Extract(archivePath, destDir, wantName string) (string, error) {
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("create dest dir: %w", err)
	}

	switch {
	case hasExt(archivePath, ".tar.gz"), hasExt(archivePath, ".tgz"):
		return extractTarGz(archivePath, destDir, wantName)
	case hasExt(archivePath, ".tar.xz"):
		return "", errors.New("xz 暂不支持")
	case hasExt(archivePath, ".zip"):
		return extractZip(archivePath, destDir, wantName)
	case hasExt(archivePath, ".gz"):
		return extractGz(archivePath, destDir, wantName)
	default:
		return copyBareFile(archivePath, destDir, wantName)
	}
}

func hasExt(path, ext string) bool {
	return strings.HasSuffix(strings.ToLower(path), ext)
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
		if err := writeTarEntry(tr, destDir, h.Name, h.Mode); err != nil {
			return "", err
		}
		return safeJoin(destDir, h.Name)
	}
}

func writeTarEntry(r *tar.Reader, destDir, name string, mode int64) error {
	outPath, err := safeJoin(destDir, name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(mode)&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, r); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func extractZip(archivePath, destDir, wantName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	var exact *zip.File
	var candidates []*zip.File

	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		name := filepath.Clean(zf.Name)
		base := filepath.Base(name)

		if baseMatches(base, wantName) {
			if exact != nil {
				return "", fmt.Errorf("multiple entries match %q: %q and %q", wantName, exact.Name, zf.Name)
			}
			if err := writeZipEntry(zf, destDir); err != nil {
				return "", err
			}
			exact = zf
			continue
		}

		if isExecutableLike(base, int64(zf.Mode())) {
			candidates = append(candidates, zf)
		}
	}

	if exact != nil {
		out, err := safeJoin(destDir, filepath.Base(exact.Name))
		if err != nil {
			return "", err
		}
		return out, nil
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no binary found in archive")
	}
	if len(candidates) > 1 {
		names := make([]string, len(candidates))
		for i, c := range candidates {
			names[i] = c.Name
		}
		return "", fmt.Errorf("multiple executable candidates: %s", strings.Join(names, ", "))
	}

	if err := writeZipEntry(candidates[0], destDir); err != nil {
		return "", err
	}
	return safeJoin(destDir, filepath.Base(candidates[0].Name))
}

func writeZipEntry(zf *zip.File, destDir string) error {
	outPath, err := safeJoin(destDir, zf.Name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	rc, err := zf.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, zf.Mode()&0o777)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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

	outPath := filepath.Join(destDir, wantName)
	out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, gz); err != nil {
		out.Close()
		return "", err
	}
	return outPath, out.Close()
}

func copyBareFile(archivePath, destDir, wantName string) (string, error) {
	outPath := filepath.Join(destDir, wantName)
	if err := copyFile(archivePath, outPath, 0o755); err != nil {
		return "", err
	}
	return outPath, nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	sf, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sf.Close()

	df, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(df, sf); err != nil {
		df.Close()
		return err
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
