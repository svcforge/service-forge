package module

import (
	"fmt"
	"sort"
	"strings"

	"github.com/svcforge/service-forge/core/config"
)

type Factory func() Module

type Catalog struct {
	factories map[string]Factory
}

func NewCatalog() *Catalog {
	return &Catalog{factories: map[string]Factory{}}
}

func (c *Catalog) Register(name, provider string, factory Factory) {
	if c.factories == nil {
		c.factories = map[string]Factory{}
	}
	c.factories[key(name, provider)] = factory
}

func (c *Catalog) Build(specs []config.ComponentConfig) ([]Module, error) {
	modules := make([]Module, 0, len(specs))
	for _, spec := range specs {
		if !spec.IsEnabled() {
			continue
		}
		if spec.Name == "" || spec.Provider == "" {
			return nil, fmt.Errorf("component name and provider are required")
		}
		factory, ok := c.factories[key(spec.Name, spec.Provider)]
		if !ok {
			return nil, fmt.Errorf("component %s.%s is not registered", spec.Name, spec.Provider)
		}
		modules = append(modules, factory())
	}
	return modules, nil
}

func (c *Catalog) Providers(name string) []string {
	providers := make([]string, 0)
	prefix := strings.ToLower(name) + "."
	for candidate := range c.factories {
		if strings.HasPrefix(candidate, prefix) {
			providers = append(providers, strings.TrimPrefix(candidate, prefix))
		}
	}
	sort.Strings(providers)
	return providers
}

func key(name, provider string) string {
	return strings.ToLower(strings.TrimSpace(name)) + "." + strings.ToLower(strings.TrimSpace(provider))
}
