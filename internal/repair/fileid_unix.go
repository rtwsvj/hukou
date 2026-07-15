//go:build unix

package repair

import (
	"os"
	"syscall"
)

// fileID returns the device and inode identity of an observed file when the
// platform exposes POSIX stat metadata. Both release platforms (darwin and
// linux) do; clean-live-temps binds this identity into every planned deletion.
func fileID(info os.FileInfo) (dev, inode uint64, ok bool) {
	stat, isStat := info.Sys().(*syscall.Stat_t)
	if !isStat || stat == nil {
		return 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Ino), true
}
