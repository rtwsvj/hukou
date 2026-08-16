//go:build darwin

package i18n

import (
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
)

// darwinAppleLocale reads the GUI language preference (~/Library/Preferences/
// .GlobalPreferences.plist, a binary plist) with a minimal bplist reader, so
// the UI can follow the macOS system language even when the shell's LANG is
// unset or English (the macOS terminal default). No subprocess, no cgo: the
// repository's execution fence stays intact.
//
// Recognized keys: AppleLocale (string, e.g. "zh_CN") and AppleLanguages
// (array; first element e.g. "zh-Hans-CN").
func darwinAppleLocale() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	payload, err := os.ReadFile(filepath.Join(home, "Library", "Preferences", ".GlobalPreferences.plist"))
	if err != nil {
		return ""
	}
	root, err := parseBplist(payload)
	if err != nil {
		return ""
	}
	dict, ok := root.(bplistDict)
	if !ok {
		return ""
	}
	if loc, ok := dict["AppleLocale"]; ok {
		if s, isStr := loc.(string); isStr && s != "" {
			return s
		}
	}
	if langs, ok := dict["AppleLanguages"]; ok {
		if arr, isArr := langs.([]any); isArr && len(arr) > 0 {
			if s, isStr := arr[0].(string); isStr && s != "" {
				return s
			}
		}
	}
	return ""
}

type bplistDict map[string]any

var errBplist = errors.New("invalid binary plist")

// parseBplist is a minimal binary-plist reader covering the object types
// .GlobalPreferences.plist uses (strings, dicts, arrays, ints, bools). Unknown
// object types fail the whole parse — a preference we do not understand must
// never produce a wrong locale guess.
func parseBplist(data []byte) (any, error) {
	if len(data) < 40 || string(data[:8]) != "bplist00" {
		return nil, errBplist
	}
	trailer := data[len(data)-32:]
	offsetIntSize := int(trailer[6])
	objectRefSize := int(trailer[7])
	numObjects := int(binary.BigEndian.Uint64(trailer[8:16]))
	topObject := int(binary.BigEndian.Uint64(trailer[16:24]))
	offsetTable := int(binary.BigEndian.Uint64(trailer[24:32]))
	if offsetIntSize < 1 || offsetIntSize > 8 || objectRefSize < 1 || objectRefSize > 8 ||
		numObjects < 0 || numObjects > 1<<20 || offsetTable < 0 || offsetTable >= len(data) {
		return nil, errBplist
	}
	offsets := make([]int, numObjects)
	for i := 0; i < numObjects; i++ {
		pos := offsetTable + i*offsetIntSize
		if pos+offsetIntSize > len(data) {
			return nil, errBplist
		}
		offsets[i] = int(readUint(data[pos : pos+offsetIntSize]))
		if offsets[i] < 8 || offsets[i] >= len(data) {
			return nil, errBplist
		}
	}
	if topObject < 0 || topObject >= numObjects {
		return nil, errBplist
	}
	return readBplistObject(data, offsets, topObject)
}

