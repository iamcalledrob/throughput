# throughput: limit throughput of an io.Reader / io.Writer

throughput is a simple, performance-minded package to limit the throughput of data that passes through an io.Reader or io.Writer.

Key features:
- **Use any Limiter:** [Limiter](https://pkg.go.dev/github.com/iamcalledrob/throughput#Limiter) is an interface, so any rate-limiting algorithm can be used. An adapter for [rate.Limiter](https://pkg.go.dev/golang.org/x/time/rate#Limiter) is provided.
- **Minimal dependencies:** Only dependency is `golang.org/x/time/rate`
- **Disableable fast path:** [DisableableLimiter](https://pkg.go.dev/github.com/iamcalledrob/throughput#DisableableLimiter) allows the limiter to be disabled whilst leaving it wired in place, with minimal overhead.
- **Limiters can be shared:** The same Limiter can be used across multiple readers or writers -- useful to apply a global rate limit.

![test status](https://github.com/iamcalledrob/throughput/actions/workflows/test.yml/badge.svg)

## Usage

[Godoc](http://pkg.go.dev/github.com/iamcalledrob/throughput)

```import "github.com/iamcalledrob/throughput"```

Simple reader example:
```go
var src io.Reader

// Instantiate a Limiter via convenience function
lim := throughput.NewBytesPerSecLimiter(32*1024)

// reader will read from src, limiting reads by using lim
reader := throughput.NewReader(context.Background(), src, lim)

// Copy will occur at a max of 32kb/sec.
_, _ = io.Copy(io.Discard, reader)
```

Simple writer example:
```go
var dst io.Writer

// Instantiate a Limiter via convenience function
lim := throughput.NewBytesPerSecLimiter(32*1024)

// writer will write to dst, limiting reads by using lim
writer := throughput.NewWriter(context.Background(), dst, lim)

// Copy will occur at a max of 32kb/sec.
_, _ = io.Copy(writer, rand.Reader)
```

## Disableable limiter fast path
You probably don't need this, but it allows for your logic to always have the limiter in place (even when not normally
needed) without impacting performance much.

For example, it allows you to implement an optional rate limit setting, which can be turned off by reconfiguring the
limiter, not all the surrounding code. 

Without fast path:
```go
// Instantiate a "noop" Limiter, using rate.Limiter + rate.Inf, which applies no limit
lim := throughput.NewRateLimiterAdapter(rate.NewLimiter(rate.Inf, 0))
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
// Instantiate a "noop" Limiter, using rate.Limiter + rate.Inf, which applies no limit
lim := throughput.NewRateLimiterAdapter(rate.NewLimiter(rate.Inf, 0))

// Wrap it in DisableableLimiter and disable
lim2 := throughput.NewDisableableLimiter(lim)
lim2.SetEnabled(false)

reader := throughput.NewReader(context.Background(), src, lim2)

// The limiter has a limit of rate.Inf, so no effective limit is applied.
//
// DisableableLimiter will bypass the wrapped limiter -- resulting in minimal overhead.
_, _ = io.Copy(io.Discard, reader)
```

Fast path is ~9x faster than "going through the motions" of using a noop limiter. 
```
cpu: 11th Gen Intel(R) Core(TM) i7-1165G7 @ 2.80GHz
BenchmarkDisableableLimiter
BenchmarkDisableableLimiter/WithoutDisableableLimiter
BenchmarkDisableableLimiter/WithoutDisableableLimiter-8                 37211024        32.52 ns/op
BenchmarkDisableableLimiter/WithDisableableLimiter
BenchmarkDisableableLimiter/WithDisableableLimiter-8                    324741836       3.646 ns/op
```