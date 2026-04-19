package service

import (
	"log"
	"sync"
	"time"
)

// RateLimiter enforces 3 sliding-window limits simultaneously:
//
//	2  requests / second
//	50 requests / minute
//	300 requests / hour
//
// Call Wait() before every outbound API request. If any window is full,
// Wait sleeps until a slot opens, then claims it before returning.
type RateLimiter struct {
	mu   sync.Mutex
	sec  []time.Time // timestamps inside the last 1 s
	min  []time.Time // timestamps inside the last 1 min
	hour []time.Time // timestamps inside the last 1 hr
}

// limits and window durations — index-aligned.
var (
	limits  = [3]int{2, 50, 300}
	windows = [3]time.Duration{time.Second, time.Minute, time.Hour}
	labels  = [3]string{"second", "minute", "hour"}
)

// NewRateLimiter returns a ready-to-use RateLimiter.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{}
}

// slices returns pointers to the three timestamp slices so the generic
// helpers can operate on all three windows with one loop.
func (rl *RateLimiter) slices() [3]*[]time.Time {
	return [3]*[]time.Time{&rl.sec, &rl.min, &rl.hour}
}

// prune drops timestamps that have fallen outside their window.
// Must be called with rl.mu held.
func prune(ts *[]time.Time, window time.Duration) {
	cutoff := time.Now().Add(-window)
	i := 0
	for i < len(*ts) && (*ts)[i].Before(cutoff) {
		i++
	}
	*ts = (*ts)[i:]
}

// waitUntil returns the earliest time at which the given window will have
// a free slot, assuming ts is already pruned. Returns zero if a slot is
// free right now.
// Must be called with rl.mu held.
func waitUntil(ts []time.Time, limit int, window time.Duration) time.Time {
	if len(ts) < limit {
		return time.Time{} // slot available now
	}
	// The oldest entry will leave the window at: oldest + window.
	return ts[0].Add(window)
}

// Wait blocks until a request slot is available across all three windows,
// then records the timestamp and returns.
func (rl *RateLimiter) Wait() {
	for {
		rl.mu.Lock()

		slices := rl.slices()

		// Prune all windows.
		for i := range windows {
			prune(slices[i], windows[i])
		}

		// Find the latest "ready time" across all windows.
		var readyAt time.Time
		for i := range windows {
			wt := waitUntil(*slices[i], limits[i], windows[i])
			if wt.After(readyAt) {
				readyAt = wt
			}
		}

		if readyAt.IsZero() {
			// All windows have capacity — record and proceed.
			now := time.Now()
			for i := range slices {
				*slices[i] = append(*slices[i], now)
			}
			rl.mu.Unlock()
			return
		}

		// At least one window is full — figure out which one(s) and log.
		for i := range windows {
			if len(*slices[i]) >= limits[i] {
				log.Printf(
					"[rate-limiter] %s window full (%d/%d). waiting %.3fs …",
					labels[i], len(*slices[i]), limits[i],
					time.Until(readyAt).Seconds(),
				)
			}
		}

		rl.mu.Unlock()

		// Sleep until the slot opens, then re-evaluate.
		time.Sleep(time.Until(readyAt))
	}
}
