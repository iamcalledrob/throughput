package throughput

import (
	"context"
	"golang.org/x/time/rate"
	"math"
	"time"
)

// RateLimiterAdapter allows use of a golang.org/x/time/rate.Limiter with Reader and Writer.
//
// rate.Limiter requires that calls to WaitN or ReserveN don't exceed the limiter's burst capacity, but there's
// nothing preventing read and write sizes from being greater than the burst capacity. Therefore, sequential
// reservations may sometimes be required to reach the appropriate wait.
//
// Additionally, the limiter's burst capacity is mutable (SetBurst) and protected internally by a lock. As there's
// no transaction between checking Burst and calling WaitN/ReserveN, extra care is needed.
type RateLimiterAdapter struct {
	lim *rate.Limiter
}

func NewRateLimiterAdapter(lim *rate.Limiter) *RateLimiterAdapter {
	return &RateLimiterAdapter{lim: lim}
}

func (a *RateLimiterAdapter) Wait(ctx context.Context, n int) error {
	// Don't invoke lim.Burst() unless necessary, as it is an additional lock acquisition
	//
	// This allows the happy path to do the minimum amount of locking, which is a consideration due to how
	// hot this code path may be in high throughput scenarios.
	burst := math.MaxInt

	for {
		now := time.Now()
		nn := min(burst, n)

		// ReserveN+timer, because WaitN doesn't provide structured errors.
		res := a.lim.ReserveN(now, nn)
		if !res.OK() {
			// n exceeds burst capacity
			//
			// This should not happen in normal io use-cases, as an individual read/write is likely to be much
			// smaller than the limiter's per-second capacity.
			//
			// Increases won't be picked up until the next call to Wait, as an accepted trade-off.
			burst = a.lim.Burst()
			continue
		}

		if res.Delay() > 0 {
			select {
			case <-time.After(res.DelayFrom(now)):
			case <-ctx.Done():
				res.Cancel()
				return ctx.Err()
			}
		}

		n -= nn

		if n <= 0 {
			return nil
		}
	}
}

var _ Limiter = (*RateLimiterAdapter)(nil)
