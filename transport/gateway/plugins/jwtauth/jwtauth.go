// Package jwtauth validates JWT bearer tokens on incoming requests.
// Verified claims are stored in c.Locals(ClaimsLocal) for downstream use.
//
// Settings:
//
//	secret:    HMAC signing secret (required)
//	algorithm: HS256, HS384 or HS512 (default HS256)
//	issuer:    expected iss claim (optional)
//	audience:  expected aud claim (optional)
//	header:    header carrying the token (default "Authorization")
//	scheme:    token prefix in the header (default "Bearer")
package jwtauth

import (
	"fmt"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	sferrors "github.com/svcforge/service-forge/core/errors"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const Name = "jwt"

// ClaimsLocal is the fiber locals key holding verified jwt.MapClaims.
const ClaimsLocal = "jwt_claims"

var allowedAlgorithms = map[string]bool{"HS256": true, "HS384": true, "HS512": true}

type options struct {
	secret    []byte
	algorithm string
	issuer    string
	audience  string
	header    string
	scheme    string
}

func Factory(ctx plugin.BuildContext) (plugin.Plugin, error) {
	opts, err := buildOptions(ctx.Settings)
	if err != nil {
		return plugin.Plugin{}, err
	}
	parserOptions := []jwt.ParserOption{jwt.WithValidMethods([]string{opts.algorithm})}
	if opts.issuer != "" {
		parserOptions = append(parserOptions, jwt.WithIssuer(opts.issuer))
	}
	if opts.audience != "" {
		parserOptions = append(parserOptions, jwt.WithAudience(opts.audience))
	}
	return plugin.Plugin{Handler: func(c *fiber.Ctx) error {
		token, ok := extractToken(c, opts.header, opts.scheme)
		if !ok {
			return plugin.WriteError(c, sferrors.New(sferrors.CodeUnauthenticated, "missing bearer token"))
		}
		claims := jwt.MapClaims{}
		parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
			return opts.secret, nil
		}, parserOptions...)
		if err != nil || !parsed.Valid {
			return plugin.WriteError(c, sferrors.New(sferrors.CodeUnauthenticated, "invalid token"))
		}
		c.Locals(ClaimsLocal, claims)
		return c.Next()
	}}, nil
}

func buildOptions(settings plugin.Settings) (options, error) {
	secret, err := settings.String("secret", "")
	if err != nil {
		return options{}, err
	}
	if secret == "" {
		return options{}, fmt.Errorf("secret is required")
	}
	algorithm, err := settings.String("algorithm", "HS256")
	if err != nil {
		return options{}, err
	}
	algorithm = strings.ToUpper(algorithm)
	if !allowedAlgorithms[algorithm] {
		return options{}, fmt.Errorf("unsupported algorithm %q (supported: HS256, HS384, HS512)", algorithm)
	}
	issuer, err := settings.String("issuer", "")
	if err != nil {
		return options{}, err
	}
	audience, err := settings.String("audience", "")
	if err != nil {
		return options{}, err
	}
	header, err := settings.String("header", fiber.HeaderAuthorization)
	if err != nil {
		return options{}, err
	}
	scheme, err := settings.String("scheme", "Bearer")
	if err != nil {
		return options{}, err
	}
	return options{
		secret:    []byte(secret),
		algorithm: algorithm,
		issuer:    issuer,
		audience:  audience,
		header:    header,
		scheme:    scheme,
	}, nil
}

func extractToken(c *fiber.Ctx, header, scheme string) (string, bool) {
	raw := strings.TrimSpace(c.Get(header))
	if raw == "" {
		return "", false
	}
	if scheme == "" {
		return raw, true
	}
	prefix := scheme + " "
	if len(raw) <= len(prefix) || !strings.EqualFold(raw[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(raw[len(prefix):]), true
}
