package throughput

import (
	"context"
	"fmt"
	"golang.org/x/time/rate"
	"io"
)

// Limiter is the rate limiter algorithm to use. Based on the design of golang.org/x/time/rate.Limiter, which
// implements this interface as-is.
type Limiter interface {
	Burst() int
	WaitN(ctx context.Context, n int) error
}

// Enabler can optionally be implemented by a Limiter to allow for toggling a fast path at runtime if the limiter
// is not enabled.
type Enabler interface {
	Enabled() bool
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
// If the limiter implements Enabler, then a fast path will be taken when Enabled() returns false.
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
// If the limiter implements Enabler, then a fast path will be taken when Enabled() returns false.
func NewWriter(ctx context.Context, dst io.Writer, lim Limiter) *Writer {
	return &Writer{
		ctx: ctx,
		dst: dst,
		lim: lim,
	}
}

// Read reads bytes into p. Takes a fast path if the limiter is nil to reduce overhead in scenarios where the
// limiter may be turned on or off during the life of the Reader.
func (s *Reader) Read(p []byte) (n int, err error) {
	// Fast path
	// Overhead of interface type assertion here is ~1ns, so no real benefit to doing this ahead of time.
	if e, ok := s.lim.(Enabler); ok && !e.Enabled() {
		return s.src.Read(p)
	}

	// Don't attempt to read more than the limiter's burst capacity, as the limiter will never be
	// able to satisfy a single request for this many tokens.
	//
	// Avoids "Wait(n=X) exceeds limiter's burst Y" error below
	burst := s.lim.Burst()
	if len(p) > burst {
		p = p[:burst]
	}

	n, err = s.src.Read(p)
	if err != nil {
		return n, err
	}

	// Wait must occur after Read, as n is unknown until Read has occurred
	err = s.lim.WaitN(s.ctx, n)
	if err != nil {
		err = fmt.Errorf("waiting after reading %d bytes: %w", n, err)
		return n, err
	}
	return n, nil
}

// Write writes bytes from p.
func (s *Writer) Write(p []byte) (n int, err error) {
	// Fast path
	// Overhead of interface type assertion here is ~1ns, so no real benefit to doing this ahead of time.
	if e, ok := s.lim.(Enabler); ok && !e.Enabled() {
		return s.dst.Write(p)
	}

	burst := s.lim.Burst()

	// The limiter may not have capacity for all of p, but io.Writer must not perform short writes
	// (unlike io.Reader, which can). Chunk into smaller writes.
	var nn int
	for {
		// Avoid exceeding limiter's burst capacity - don't wait for more tokens than it's possible to have.
		p2 := p[:min(len(p), burst)]

		err = s.lim.WaitN(s.ctx, len(p2))
		if err != nil {
			err = fmt.Errorf("waiting to write %d bytes: %w", len(p2), err)
			return
		}

		nn, err = s.dst.Write(p2)
		n += nn

		if err != nil {
			return
		}

		// p = remaining
		p = p[nn:]

		if len(p) == 0 {
			return
		}
	}
}

// NewBytesPerSecLimiter is a convenience function to create a rate.Limiter token bucket to allow bytesPerSec.
//
// By default, the bucket begins full. So NewBytesPerSecLimiter(1024) would allow 1024 bytes at 0s, then another
// 1024 bytes at 1s, 2s, and so on. This can be counterintuitive in tests because time has slop, and measuring writes
// within the first second might count two writes (0s, 1s)
func NewBytesPerSecLimiter(bytesPerSec int64) *rate.Limiter {
	return rate.NewLimiter(rate.Limit(bytesPerSec), int(bytesPerSec))
}
