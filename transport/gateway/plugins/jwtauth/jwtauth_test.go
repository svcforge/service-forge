package jwtauth

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/golang-jwt/jwt/v5"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

const testSecret = "test-secret"

func signToken(t *testing.T, secret string, claims jwt.MapClaims) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return token
}

func newApp(t *testing.T, settings plugin.Settings) *fiber.App {
	t.Helper()
	built, err := Factory(plugin.BuildContext{Settings: settings})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/", func(c *fiber.Ctx) error {
		claims, _ := c.Locals(ClaimsLocal).(jwt.MapClaims)
		subject, _ := claims["sub"].(string)
		return c.SendString(subject)
	})
	return app
}

func TestFactoryValidation(t *testing.T) {
	if _, err := Factory(plugin.BuildContext{Settings: plugin.Settings{}}); err == nil {
		t.Fatal("expected error when secret is missing")
	}
	if _, err := Factory(plugin.BuildContext{Settings: plugin.Settings{
		"secret":    testSecret,
		"algorithm": "none",
	}}); err == nil {
		t.Fatal("expected error for unsupported algorithm")
	}
}

func TestAcceptsValidTokenAndExposesClaims(t *testing.T) {
	app := newApp(t, plugin.Settings{"secret": testSecret})
	token := signToken(t, testSecret, jwt.MapClaims{
		"sub": "user-1",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body := make([]byte, 16)
	n, _ := resp.Body.Read(body)
	if string(body[:n]) != "user-1" {
		t.Fatalf("expected claims subject user-1, got %q", string(body[:n]))
	}
}

func TestRejectsInvalidTokens(t *testing.T) {
	app := newApp(t, plugin.Settings{"secret": testSecret})

	resp, _ := app.Test(httptest.NewRequest("GET", "/", nil))
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("missing token: expected 401, got %d", resp.StatusCode)
	}

	wrongKey := signToken(t, "other-secret", jwt.MapClaims{
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+wrongKey)
	resp, _ = app.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("wrong signature: expected 401, got %d", resp.StatusCode)
	}

	expired := signToken(t, testSecret, jwt.MapClaims{
		"exp": time.Now().Add(-time.Hour).Unix(),
	})
	req = httptest.NewRequest("GET", "/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+expired)
	resp, _ = app.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("expired token: expected 401, got %d", resp.StatusCode)
	}
}

func TestValidatesIssuer(t *testing.T) {
	app := newApp(t, plugin.Settings{"secret": testSecret, "issuer": "svcforge"})
	token := signToken(t, testSecret, jwt.MapClaims{
		"iss": "someone-else",
		"exp": time.Now().Add(time.Hour).Unix(),
	})
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set(fiber.HeaderAuthorization, "Bearer "+token)
	resp, _ := app.Test(req)
	if resp.StatusCode != fiber.StatusUnauthorized {
		t.Fatalf("wrong issuer: expected 401, got %d", resp.StatusCode)
	}
}
