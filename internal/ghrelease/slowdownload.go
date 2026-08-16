package ghrelease

import (
	"context"
	"io"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/rtwsvj/hukou/internal/i18n"
)

// Download-speed floor: after a grace period, an attempt whose average
// throughput falls below minDownloadSpeed bytes/sec is aborted and retried
// through the system proxy. Both knobs are environment-overridable for
// tests; HUKOU_DOWNLOAD_MIN_SPEED=0 disables slow detection entirely.
var (
	minDownloadSpeed = func() int64 {
		if v := os.Getenv("HUKOU_DOWNLOAD_MIN_SPEED"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				return n
			}
		}
		return 32 * 1024 // 32 KiB/s
	}()
	downloadGracePeriod = func() time.Duration {
		if v := os.Getenv("HUKOU_DOWNLOAD_GRACE"); v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				return d
			}
		}
		return 5 * time.Second
	}()
)

var errSlowDownload = i18n.Errorf("download stalled below minimum speed")

// speedLimitedReader measures throughput and cancels the download context
// when the average speed stays below the floor after the grace period.
type speedLimitedReader struct {
	r        io.Reader
	mu       sync.Mutex
	n        int64
	start    time.Time
	slow     bool
	grace    time.Duration
	minSpeed int64
	cancel   context.CancelFunc
}

func newSpeedLimitedReader(ctx context.Context, cancel context.CancelFunc, r io.Reader) *speedLimitedReader {
	s := &speedLimitedReader{
		r:        r,
		start:    time.Now(),
		grace:    downloadGracePeriod,
		minSpeed: minDownloadSpeed,
		cancel:   cancel,
	}
	if s.minSpeed > 0 {
		go s.watch(ctx)
	}
	return s
}

func (s *speedLimitedReader) watch(ctx context.Context) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.mu.Lock()
			elapsed := time.Since(s.start)
			n := s.n
			s.mu.Unlock()
			if elapsed < s.grace {
				continue
			}
			if float64(n)/elapsed.Seconds() < float64(s.minSpeed) {
				s.mu.Lock()
				s.slow = true
				s.mu.Unlock()
				s.cancel()
				return
			}
		}
	}
}

func (s *speedLimitedReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.mu.Lock()
	s.n += int64(n)
	s.mu.Unlock()
	return n, err
}

func (s *speedLimitedReader) Slow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slow
}
