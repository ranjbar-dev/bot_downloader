package bot

import (
	"sync"
	"time"
)

// simple fixed-window per-user rate limiter.
type rateLimiter struct {
	max    int
	window time.Duration
	mu     sync.Mutex
	hits   map[int64][]time.Time
}

func newRateLimiter(max int, window time.Duration) *rateLimiter {
	return &rateLimiter{max: max, window: window, hits: make(map[int64][]time.Time)}
}

func (r *rateLimiter) allow(userID int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-r.window)
	kept := r.hits[userID][:0]
	for _, t := range r.hits[userID] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= r.max {
		r.hits[userID] = kept
		return false
	}
	r.hits[userID] = append(kept, now)
	return true
}
