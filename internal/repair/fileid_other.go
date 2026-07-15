//go:build !unix

package repair

import "os"

// fileID reports no identity on platforms without POSIX stat metadata. The
// remaining size, mode, modification-time, and hash observations still bind a
// clean-live-temps target.
func fileID(_ os.FileInfo) (dev, inode uint64, ok bool) {
	return 0, 0, false
}
