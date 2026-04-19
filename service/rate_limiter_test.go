package service

import (
	"sync"
	"testing"
	"time"
)

// TestRateLimiter_SecondWindow verifies the 2 req/sec cap.
// We fire 4 calls and expect the total wall time to be ≥ 1 second because
// the 3rd call must wait for the first slot to leave the 1-second window.
func TestRateLimiter_SecondWindow(t *testing.T) {
	rl := NewRateLimiter()

	start := time.Now()
	for i := 0; i < 4; i++ {
		rl.Wait()
	}
	elapsed := time.Since(start)

	if elapsed < time.Second {
		t.Errorf("expected ≥1s for 4 calls at 2 req/sec, got %v", elapsed)
	}
}

// TestRateLimiter_Concurrent verifies no data races and correct blocking
// when multiple goroutines call Wait() simultaneously.
func TestRateLimiter_Concurrent(t *testing.T) {
	rl := NewRateLimiter()

	const numGoroutines = 6
	var wg sync.WaitGroup
	timestamps := make([]time.Time, numGoroutines)

	start := time.Now()
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rl.Wait()
			timestamps[idx] = time.Now()
		}(i)
	}
	wg.Wait()
	elapsed := time.Since(start)

	// 6 calls at 2/sec needs at least 2 full seconds
	// (calls 1-2 free, calls 3-4 wait ~1s, calls 5-6 wait ~2s).
	if elapsed < 2*time.Second {
		t.Errorf("expected ≥2s for 6 concurrent calls at 2 req/sec, got %v", elapsed)
	}
}

// TestRateLimiter_NoExceedPerSecond records timestamps after each Wait()
// and verifies that within any 1-second bucket no more than 2 calls completed.
func TestRateLimiter_NoExceedPerSecond(t *testing.T) {
	rl := NewRateLimiter()

	const calls = 6
	done := make([]time.Time, calls)
	for i := 0; i < calls; i++ {
		rl.Wait()
		done[i] = time.Now()
	}

	// For each call, count how many others completed within the same second window.
	for i, ts := range done {
		count := 0
		for _, other := range done {
			if other.After(ts.Add(-time.Second)) && !other.After(ts) {
				count++
			}
		}
		if count > 2 {
			t.Errorf("call %d: %d calls completed within its trailing 1s window (limit 2)", i, count)
		}
	}
}

// TestRateLimiter_FirstTwoCalls ensures the first 2 calls return immediately
// (no sleeping when the window is empty).
func TestRateLimiter_FirstTwoCalls(t *testing.T) {
	rl := NewRateLimiter()

	start := time.Now()
	rl.Wait()
	rl.Wait()
	elapsed := time.Since(start)

	// Should be well under 500ms — the window is empty so no sleep occurs.
	if elapsed > 500*time.Millisecond {
		t.Errorf("first 2 calls should be instant, took %v", elapsed)
	}
}
