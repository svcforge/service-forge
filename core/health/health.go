package health

import (
	"context"
	"sync"
)

// Checker reports health for a component.
type Checker interface {
	Name() string
	Health(ctx context.Context) error
}

// Report is a point-in-time health result.
type Report struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Registry groups health checks for an application.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Checker
	healthy string
}

func NewRegistry() *Registry {
	return &Registry{
		checks:  make(map[string]Checker),
		healthy: "ok",
	}
}

func (r *Registry) Add(check Checker) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[check.Name()] = check
}

func (r *Registry) Check(ctx context.Context) Report {
	r.mu.RLock()
	checks := make([]Checker, 0, len(r.checks))
	for _, check := range r.checks {
		checks = append(checks, check)
	}
	r.mu.RUnlock()

	report := Report{
		Status: r.healthy,
		Checks: make(map[string]string, len(checks)),
	}
	for _, check := range checks {
		if err := check.Health(ctx); err != nil {
			report.Status = "degraded"
			report.Checks[check.Name()] = err.Error()
			continue
		}
		report.Checks[check.Name()] = "ok"
	}
	return report
}
