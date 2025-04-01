package throughput

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"github.com/dustin/go-humanize"
	"golang.org/x/time/rate"
	"io"
	"testing"
	"time"
)

var rates = []int{
	16,               // 16B/sec
	32 * 1024,        // 32KB/sec
	64 * 1024 * 1024, // 64MB/sec
}

var sizes = []int{
	1024,
	16 * 1024,
	1024 * 1024,
}

func TestRead(t *testing.T) {
	// Ensures read size exceeding the bps limit still works
	// Ensures all read sizes deplete the limiter in the same way
	for _, readSize := range sizes {
		for _, limit := range rates {
			t.Run(fmt.Sprintf("Limit=%s, ReadSize=%s", humanize.IBytes(uint64(limit)), humanize.IBytes(uint64(readSize))), func(t *testing.T) {
				testRead(t, 3*time.Second, readSize, limit)
			})
		}
	}
}

func TestSharedLimiter(t *testing.T) {
	bytesPerSecLimit := 256 * 1024

	lim := depletedLimiter(bytesPerSecLimit)

	n := 4
	expectedBytesPerSec := bytesPerSecLimit / n

	// Note: Fairness is dictated by how the limiter responds to WaitN.
	// Using a large bytesPerSecLimit, small readSize and a longer test duration helps with balance as it leads to
	// more requests to the limiter for each test, which gives more opportunities for balance
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func() {
			testReadWithLimiter(t, lim, 16*time.Second, 32, expectedBytesPerSec)
			done <- struct{}{}
		}()
	}

	for i := 0; i < n; i++ {
		<-done
	}
}

func testRead(t *testing.T, duration time.Duration, readSize int, bytesPerSecLimit int) {
	lim := depletedLimiter(bytesPerSecLimit)
	testReadWithLimiter(t, lim, duration, readSize, bytesPerSecLimit)
}

// Read a fixed amount of data, measure how long it took and compare
// By taking limiter and expectedBytesPerSec, it's possible to test multiple readers of a shared limiter.
func testReadWithLimiter(t *testing.T, lim Limiter, expectedDuration time.Duration, readSize int, expectedBytesPerSec int) {
	count := int64((expectedDuration.Seconds() + 0.0) * float64(expectedBytesPerSec))
	r := NewReader(context.Background(), &nopReader{}, lim)

	start := time.Now()
	_, err := io.CopyBuffer(io.Discard, io.LimitReader(r, count), make([]byte, readSize))
	if err != nil {
		t.Fatalf("copy: %s", err)
	}
	elapsed := time.Since(start)

	// Ensure limiter didn't allow more or less than expected
	// Allow 5% slop each way to account for overhead of copying the bytes and for a small concurrency
	// inaccuracy in rate.Limiter due to clock drift. golang/go#65508
	slop := expectedDuration / 20
	err = verifyWithSlop(elapsed, expectedDuration, slop)
	if err != nil {
		t.Error(err.Error())
	}

	// Don't bother verifying read data, as the read code doesn't take multiple passes
}

func TestWrite(t *testing.T) {
	for _, writeSize := range sizes {
		for _, limit := range rates {
			t.Run(fmt.Sprintf("Limit=%s, WriteSize=%s", humanize.IBytes(uint64(limit)), humanize.IBytes(uint64(writeSize))), func(t *testing.T) {
				testWrite(t, 3*time.Second, writeSize, limit)
			})
		}
	}
}

func testWrite(t *testing.T, expectedDuration time.Duration, writeSize int, bytesPerSecLimit int) {
	count := int64(expectedDuration.Seconds() * float64(bytesPerSecLimit))

	// Pre-populate and pre-allocate because rand.Reader is slow
	want := make([]byte, count)
	_, _ = io.ReadFull(rand.Reader, want)

	got := bytes.NewBuffer(make([]byte, 0, len(want)))

	lim := depletedLimiter(bytesPerSecLimit)
	w := NewWriter(context.Background(), got, lim)

	start := time.Now()
	_, err := io.CopyBuffer(w, bytes.NewReader(want), make([]byte, writeSize))
	if err != nil {
		t.Fatalf("copy: %s", err)
	}
	elapsed := time.Since(start)

	// Ensure limiter didn't allow more or less than expected
	// Allow 5% slop each way to account for overhead of copying the bytes and for a small concurrency
	// inaccuracy in rate.Limiter due to clock drift. golang/go#65508
	slop := expectedDuration / 20
	err = verifyWithSlop(elapsed, expectedDuration, slop)
	if err != nil {
		t.Errorf(err.Error())
	}

	// Ensure written data is not corrupted by chunking logic in Write
	if !bytes.Equal(want, got.Bytes()) {
		t.Errorf("bytes written (%d) not equal to bytes read (%d)", len(want), got.Len())
	}
}

func benchmarkRead(b *testing.B, lim Limiter) {
	r := NewReader(context.Background(), &nopReader{}, lim)

	p := make([]byte, 1024)
	for i := 0; i < b.N; i++ {
		_, _ = r.Read(p)
	}
}

func BenchmarkDisableableLimiter(b *testing.B) {
	b.Run("WithoutDisableableLimiter", func(b *testing.B) {
		lim := NewRateLimiterAdapter(rate.NewLimiter(rate.Inf, 0))
		benchmarkRead(b, lim)
	})
	b.Run("WithDisableableLimiter", func(b *testing.B) {
		lim := NewRateLimiterAdapter(rate.NewLimiter(rate.Inf, 0))
		lim2 := NewDisableableLimiter(lim)
		lim2.SetEnabled(false)
		benchmarkRead(b, lim2)
	})
}

// Depleted limiter removes initial burst capacity, which is easier to reason about for tests.
// This is because the # of bytes allowed would equal the limit * secs, rather than being off-by-one
// due to the initial burst.
func depletedLimiter(limit int) *RateLimiterAdapter {
	lim := NewBytesPerSecLimiter(int64(limit))
	lim.AllowN(time.Now(), limit)
	return NewRateLimiterAdapter(lim)
}

func verifyWithSlop(actual time.Duration, expected time.Duration, slop time.Duration) error {
	if actual > expected+slop {
		return fmt.Errorf("actual duration %.2s longer than expected + slop", actual)
	}
	if actual < expected-slop {
		return fmt.Errorf("actual duration %.2s shorter than expected - slop", actual)
	}
	return nil
}

type nopReader struct{}

func (r *nopReader) Read(p []byte) (n int, err error) {
	return len(p), nil
}
