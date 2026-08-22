//go:build unix

package cmd

import (
	"os"
	"syscall"
)

// ownerOfFileInfo reports the uid that owns fi on platforms that expose it
// (every unix); callers treat a negative result as "ownership unknown".
func ownerOfFileInfo(fi os.FileInfo) int {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return int(st.Uid)
	}
	return -1
}
