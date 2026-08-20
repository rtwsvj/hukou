//go:build unix

package safeopen

import (
	"os"
	"syscall"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// open uses O_RDONLY|O_NONBLOCK: on a FIFO the open succeeds immediately
// (without waiting for a writer) and the caller's regular-file re-check
// rejects it; on regular files O_NONBLOCK is ignored.
func open(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, i18n.Errorf("open %s: invalid file descriptor", path)
	}
	return f, nil
}
