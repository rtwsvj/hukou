package archive

import "strings"

// Format identifies how a release asset is packaged.
//
// Unknown suffixes intentionally remain FormatBare for backwards compatibility:
// hukou supports release assets that are already standalone executables. Known
// archive formats that cannot be extracted must therefore have an explicit
// format value so callers never mistake them for bare binaries.
type Format uint8

const (
	FormatBare Format = iota
	FormatTarGz
	FormatZip
	FormatGz
	FormatTarXz
	FormatUnsupported
)

// DetectFormat classifies name by its longest recognized, case-insensitive
// suffix. Both .tar.xz and .txz are recognized even though extraction is not
// currently supported.
func DetectFormat(name string) Format {
	lower := strings.ToLower(name)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return FormatTarGz
	case strings.HasSuffix(lower, ".tar.xz"), strings.HasSuffix(lower, ".txz"):
		return FormatTarXz
	case strings.HasSuffix(lower, ".tar.zst"), strings.HasSuffix(lower, ".tzst"),
		strings.HasSuffix(lower, ".tar.bz2"), strings.HasSuffix(lower, ".tbz"), strings.HasSuffix(lower, ".tbz2"),
		strings.HasSuffix(lower, ".tar"), strings.Contains(lower, ".tar."),
		strings.HasSuffix(lower, ".7z"), strings.HasSuffix(lower, ".rar"),
		strings.HasSuffix(lower, ".dmg"), strings.HasSuffix(lower, ".pkg"),
		strings.HasSuffix(lower, ".deb"), strings.HasSuffix(lower, ".rpm"), strings.HasSuffix(lower, ".apk"),
		strings.HasSuffix(lower, ".msi"), strings.HasSuffix(lower, ".iso"), strings.HasSuffix(lower, ".cab"),
		strings.HasSuffix(lower, ".zst"), strings.HasSuffix(lower, ".bz2"), strings.HasSuffix(lower, ".xz"), strings.HasSuffix(lower, ".lz4"):
		return FormatUnsupported
	case strings.HasSuffix(lower, ".zip"):
		return FormatZip
	case strings.HasSuffix(lower, ".gz"):
		return FormatGz
	default:
		return FormatBare
	}
}

// Supported reports whether hukou can install assets in this format.
func (f Format) Supported() bool {
	return f != FormatTarXz && f != FormatUnsupported
}
