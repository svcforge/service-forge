package gateway

import (
	"net/http"
	"testing"
)

func TestBuildBalancerDefaultsToRoundRobin(t *testing.T) {
	for _, strategy := range []string{"", "round_robin", "unknown", "  RoundRobin  "} {
		if _, ok := buildBalancer(strategy).(*roundRobinBalancer); !ok {
			t.Fatalf("strategy %q did not build a round-robin balancer", strategy)
		}
	}
}

func TestRoundRobinCyclesAcrossConns(t *testing.T) {
	b := buildBalancer("round_robin")
	got := make([]int, 0, 6)
	for i := 0; i < 6; i++ {
		idx, release := b.pick(3)
		release()
		got = append(got, idx)
	}
	// The counter starts at 1 (matching the previous atomic-add behaviour).
	want := []int{1, 2, 0, 1, 2, 0}
	for i, idx := range got {
		if idx != want[i] {
			t.Fatalf("pick sequence = %v, want %v", got, want)
		}
	}
}

func TestRandomStaysInRange(t *testing.T) {
	b := buildBalancer("random")
	seen := map[int]bool{}
	for i := 0; i < 200; i++ {
		idx, release := b.pick(4)
		release()
		if idx < 0 || idx >= 4 {
			t.Fatalf("idx = %d out of range [0,4)", idx)
		}
		seen[idx] = true
	}
	if len(seen) < 2 {
		t.Fatalf("random balancer only ever picked %v", seen)
	}
}

func TestLeastConnPicksFewestInflight(t *testing.T) {
	b := buildBalancer("least_conn")

	// All zero: ties resolve to the lowest index.
	i0, r0 := b.pick(3) // inflight [1,0,0]
	i1, _ := b.pick(3)  // inflight [1,1,0]
	i2, _ := b.pick(3)  // inflight [1,1,1]
	if i0 != 0 || i1 != 1 || i2 != 2 {
		t.Fatalf("initial picks = %d,%d,%d, want 0,1,2", i0, i1, i2)
	}

	// Releasing slot 0 makes it the least loaded again.
	r0()
	i3, _ := b.pick(3) // inflight [0,1,1] -> picks 0
	if i3 != 0 {
		t.Fatalf("after release, pick = %d, want 0", i3)
	}
}

func TestPooledRouteWithLeastConn(t *testing.T) {
	registerHealthProxyInvoker(t)
	server, target := startHealthServer(t)
	defer server.Stop()

	route := healthRoute(target)
	route.PoolSize = 3
	route.LoadBalance = "least_conn"
	gw := newHealthGateway(t, route)

	// Exercise the n>1 balancer path end-to-end; every call must succeed and
	// release its in-flight slot so the pool keeps serving.
	for i := 0; i < 10; i++ {
		if status := doHealthRequest(t, gw); status != http.StatusOK {
			t.Fatalf("call %d status = %d, want 200", i, status)
		}
	}
}
