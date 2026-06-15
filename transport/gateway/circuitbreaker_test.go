package gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/svcforge/service-forge/core/config"
	"google.golang.org/grpc/codes"
)

// fakeClock is a manually advanced clock for deterministic breaker tests.
type fakeClock struct {
	t time.Time
}

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestBreaker(clock *fakeClock, cfg *config.CircuitBreakerConfig) *circuitBreaker {
	b := newCircuitBreaker(cfg)
	b.now = clock.now
	return b
}

// drive runs n calls with the given success flag through the breaker, recording
// each result. It returns how many calls the breaker actually admitted.
func drive(b *circuitBreaker, n int, success bool) int {
	admitted := 0
	for i := 0; i < n; i++ {
		gen, ok := b.beforeRequest()
		if !ok {
			continue
		}
		admitted++
		b.afterRequest(gen, success)
	}
	return admitted
}

func TestBreakerNilWhenDisabled(t *testing.T) {
	if b := newCircuitBreaker(nil); b != nil {
		t.Fatalf("expected nil breaker for nil config, got %#v", b)
	}
}

func TestBreakerStaysClosedBelowMinRequests(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{MinRequests: 4, FailureRatio: 0.5})

	// 3 failures, below MinRequests of 4: must not trip.
	drive(b, 3, false)
	if got := b.state(); got != stateClosed {
		t.Fatalf("state = %v, want closed", got)
	}
	if _, ok := b.beforeRequest(); !ok {
		t.Fatal("breaker rejected call while still closed")
	}
}

func TestBreakerTripsOnFailureRatio(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{MinRequests: 4, FailureRatio: 0.5})

	drive(b, 4, false)
	if got := b.state(); got != stateOpen {
		t.Fatalf("state = %v, want open", got)
	}
	if _, ok := b.beforeRequest(); ok {
		t.Fatal("breaker admitted call while open")
	}
}

func TestBreakerDoesNotTripWhenMostlySuccessful(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{MinRequests: 4, FailureRatio: 0.5})

	drive(b, 6, true)
	drive(b, 1, false)
	if got := b.state(); got != stateClosed {
		t.Fatalf("state = %v, want closed", got)
	}
}

func TestBreakerHalfOpensAfterTimeout(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{
		MinRequests: 4, FailureRatio: 0.5, OpenTimeout: 5 * time.Second,
	})

	drive(b, 4, false)
	if b.state() != stateOpen {
		t.Fatal("breaker did not open")
	}

	clock.advance(5 * time.Second)
	if got := b.state(); got != stateHalfOpen {
		t.Fatalf("state = %v, want half-open", got)
	}
	if _, ok := b.beforeRequest(); !ok {
		t.Fatal("breaker rejected probe in half-open")
	}
}

func TestBreakerClosesAfterProbeSucceeds(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{
		MinRequests: 4, FailureRatio: 0.5, OpenTimeout: 5 * time.Second,
	})

	drive(b, 4, false)
	clock.advance(5 * time.Second)

	gen, ok := b.beforeRequest()
	if !ok {
		t.Fatal("probe not admitted")
	}
	b.afterRequest(gen, true)
	if got := b.state(); got != stateClosed {
		t.Fatalf("state = %v, want closed after successful probe", got)
	}
}

func TestBreakerReopensAfterProbeFails(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{
		MinRequests: 4, FailureRatio: 0.5, OpenTimeout: 5 * time.Second,
	})

	drive(b, 4, false)
	clock.advance(5 * time.Second)

	gen, ok := b.beforeRequest()
	if !ok {
		t.Fatal("probe not admitted")
	}
	b.afterRequest(gen, false)
	if got := b.state(); got != stateOpen {
		t.Fatalf("state = %v, want open after failed probe", got)
	}
}

func TestBreakerLimitsHalfOpenCalls(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{
		MinRequests: 4, FailureRatio: 0.5, OpenTimeout: 5 * time.Second, HalfOpenMaxCalls: 1,
	})

	drive(b, 4, false)
	clock.advance(5 * time.Second)

	if _, ok := b.beforeRequest(); !ok {
		t.Fatal("first probe should be admitted")
	}
	if _, ok := b.beforeRequest(); ok {
		t.Fatal("second concurrent probe should be rejected in half-open")
	}
}

func TestBreakerWindowResetsCounts(t *testing.T) {
	clock := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clock, &config.CircuitBreakerConfig{
		MinRequests: 4, FailureRatio: 0.5, Window: 10 * time.Second,
	})

	// 3 failures, then window elapses: counts should reset so the breaker never
	// trips on stale failures.
	drive(b, 3, false)
	clock.advance(10 * time.Second)
	drive(b, 1, false)
	if got := b.state(); got != stateClosed {
		t.Fatalf("state = %v, want closed (counts reset across window)", got)
	}
}

func TestCircuitBreakerOpensAndShortCircuits(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 1000, codes.Unavailable)

	route := healthRoute(target)
	route.CircuitBreaker = &config.CircuitBreakerConfig{MinRequests: 4, FailureRatio: 0.5}
	gw := newHealthGateway(t, route)

	// Drive enough failing calls to trip the breaker.
	for i := 0; i < 4; i++ {
		if status := doHealthRequest(t, gw); status != http.StatusServiceUnavailable {
			t.Fatalf("call %d status = %d, want 503", i, status)
		}
	}
	callsAtTrip := impl.calls.Load()

	// Once open, further calls are short-circuited without touching the backend.
	if status := doHealthRequest(t, gw); status != http.StatusServiceUnavailable {
		t.Fatalf("short-circuit status = %d, want 503", status)
	}
	if got := impl.calls.Load(); got != callsAtTrip {
		t.Fatalf("backend calls = %d after trip, want %d (short-circuited)", got, callsAtTrip)
	}
}

func TestCircuitBreakerIgnoresClientErrors(t *testing.T) {
	registerHealthProxyInvoker(t)
	impl, target := startFlakyHealthServer(t, 1000, codes.InvalidArgument)

	route := healthRoute(target)
	route.CircuitBreaker = &config.CircuitBreakerConfig{MinRequests: 3, FailureRatio: 0.5}
	gw := newHealthGateway(t, route)

	// Client errors must not trip the breaker, so every call reaches the backend.
	for i := 0; i < 6; i++ {
		if status := doHealthRequest(t, gw); status != http.StatusBadRequest {
			t.Fatalf("call %d status = %d, want 400", i, status)
		}
	}
	if got := impl.calls.Load(); got != 6 {
		t.Fatalf("backend calls = %d, want 6 (breaker stayed closed)", got)
	}
}
