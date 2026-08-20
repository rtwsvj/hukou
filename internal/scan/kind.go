package scan

import (
	"encoding/binary"
	"io"
	"os"

	"github.com/rtwsvj/hukou/internal/safeopen"
)

// Mach-O and fat magic numbers (host and opposite byte order).
const (
	magicMachO32    uint32 = 0xfeedface
	magicMachO64    uint32 = 0xfeedfacf
	magicMachOFat   uint32 = 0xcafebabe // FAT_MAGIC
	magicMachOFat64 uint32 = 0xcafebabf // FAT_MAGIC_64
	// Opposite-endian variants (file written on opposite-endian host).
	magicMachO32Swap    uint32 = 0xcefaedfe
	magicMachO64Swap    uint32 = 0xcffaedfe
	magicMachOFatSwap   uint32 = 0xbebafeca // FAT_CIGAM
	magicMachOFat64Swap uint32 = 0xbfbafeca // FAT_CIGAM_64
	// ELF magic: 0x7f 'E' 'L' 'F'
	magicELF0 = 0x7f
	magicELF1 = 'E'
	magicELF2 = 'L'
	magicELF3 = 'F'
	// nfat_arch above this is treated as non-Mach-O (e.g. Java class 0xcafebabe).
	maxReasonableNFatArch = 128
)

// DetectKind reads at most the first 8 bytes of path and classifies the binary.
// Unreadable files return KindOther and a non-nil error so callers can skip/count.
// The open goes through safeopen so a FIFO swapped in after the walker's stat
// fails closed instead of blocking the scan forever.
func DetectKind(path string) (BinKind, error) {
	f, err := safeopen.Open(path)
	if err != nil {
		return KindOther, err
	}
	defer f.Close()

	var hdr [4]byte
	n, err := io.ReadFull(f, hdr[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return KindOther, err
	}
	if n < 2 {
		return KindOther, nil
	}

	// Script: shebang
	if n >= 2 && hdr[0] == '#' && hdr[1] == '!' {
		return KindScript, nil
	}

	if n < 4 {
		return KindOther, nil
	}

	// ELF
	if hdr[0] == magicELF0 && hdr[1] == magicELF1 && hdr[2] == magicELF2 && hdr[3] == magicELF3 {
		return KindELF, nil
	}

	// Mach-O (32/64) and fat, both endians.
	be := binary.BigEndian.Uint32(hdr[:])
	le := binary.LittleEndian.Uint32(hdr[:])
	if isMachOThinMagic(be) || isMachOThinMagic(le) {
		return KindMachO, nil
	}
	// Fat / Java 0xcafebabe disambiguation via nfat_arch (bytes 5–8).
	if isMachOFatMagic(be) || isMachOFatMagic(le) {
		return classifyFatOrJava(f, be, le)
	}

	return KindOther, nil
}

// classifyFatOrJava reads nfat_arch after a fat-looking magic.
// Real fat binaries have a small arch count; values above 128 are treated as
// non-Mach-O (Java class files also use 0xcafebabe).
// Endian of nfat_arch follows the on-disk magic (BE magic → BE field).
func classifyFatOrJava(f *os.File, be, _ uint32) (BinKind, error) {
	var nfatBuf [4]byte
	n, err := io.ReadFull(f, nfatBuf[:])
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return KindOther, err
	}
	if n < 4 {
		// Truncated header: not a reliable fat binary.
		return KindOther, nil
	}
	var nfat uint32
	switch be {
	case magicMachOFat, magicMachOFat64:
		nfat = binary.BigEndian.Uint32(nfatBuf[:])
	case magicMachOFatSwap, magicMachOFat64Swap:
		// Opposite-endian fat: fields are little-endian on disk.
		nfat = binary.LittleEndian.Uint32(nfatBuf[:])
	default:
		// Magic matched via little-endian view of the same 4 bytes.
		nfat = binary.BigEndian.Uint32(nfatBuf[:])
	}
	if nfat > maxReasonableNFatArch {
		return KindOther, nil
	}
	return KindMachO, nil
}

func isMachOThinMagic(m uint32) bool {
	switch m {
	case magicMachO32, magicMachO64,
		magicMachO32Swap, magicMachO64Swap:
		return true
	default:
		return false
	}
}

func isMachOFatMagic(m uint32) bool {
	switch m {
	case magicMachOFat, magicMachOFat64,
		magicMachOFatSwap, magicMachOFat64Swap:
		return true
	default:
		return false
	}
}

func isMachOMagic(m uint32) bool {
	return isMachOThinMagic(m) || isMachOFatMagic(m)
}
