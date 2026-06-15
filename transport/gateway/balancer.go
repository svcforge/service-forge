package gateway

import (
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
)

// Supported load-balancing strategies for a pooled proxy route.
const (
	lbRoundRobin = "round_robin"
	lbLeastConn  = "least_conn"
	lbRandom     = "random"
)

// balancer selects one of a route's pooled connections by index. pick returns
// the chosen index in [0,n) and a release callback the caller must invoke when
// the call finishes; release is never nil so callers can defer it
// unconditionally. Implementations must be safe for concurrent use.
type balancer interface {
	pick(n int) (int, func())
}

var noopRelease = func() {}

// buildBalancer maps a strategy name to a balancer, defaulting to round-robin
// for empty or unrecognised values.
func buildBalancer(strategy string) balancer {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case lbLeastConn:
		return &leastConnBalancer{}
	case lbRandom:
		return randomBalancer{}
	default:
		return &roundRobinBalancer{}
	}
}

type roundRobinBalancer struct {
	next atomic.Uint64
}

func (b *roundRobinBalancer) pick(n int) (int, func()) {
	idx := int(b.next.Add(1) % uint64(n))
	return idx, noopRelease
}

type randomBalancer struct{}

func (randomBalancer) pick(n int) (int, func()) {
	return rand.IntN(n), noopRelease
}

// leastConnBalancer routes to the connection with the fewest in-flight calls,
// which behaves better than round-robin when request latencies are uneven (it
// is also why pool_size > 1 hurt under round-robin at low concurrency). The
// in-flight counters are allocated lazily on first use since the pool size is
// not known until connections are dialed.
type leastConnBalancer struct {
	once     sync.Once
	inflight []atomic.Int64
}

func (b *leastConnBalancer) pick(n int) (int, func()) {
	b.once.Do(func() {
		b.inflight = make([]atomic.Int64, n)
	})

	best := 0
	min := b.inflight[0].Load()
	for i := 1; i < n; i++ {
		if v := b.inflight[i].Load(); v < min {
			min = v
			best = i
		}
	}
	b.inflight[best].Add(1)
	return best, func() { b.inflight[best].Add(-1) }
}
