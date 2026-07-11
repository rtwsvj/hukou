package scan

import (
	"encoding/binary"
	"io"
	"os"
)

// Mach-O and fat magic numbers (host and opposite byte order).
const (
	magicMachO32  uint32 = 0xfeedface
	magicMachO64  uint32 = 0xfeedfacf
	magicMachOFat uint32 = 0xcafebabe
	// Opposite-endian variants (file written on opposite-endian host).
	magicMachO32Swap  uint32 = 0xcefaedfe
	magicMachO64Swap  uint32 = 0xcffaedfe
	magicMachOFatSwap uint32 = 0xbebafeca
	// ELF magic: 0x7f 'E' 'L' 'F'
	magicELF0 = 0x7f
	magicELF1 = 'E'
	magicELF2 = 'L'
	magicELF3 = 'F'
)

// DetectKind reads at most the first 4 bytes of path and classifies the binary.
// Unreadable files return KindOther and a non-nil error so callers can skip/count.
func DetectKind(path string) (BinKind, error) {
	f, err := os.Open(path)
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

	// Mach-O (32/64) and fat, both endians — match magic as big-endian word
	// or little-endian word so we cover 0xfeedface / 0xcefaedfe etc.
	be := binary.BigEndian.Uint32(hdr[:])
	le := binary.LittleEndian.Uint32(hdr[:])
	if isMachOMagic(be) || isMachOMagic(le) {
		return KindMachO, nil
	}
	// Also accept raw constant equality for documented magic values
	// (covers fat 0xcafebabe / 0xbebafeca regardless of host endianness).
	if be == magicMachO32 || be == magicMachO64 || be == magicMachOFat ||
		be == magicMachO32Swap || be == magicMachO64Swap || be == magicMachOFatSwap ||
		le == magicMachO32 || le == magicMachO64 || le == magicMachOFat ||
		le == magicMachO32Swap || le == magicMachO64Swap || le == magicMachOFatSwap {
		return KindMachO, nil
	}

	return KindOther, nil
}

func isMachOMagic(m uint32) bool {
	switch m {
	case magicMachO32, magicMachO64, magicMachOFat,
		magicMachO32Swap, magicMachO64Swap, magicMachOFatSwap:
		return true
	default:
		return false
	}
}
