package app

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/svcforge/service-forge/core/module"
)

type lifecycleModule struct {
	module.BaseModule
	record func(string)
	failAt string
	err    error
}

func (m lifecycleModule) step(phase string) error {
	m.record(phase + ":" + m.Name())
	if m.failAt == phase {
		return m.err
	}
	return nil
}

func (m lifecycleModule) Init(context.Context, module.Runtime) error { return m.step("init") }
func (m lifecycleModule) Start(context.Context) error                { return m.step("start") }

func (m lifecycleModule) Stop(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return m.step("stop")
}

func TestRunLifecycle(t *testing.T) {
	failure := errors.New("lifecycle failure")
	cleanup := errors.New("cleanup failure")
	for _, tt := range []struct {
		name   string
		failAt string
		want   []string
	}{
		{"shutdown", "", []string{"init:a", "init:b", "init:c", "start:a", "start:b", "start:c", "stop:c", "stop:b", "stop:a"}},
		{"init rollback", "init", []string{"init:a", "init:b", "stop:a"}},
		{"start rollback", "start", []string{"init:a", "init:b", "init:c", "start:a", "start:b", "stop:c", "stop:b", "stop:a"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var steps []string
			record := func(step string) { steps = append(steps, step) }
			a := lifecycleModule{module.BaseModule{ModuleName: "a"}, record, "stop", cleanup}
			b := lifecycleModule{module.BaseModule{ModuleName: "b"}, record, tt.failAt, failure}
			c := lifecycleModule{module.BaseModule{ModuleName: "c"}, record, "", nil}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			err := New(nil, WithModules(a, b, c)).Run(ctx)
			if !errors.Is(err, cleanup) || errors.Is(err, failure) != (tt.failAt != "") {
				t.Fatalf("Run error = %v", err)
			}
			if !slices.Equal(steps, tt.want) {
				t.Fatalf("steps = %v, want %v", steps, tt.want)
			}
		})
	}
}
