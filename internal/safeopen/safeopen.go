// Package safeopen opens files for reading without ever blocking on
// non-regular targets. A stat-then-open sequence races: if the path is
// swapped for a FIFO (or another blocking device node) between the check and
// the open, a plain os.Open wedges the caller forever. On unix the open uses
// O_RDONLY|O_NONBLOCK; off unix there is no portable equivalent, so the open
// stays plain and the post-open regular-file re-check is the only guard (an
// accepted, documented platform limitation — see open_other.go). Either way
// a non-regular target is rejected fail-closed.
package safeopen

import (
	"os"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// Open opens path read-only and verifies it is a regular file. Non-regular
// targets (FIFO, socket, device, directory) are an error, never a block.
func Open(path string) (*os.File, error) {
	f, err := open(path)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, i18n.Errorf("not a regular file: %s", path)
	}
	return f, nil
}
