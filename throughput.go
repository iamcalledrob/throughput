package throughput

import (
	"context"
	"fmt"
	"golang.org/x/time/rate"
	"io"
	"sync/atomic"
)

// Limiter allows for any rate-limiting algorithm to be used with Reader and Writer.
// RateLimiterAdapter implements Limiter and allows for a stdlib rate.Limiter to be used.
type Limiter interface {
	// Wait should delay its return based on n bytes of usage. n should be unbounded.
	Wait(ctx context.Context, n int) error
}

type Reader struct {
	ctx context.Context
	src io.Reader
	lim Limiter
}

type Writer struct {
	ctx context.Context
	dst io.Writer
	lim Limiter
}

// NewReader returns an io.Reader that reads from src and is rate-limited by lim.
// The context is used to unblock calls to Read when rate-limited.
// A limiter can be shared across multiple readers.
func NewReader(ctx context.Context, src io.Reader, lim Limiter) *Reader {
	return &Reader{
		ctx: ctx,
		src: src,
		lim: lim,
	}
}

// NewWriter returns an io.Writer that writes into dst and is rate-limited by lim.
// The context is used to unblock calls to Write when rate-limited.
// A limiter can be shared across multiple writers.
func NewWriter(ctx context.Context, dst io.Writer, lim Limiter) *Writer {
	return &Writer{
		ctx: ctx,
		dst: dst,
		lim: lim,
	}
}

func (s *Reader) Read(p []byte) (n int, err error) {
	n, err = s.src.Read(p)
	if err != nil {
		return
	}

	// Wait must occur after Read, as n is unknown until Read has occurred
	err = s.lim.Wait(s.ctx, n)
	if err != nil {
		err = fmt.Errorf("waiting after reading %d bytes: %w", n, err)
		return
	}
	return
}

func (s *Writer) Write(p []byte) (n int, err error) {
	n, err = s.dst.Write(p)
	if err != nil {
		return
	}

	// Wait occurs after Write for consistency with Read.
	err = s.lim.Wait(s.ctx, n)
	if err != nil {
		err = fmt.Errorf("waiting after writing %d bytes: %w", n, err)
		return
	}
	return
}

// NewBytesPerSecLimiter is a convenience function to create a rate.Limiter token bucket to allow bytesPerSec.
//
// By default, the bucket begins full. So NewBytesPerSecLimiter(1024) would allow 1024 bytes at 0s, then another
// 1024 bytes at 1s, 2s, and so on. This can be counterintuitive in tests because time has slop, and measuring writes
// within the first second might count two writes (0s, 1s)
func NewBytesPerSecLimiter(bytesPerSec int64) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec))
}

// DisableableLimiter implements a fast path to bypass the wrapped Limiter.
// Depending on the Limiter used, this may be much more performant than setting an infinite rate limit.
type DisableableLimiter struct {
	disabled atomic.Bool
	Limiter
}

func NewDisableableLimiter(wrapping Limiter) *DisableableLimiter {
	return &DisableableLimiter{Limiter: wrapping}
}

func (e *DisableableLimiter) Wait(ctx context.Context, n int) error {
	if e.disabled.Load() {
		return nil
	}

	return e.Limiter.Wait(ctx, n)
}

func (e *DisableableLimiter) SetEnabled(enabled bool) {
	e.disabled.Store(!enabled)
}
