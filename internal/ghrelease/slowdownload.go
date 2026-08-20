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

// Download-speed floor: after a grace period, an attempt whose throughput
// over a sliding window (downloadWindow, default 10s) falls below
// minDownloadSpeed bytes/sec is aborted and retried through the system proxy.
// The window — not a whole-attempt average — is what catches a fast-then-
// stalled connection: once the burst ages out of the window the measured
// speed collapses and the stall is detected within one window period. Both
// knobs are environment-overridable for tests; HUKOU_DOWNLOAD_MIN_SPEED=0
// disables slow detection entirely.
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
	// downloadWindow is the sliding measurement window. A package var so tests
	// can shrink it; production keeps the default.
	downloadWindow = 10 * time.Second
)

var errSlowDownload = i18n.Errorf("download stalled below minimum speed")

// speedLimitedReader measures throughput over a sliding window and cancels
// the download context when the windowed speed stays below the floor after
// the grace period.
type speedLimitedReader struct {
	r        io.Reader
	mu       sync.Mutex
	start    time.Time
	slow     bool
	grace    time.Duration
	minSpeed int64
	window   time.Duration
	chunks   []readChunk
	cancel   context.CancelFunc
}

// readChunk is one Read's byte count with its timestamp, the raw material of
// the sliding window.
type readChunk struct {
	t time.Time
	n int64
}

func newSpeedLimitedReader(ctx context.Context, cancel context.CancelFunc, r io.Reader) *speedLimitedReader {
	s := &speedLimitedReader{
		r:        r,
		start:    time.Now(),
		grace:    downloadGracePeriod,
		minSpeed: minDownloadSpeed,
		window:   downloadWindow,
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
			speed := 0.0
			if elapsed >= s.grace {
				speed = s.windowSpeedLocked(time.Now())
			}
			s.mu.Unlock()
			if elapsed < s.grace {
				continue
			}
			if speed < float64(s.minSpeed) {
				s.mu.Lock()
				s.slow = true
				s.mu.Unlock()
				s.cancel()
				return
			}
		}
	}
}

// windowSpeedLocked computes bytes/second over the trailing window, evicting
// aged-out chunks as a side effect. For attempts younger than the window the
// denominator is the attempt age (not the full window) so a healthy young
// download is never under-measured. Caller holds mu.
func (s *speedLimitedReader) windowSpeedLocked(now time.Time) float64 {
	cutoff := now.Add(-s.window)
	var sum int64
	kept := s.chunks[:0]
	for _, c := range s.chunks {
		if c.t.After(cutoff) {
			sum += c.n
			kept = append(kept, c)
		}
	}
	s.chunks = kept
	span := now.Sub(s.start).Seconds()
	if span > s.window.Seconds() {
		span = s.window.Seconds()
	}
	if span <= 0 {
		return 0
	}
	return float64(sum) / span
}

func (s *speedLimitedReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.mu.Lock()
		s.chunks = append(s.chunks, readChunk{t: time.Now(), n: int64(n)})
		s.mu.Unlock()
	}
	return n, err
}

func (s *speedLimitedReader) Slow() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.slow
}
