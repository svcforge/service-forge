package accesslog

import (
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/svcforge/service-forge/core/module"
	"github.com/svcforge/service-forge/transport/gateway/plugin"
)

type recordingLogger struct {
	mu       sync.Mutex
	messages []string
}

func (l *recordingLogger) With(fields ...any) module.Logger { return l }
func (l *recordingLogger) Debug(msg string, fields ...any)  {}
func (l *recordingLogger) Warn(msg string, fields ...any)   {}
func (l *recordingLogger) Error(msg string, fields ...any)  {}
func (l *recordingLogger) Info(msg string, fields ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.messages = append(l.messages, msg)
}

func (l *recordingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.messages)
}

func TestLogsRequestsAndSkipsConfiguredPaths(t *testing.T) {
	logger := &recordingLogger{}
	built, err := Factory(plugin.BuildContext{
		Logger:   logger,
		Settings: plugin.Settings{"skip_paths": []any{"/health"}},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	app := fiber.New()
	app.Use(built.Handler)
	app.Get("/health", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/api", func(c *fiber.Ctx) error { return c.SendString("ok") })

	if _, err := app.Test(httptest.NewRequest("GET", "/api", nil)); err != nil {
		t.Fatalf("request: %v", err)
	}
	if _, err := app.Test(httptest.NewRequest("GET", "/health", nil)); err != nil {
		t.Fatalf("request: %v", err)
	}
	if logger.count() != 1 {
		t.Fatalf("expected exactly one access log line, got %d", logger.count())
	}
}
