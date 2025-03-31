# throughput: limit throughput of an io.Reader / io.Writer

throughput is a very simple package to allow a [rate.Limiter](https://pkg.go.dev/golang.org/x/time/rate) to control
the throughput of data that passes through an io.Reader or io.Writer

Key features:
- **Optional fast path:** Disable the limiter whilst leaving it wired in place, without impacting performance.
- **Limiters can be shared:** The same limiter can be used across multiple readers or writers -- useful to apply a global rate limit.
- **Pluggable:** Limiter is an interface, so any algorithm can be used.
- **Minimal dependencies:** Only dependency is `golang.org/x/time/rate`

![test status](https://github.com/iamcalledrob/throughput/actions/workflows/test.yml/badge.svg)

## Usage

[Godoc](http://pkg.go.dev/github.com/iamcalledrob/throughput)

```import "github.com/iamcalledrob/throughput"```

Reader example:
```go
var src io.Reader

// Instantiate a rate.Limiter
lim := throughput.NewBytesPerSecLimiter(32*1024)

// reader will read from src, limiting reads by using lim
reader := throughput.NewReader(context.Background(), src, lim)

// Copy will occur at a max of 32kb/sec.
_, _ = io.Copy(io.Discard, reader)
```

Writer example:
```go
var dst io.Writer

// Instantiate a rate.Limiter
lim := throughput.NewBytesPerSecLimiter(32*1024)

// writer will write to dst, limiting reads by using lim
writer := throughput.NewWriter(context.Background(), dst, lim)

// Copy will occur at a max of 32kb/sec.
_, _ = io.Copy(writer, rand.Reader)
```

## Fast path
You probably don't need the fast path, but it allows for your logic to always have the limiter in place (even when not
normally needed) without impacting performance much.

For example, it allows you to implement an optional rate limit setting, which can be turned off by reconfiguring the
limiter, not all the surrounding code. 

Without fast path:
```go
// Instantiate a rate.Limiter. rate.Inf applies no limit
lim := rate.NewLimiter(rate.Inf, 0)
reader := throughput.NewReader(context.Background(), src, lim)

// The limiter has a limit of rate.Inf, so no effective limit is applied.
//
// However, each read from reader will "go through the motions" of consulting the
// rate limiter, and depending on the limiter implementation this may require
// locking or allocations.
_, _ = io.Copy(io.Discard, reader)
```

Using fast path:
```go
// Implement a Limiter that also implements the Enableable interface
type EnableableLimiter struct {
    *rate.Limiter
    enabled atomic.Bool
}

func (e *EnableableLimiter) SetEnabled(value bool) {
    e.enabled.Store(value)
}

func (e *EnableableLimiter) Enabled() bool {
    return e.enabled.Load()
}

lim := &EnableableLimiter{Limiter: rate.NewLimiter(rate.Inf, 0)}
reader := throughput.NewReader(context.Background(), src, lim)

// The limiter has a limit of rate.Inf, so no effective limit is applied.
//
// As lim is now an Enabler, each read from reader will consult Enabled() and skip any
// rate limiting logic if Enabled() returns false -- resulting in minimal overhead.
_, _ = io.Copy(io.Discard, reader)
```

Fast path is ~18x faster than "going through the motions" of using a noop limiter. 
```
cpu: 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz
BenchmarkFastPath
BenchmarkFastPath/WithoutFastPath
BenchmarkFastPath/WithoutFastPath-8             15971251                75.38 ns/op
BenchmarkFastPath/WithFastPath
BenchmarkFastPath/WithFastPath-8                234451333                5.192 ns/op
```