package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	gatewaypolicy "github.com/svcforge/service-forge/api/proto/svcforge/gateway/v1"
	"gopkg.in/yaml.v3"
)

// bufGenerateArgs excludes the local policy from generation while retaining
// each input's existing path filters. Buf CLI path flags replace those filters.
func bufGenerateArgs() ([]string, func(), error) {
	args := []string{"generate"}
	cleanup := func() {}
	raw, err := os.ReadFile("buf.gen.yaml")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, cleanup, err
	}
	var template struct {
		Inputs []map[string]any `yaml:"inputs,omitempty"`
		Other  map[string]any   `yaml:",inline"`
	}
	if err := yaml.Unmarshal(raw, &template); err != nil {
		return nil, cleanup, fmt.Errorf("read buf.gen.yaml: %w", err)
	}
	if len(template.Inputs) == 0 {
		return append(args, "--exclude-path", gatewaypolicy.ProjectPath), cleanup, nil
	}

	policy, err := filepath.Abs(gatewaypolicy.ProjectPath)
	if err != nil {
		return nil, cleanup, err
	}
	changed := false
	for _, input := range template.Inputs {
		directory, ok := input["directory"].(string)
		if !ok {
			continue
		}
		base, err := filepath.Abs(directory)
		if err != nil {
			return nil, cleanup, err
		}
		relative, err := filepath.Rel(base, policy)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			continue
		}
		paths, ok := input["exclude_paths"].([]any)
		if !ok && input["exclude_paths"] != nil {
			return nil, cleanup, fmt.Errorf("buf.gen.yaml inputs.exclude_paths must be a list")
		}
		if slices.ContainsFunc(paths, func(path any) bool { return path == gatewaypolicy.ProjectPath }) {
			continue
		}
		input["exclude_paths"] = append(paths, gatewaypolicy.ProjectPath)
		changed = true
	}
	if !changed {
		return args, cleanup, nil
	}
	data, err := yaml.Marshal(template)
	if err != nil {
		return nil, cleanup, err
	}
	file, err := os.CreateTemp("", "svcforge-buf-gen-*.yaml")
	if err != nil {
		return nil, cleanup, err
	}
	cleanup = func() { _ = os.Remove(file.Name()) }
	_, writeErr := file.Write(data)
	if err := errors.Join(writeErr, file.Close()); err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return append(args, "--template", file.Name()), cleanup, nil
}
