package gateway

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/svcforge/service-forge/core/config"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"google.golang.org/grpc/metadata"
)

// claimsLocal is the request-locals key under which the jwt plugin stores the
// verified claims (mirrors jwtauth.ClaimsLocal). The proxy reads it to forward
// selected claims to the upstream service as gRPC metadata. It is kept as a
// literal here so the gateway core does not depend on the jwt plugin package.
const claimsLocal = "jwt_claims"

// claimForwarder copies selected JWT claims from the request locals into the
// outgoing gRPC metadata. It is nil (and a no-op) unless a route configures
// forward_claims, matching the framework convention that features are off by
// default.
type claimForwarder struct {
	pairs []claimPair
}

type claimPair struct {
	claim        string
	header       string
	required     bool
	defaultValue string
	hasDefault   bool
}

// buildClaimForwarder compiles a route's forward_claims config. It returns nil
// when nothing is configured so the proxy skips claim handling entirely.
func buildClaimForwarder(specs []config.ClaimForwardConfig) *claimForwarder {
	pairs := make([]claimPair, 0, len(specs))
	for _, spec := range specs {
		claim := strings.TrimSpace(spec.Claim)
		if claim == "" {
			continue
		}
		header := strings.ToLower(strings.TrimSpace(spec.Header))
		if header == "" {
			header = strings.ToLower(claim)
		}
		pair := claimPair{claim: claim, header: header, required: spec.Required}
		if spec.Default != nil {
			pair.defaultValue = *spec.Default
			pair.hasDefault = true
		}
		pairs = append(pairs, pair)
	}
	if len(pairs) == 0 {
		return nil
	}
	return &claimForwarder{pairs: pairs}
}

// outgoing returns ctx augmented with gRPC metadata for each configured claim.
// lookup reads a locals value by key, letting the same forwarder serve both the
// unary path (*fiber.Ctx) and the streaming paths (websocket.Conn). For a claim
// that is absent or not scalar: a configured default is forwarded, otherwise a
// required claim fails with UNAUTHENTICATED and an optional claim is skipped. A
// nil forwarder returns ctx unchanged.
func (f *claimForwarder) outgoing(ctx context.Context, lookup func(string) any) (context.Context, error) {
	if f == nil {
		return ctx, nil
	}
	// A nil claims map (no jwt plugin / anonymous request) is fine to range over:
	// every claim resolves to absent, so defaults and required checks still apply.
	claims, _ := asClaims(lookup(claimsLocal))
	md := make([]string, 0, len(f.pairs)*2)
	for _, pair := range f.pairs {
		text, ok := claimText(claims, pair.claim)
		if !ok {
			switch {
			case pair.hasDefault:
				text = pair.defaultValue
			case pair.required:
				return ctx, sferrors.New(sferrors.CodeUnauthenticated, "missing required claim").
					WithDetails("claim", pair.claim)
			default:
				continue
			}
		}
		md = append(md, pair.header, text)
	}
	if len(md) == 0 {
		return ctx, nil
	}
	return metadata.AppendToOutgoingContext(ctx, md...), nil
}

// claimText resolves a claim to its scalar string form. It reports false when
// the claim is absent or not representable as a single header value.
func claimText(claims map[string]any, key string) (string, bool) {
	value, exists := claims[key]
	if !exists {
		return "", false
	}
	return claimToString(value)
}

// asClaims normalizes a locals value into a claim map. The jwt plugin stores
// jwt.MapClaims; a plain map is also accepted so custom auth plugins can feed
// the same forwarding path.
func asClaims(raw any) (map[string]any, bool) {
	switch claims := raw.(type) {
	case jwt.MapClaims:
		return claims, true
	case map[string]any:
		return claims, true
	default:
		return nil, false
	}
}

// claimToString renders a scalar claim value for a metadata header. Non-scalar
// claims (arrays, objects) cannot be expressed as a single header value and are
// skipped.
func claimToString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return v, true
	case bool:
		return strconv.FormatBool(v), true
	case float64:
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10), true
		}
		return strconv.FormatFloat(v, 'g', -1, 64), true
	case json.Number:
		return v.String(), true
	default:
		return "", false
	}
}
