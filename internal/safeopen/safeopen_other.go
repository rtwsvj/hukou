//go:build !unix

package safeopen

import "os"

// open is a plain os.Open: O_NONBLOCK has no portable equivalent off unix, so
// a blocking-device swap can still wedge the open itself. Accepted platform
// limitation, stated honestly: the post-open regular-file re-check in Open
// still applies, and on Windows named pipes live on Win32 device paths rather
// than ordinary filesystem paths, which narrows the practical hazard.
func open(path string) (*os.File, error) {
	return os.Open(path)
}
