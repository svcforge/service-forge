package gateway

import (
	"sync"
	"time"

	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
)

type circuitState int

const (
	stateClosed circuitState = iota
	stateHalfOpen
	stateOpen
)

func (s circuitState) String() string {
	switch s {
	case stateClosed:
		return "closed"
	case stateHalfOpen:
		return "half-open"
	case stateOpen:
		return "open"
	default:
		return "unknown"
	}
}

const (
	defaultBreakerMinRequests      = 20
	defaultBreakerFailureRatio     = 0.5
	defaultBreakerWindow           = 10 * time.Second
	defaultBreakerOpenTimeout      = 5 * time.Second
	defaultBreakerHalfOpenMaxCalls = 1
)

// breakerCounts tracks outcomes within a single generation (window or state).
type breakerCounts struct {
	requests  int
	successes int
	failures  int
}

// circuitBreaker is a lightweight per-route breaker modelled on the classic
// closed/half-open/open state machine. State transitions are time-driven; the
// clock is injectable via now so behaviour is deterministic under test. A nil
// *circuitBreaker means the route has no breaker configured.
type circuitBreaker struct {
	minRequests      int
	failureRatio     float64
	window           time.Duration
	openTimeout      time.Duration
	halfOpenMaxCalls int
	now              func() time.Time

	mu         sync.Mutex
	phase      circuitState
	generation uint64
	counts     breakerCounts
	expiry     time.Time
}

// isBreakerFailure reports whether an attempt error should count against the
// breaker. Only server-side/transport failures trip it; nil errors and
// client-driven errors (INVALID_ARGUMENT, NOT_FOUND, UNAUTHENTICATED, ...) are
// treated as successes so a misbehaving caller cannot open the breaker against
// a healthy upstream.
func isBreakerFailure(err error) bool {
	if err == nil {
		return false
	}
	switch errorCode(err) {
	case sferrors.CodeUnavailable, sferrors.CodeDeadlineExceeded, sferrors.CodeInternal:
		return true
	default:
		return false
	}
}

func newCircuitBreaker(cfg *config.CircuitBreakerConfig) *circuitBreaker {
	if cfg == nil {
		return nil
	}
	b := &circuitBreaker{
		minRequests:      cfg.MinRequests,
		failureRatio:     cfg.FailureRatio,
		window:           cfg.Window,
		openTimeout:      cfg.OpenTimeout,
		halfOpenMaxCalls: cfg.HalfOpenMaxCalls,
		now:              time.Now,
		phase:            stateClosed,
	}
	if b.minRequests <= 0 {
		b.minRequests = defaultBreakerMinRequests
	}
	if b.failureRatio <= 0 {
		b.failureRatio = defaultBreakerFailureRatio
	}
	if b.window <= 0 {
		b.window = defaultBreakerWindow
	}
	if b.openTimeout <= 0 {
		b.openTimeout = defaultBreakerOpenTimeout
	}
	if b.halfOpenMaxCalls <= 0 {
		b.halfOpenMaxCalls = defaultBreakerHalfOpenMaxCalls
	}
	return b
}

// beforeRequest reports whether a call may proceed and returns the generation
// token the caller must pass back to afterRequest. An open breaker, or a
// half-open breaker that already has its probe budget in flight, rejects.
func (b *circuitBreaker) beforeRequest() (uint64, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	state, gen := b.currentState(b.now())
	if state == stateOpen {
		return gen, false
	}
	if state == stateHalfOpen && b.counts.requests >= b.halfOpenMaxCalls {
		return gen, false
	}
	b.counts.requests++
	return gen, true
}

// afterRequest records the outcome of a call. Results from a stale generation
// (the breaker changed state mid-flight) are ignored.
func (b *circuitBreaker) afterRequest(gen uint64, success bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	state, current := b.currentState(now)
	if current != gen {
		return
	}
	if success {
		b.onSuccess(state, now)
		return
	}
	b.onFailure(state, now)
}

func (b *circuitBreaker) state() circuitState {
	b.mu.Lock()
	defer b.mu.Unlock()
	state, _ := b.currentState(b.now())
	return state
}

// currentState applies any pending time-based transition (window reset while
// closed, open->half-open once the open timeout elapses) and returns the live
// state and generation. Caller must hold the lock.
func (b *circuitBreaker) currentState(now time.Time) (circuitState, uint64) {
	switch b.phase {
	case stateClosed:
		if b.expiry.IsZero() {
			b.expiry = now.Add(b.window)
		} else if !b.expiry.After(now) {
			b.toNewGeneration(now)
		}
	case stateOpen:
		if !b.expiry.After(now) {
			b.setState(stateHalfOpen, now)
		}
	}
	return b.phase, b.generation
}

func (b *circuitBreaker) onSuccess(state circuitState, now time.Time) {
	b.counts.successes++
	if state == stateHalfOpen && b.counts.successes >= b.halfOpenMaxCalls {
		b.setState(stateClosed, now)
	}
}

func (b *circuitBreaker) onFailure(state circuitState, now time.Time) {
	switch state {
	case stateClosed:
		b.counts.failures++
		if b.readyToTrip() {
			b.setState(stateOpen, now)
		}
	case stateHalfOpen:
		b.setState(stateOpen, now)
	}
}

func (b *circuitBreaker) readyToTrip() bool {
	if b.counts.requests < b.minRequests {
		return false
	}
	return float64(b.counts.failures)/float64(b.counts.requests) >= b.failureRatio
}

func (b *circuitBreaker) setState(state circuitState, now time.Time) {
	if b.phase == state {
		return
	}
	b.phase = state
	b.toNewGeneration(now)
}

// toNewGeneration resets counters, bumps the generation (invalidating in-flight
// tokens), and arms the expiry appropriate to the new state. Caller holds lock.
func (b *circuitBreaker) toNewGeneration(now time.Time) {
	b.generation++
	b.counts = breakerCounts{}
	switch b.phase {
	case stateClosed:
		b.expiry = now.Add(b.window)
	case stateOpen:
		b.expiry = now.Add(b.openTimeout)
	default:
		b.expiry = time.Time{}
	}
}