func readBplistObject(data []byte, offsets []int, idx int) (any, error) {
	if idx < 0 || idx >= len(offsets) {
		return nil, errBplist
	}
	pos := offsets[idx]
	if pos >= len(data) {
		return nil, errBplist
	}
	marker := data[pos]
	switch {
	case marker == 0x00:
		return nil, nil
	case marker == 0x08:
		return false, nil
	case marker == 0x09:
		return true, nil
	case marker&0xf0 == 0x10: // integer
		n := 1 << (marker & 0x0f)
		pos++
		if pos+n > len(data) {
			return nil, errBplist
		}
		return int64(readUint(data[pos : pos+n])), nil
	case marker&0xf0 == 0x50: // ASCII string
		n, pos, err := readBplistCount(data, marker, pos)
		if err != nil {
			return nil, err
		}
		if pos+n > len(data) {
			return nil, errBplist
		}
		return string(data[pos : pos+n]), nil
	case marker&0xf0 == 0x60: // UTF-16BE string
		n, pos, err := readBplistCount(data, marker, pos)
		if err != nil {
			return nil, err
		}
		if pos+2*n > len(data) {
			return nil, errBplist
		}
		runes := make([]rune, 0, n)
		for i := 0; i < n; i++ {
			runes = append(runes, rune(binary.BigEndian.Uint16(data[pos+2*i:])))
		}
		return string(runes), nil
	case marker&0xf0 == 0xa0: // array
		count, pos, err := readBplistCount(data, marker, pos)
		if err != nil {
			return nil, err
		}
		refs, err := readBplistRefs(data, offsets, pos, count)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(refs))
		for _, r := range refs {
			obj, err := readBplistObject(data, offsets, r)
			if err != nil {
				return nil, err
			}
			out = append(out, obj)
		}
		return out, nil
	case marker&0xf0 == 0xd0: // dict
		count, pos, err := readBplistCount(data, marker, pos)
		if err != nil {
			return nil, err
		}
		keyRefs, err := readBplistRefs(data, offsets, pos, count)
		if err != nil {
			return nil, err
		}
		pos += count * refSize(offsets, data)
		valRefs, err := readBplistRefs(data, offsets, pos, count)
		if err != nil {
			return nil, err
		}
		out := make(bplistDict, count)
		for i := 0; i < count; i++ {
			k, err := readBplistObject(data, offsets, keyRefs[i])
			if err != nil {
				return nil, err
			}
			key, ok := k.(string)
			if !ok {
				return nil, errBplist
			}
			v, err := readBplistObject(data, offsets, valRefs[i])
			if err != nil {
				return nil, err
			}
			out[key] = v
		}
		return out, nil
	case marker&0xf0 == 0x20: // IEEE real (4/8 bytes): skip the payload
		n := 1 << (marker & 0x0f)
		if n != 4 && n != 8 {
			return nil, errBplist
		}
		return nil, nil
	case marker == 0x33: // date: 8-byte float seconds since 2001
		return nil, nil
	case marker&0xf0 == 0x40: // data blob: skip the payload
		n, pos, err := readBplistCount(data, marker, pos)
		if err != nil {
			return nil, err
		}
		if pos+n > len(data) {
			return nil, errBplist
		}
		return nil, nil
	case marker&0xf0 == 0x80: // uid: (n+1) bytes, or long form
		n := int(marker & 0x0f)
		if n == 0x0f {
			_, _, err := readBplistCount(data, marker, pos)
			if err != nil {
				return nil, err
			}
			return nil, nil
		}
		return nil, nil
	default:
		return nil, errBplist // truly unknown marker
	}
}

func refSize(offsets []int, data []byte) int {
	// The object-reference size lives in the trailer; derivable from data,
	// but offsets are index-based here so use the trailer-derived size passed
	// via closure-free helper: recompute from the file trailer.
	if len(data) < 40 {
		return 0
	}
	return int(data[len(data)-32+7])
}

func readBplistCount(data []byte, marker byte, pos int) (int, int, error) {
	n := int(marker & 0x0f)
	if n != 0x0f {
		return n, pos + 1, nil
	}
	pos++
	if pos >= len(data) || data[pos]&0xf0 != 0x10 {
		return 0, 0, errBplist
	}
	width := 1 << (data[pos] & 0x0f)
	pos++
	if pos+width > len(data) {
		return 0, 0, errBplist
	}
	return int(readUint(data[pos : pos+width])), pos + width, nil
}

func readBplistRefs(data []byte, offsets []int, pos, count int) ([]int, error) {
	size := refSize(offsets, data)
	if size <= 0 || size > 8 || pos+count*size > len(data) {
		return nil, errBplist
	}
	out := make([]int, count)
	for i := 0; i < count; i++ {
		out[i] = int(readUint(data[pos+i*size : pos+(i+1)*size]))
		if out[i] >= len(offsets) {
			return nil, errBplist
		}
	}
	return out, nil
}

func readUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

var _ = math.MaxInt64 // keep math import honest if bounds change

func defaultSystemGUILocale() string { return darwinAppleLocale() }
