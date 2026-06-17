package gateway

import (
	"context"
	"testing"

	"github.com/golang-jwt/jwt/v5"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"google.golang.org/grpc/metadata"
)

func TestBuildClaimForwarder(t *testing.T) {
	t.Run("nil when unconfigured", func(t *testing.T) {
		if buildClaimForwarder(nil) != nil {
			t.Fatal("expected nil forwarder for empty config")
		}
		if buildClaimForwarder([]config.ClaimForwardConfig{{Claim: "  "}}) != nil {
			t.Fatal("expected nil forwarder when only blank claims are given")
		}
	})

	t.Run("defaults header to lowercased claim", func(t *testing.T) {
		f := buildClaimForwarder([]config.ClaimForwardConfig{
			{Claim: "sub"},
			{Claim: "role", Header: "X-Role"},
		})
		if f == nil || len(f.pairs) != 2 {
			t.Fatalf("expected 2 pairs, got %+v", f)
		}
		if f.pairs[0] != (claimPair{claim: "sub", header: "sub"}) {
			t.Fatalf("unexpected pair: %+v", f.pairs[0])
		}
		if f.pairs[1] != (claimPair{claim: "role", header: "x-role"}) {
			t.Fatalf("unexpected pair: %+v", f.pairs[1])
		}
	})
}

func TestClaimForwarderOutgoing(t *testing.T) {
	f := buildClaimForwarder([]config.ClaimForwardConfig{
		{Claim: "sub", Header: "x-user-id"},
		{Claim: "tenant"},
		{Claim: "admin", Header: "x-admin"},
		{Claim: "exp", Header: "x-exp"},
		{Claim: "roles", Header: "x-roles"}, // non-scalar, skipped
		{Claim: "missing", Header: "x-missing"},
	})

	claims := jwt.MapClaims{
		"sub":    "user-42",
		"tenant": "acme",
		"admin":  true,
		"exp":    float64(1700000000),
		"roles":  []any{"a", "b"},
	}
	lookup := func(key string) any {
		if key == claimsLocal {
			return claims
		}
		return nil
	}

	ctx, err := f.outgoing(context.Background(), lookup)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}
	want := map[string]string{
		"x-user-id": "user-42",
		"tenant":    "acme",
		"x-admin":   "true",
		"x-exp":     "1700000000",
	}
	for key, value := range want {
		if got := md.Get(key); len(got) != 1 || got[0] != value {
			t.Errorf("metadata[%q] = %v, want [%q]", key, got, value)
		}
	}
	if got := md.Get("x-roles"); len(got) != 0 {
		t.Errorf("non-scalar claim should be skipped, got %v", got)
	}
	if got := md.Get("x-missing"); len(got) != 0 {
		t.Errorf("absent claim should be skipped, got %v", got)
	}
}

func TestClaimForwarderOutgoingNoOp(t *testing.T) {
	ctx := context.Background()
	noClaims := func(string) any { return nil }

	mustMetadata := func(t *testing.T, c context.Context, err error) metadata.MD {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		md, ok := metadata.FromOutgoingContext(c)
		if !ok {
			t.Fatal("expected outgoing metadata")
		}
		return md
	}

	t.Run("nil forwarder passes ctx through", func(t *testing.T) {
		var f *claimForwarder
		got, err := f.outgoing(ctx, noClaims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := metadata.FromOutgoingContext(got); ok {
			t.Fatal("nil forwarder must not attach metadata")
		}
	})

	t.Run("optional claim missing is skipped", func(t *testing.T) {
		f := buildClaimForwarder([]config.ClaimForwardConfig{{Claim: "sub"}})
		got, err := f.outgoing(ctx, noClaims)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := metadata.FromOutgoingContext(got); ok {
			t.Fatal("missing optional claim must not attach metadata")
		}
	})

	t.Run("accepts plain map claims", func(t *testing.T) {
		f := buildClaimForwarder([]config.ClaimForwardConfig{{Claim: "sub", Header: "x-user-id"}})
		lookup := func(string) any { return map[string]any{"sub": "u1"} }
		got, err := f.outgoing(ctx, lookup)
		md := mustMetadata(t, got, err)
		if md.Get("x-user-id")[0] != "u1" {
			t.Fatalf("expected x-user-id=u1, got %v", md)
		}
	})
}

func TestClaimForwarderRequired(t *testing.T) {
	ctx := context.Background()
	f := buildClaimForwarder([]config.ClaimForwardConfig{
		{Claim: "sub", Header: "x-user-id", Required: true},
	})

	t.Run("missing required claim is rejected", func(t *testing.T) {
		_, err := f.outgoing(ctx, func(string) any { return nil })
		appErr, ok := err.(*sferrors.AppError)
		if !ok {
			t.Fatalf("expected *sferrors.AppError, got %T (%v)", err, err)
		}
		if appErr.Code != sferrors.CodeUnauthenticated {
			t.Fatalf("expected UNAUTHENTICATED, got %v", appErr.Code)
		}
	})

	t.Run("present required claim passes", func(t *testing.T) {
		lookup := func(string) any { return jwt.MapClaims{"sub": "u1"} }
		ctx2, err := f.outgoing(ctx, lookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		md, _ := metadata.FromOutgoingContext(ctx2)
		if md.Get("x-user-id")[0] != "u1" {
			t.Fatalf("expected x-user-id=u1, got %v", md)
		}
	})
}

func TestClaimForwarderDefault(t *testing.T) {
	ctx := context.Background()
	anon := "anonymous"
	static := "svcforge"
	f := buildClaimForwarder([]config.ClaimForwardConfig{
		{Claim: "sub", Header: "x-user-id", Default: &anon},
		{Claim: "sub", Header: "x-gateway", Default: &static, Required: true}, // default wins over required
	})

	got, err := f.outgoing(ctx, func(string) any { return nil })
	if err != nil {
		t.Fatalf("default must not be rejected: %v", err)
	}
	md, ok := metadata.FromOutgoingContext(got)
	if !ok {
		t.Fatal("expected outgoing metadata")
	}
	if md.Get("x-user-id")[0] != anon {
		t.Errorf("x-user-id = %v, want %q", md.Get("x-user-id"), anon)
	}
	if md.Get("x-gateway")[0] != static {
		t.Errorf("x-gateway = %v, want %q", md.Get("x-gateway"), static)
	}

	t.Run("present claim overrides default", func(t *testing.T) {
		lookup := func(string) any { return jwt.MapClaims{"sub": "real-user"} }
		ctx2, err := f.outgoing(ctx, lookup)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		md, _ := metadata.FromOutgoingContext(ctx2)
		if md.Get("x-user-id")[0] != "real-user" {
			t.Fatalf("expected real-user, got %v", md.Get("x-user-id"))
		}
	})
}
