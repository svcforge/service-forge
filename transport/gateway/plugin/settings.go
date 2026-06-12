package plugin

import (
	"fmt"
	"time"
)

// Settings is the raw YAML/JSON config block of one plugin entry. Accessors
// return the default when the key is absent and an error when the value has
// the wrong type, so misconfiguration fails at startup instead of silently
// falling back.
type Settings map[string]any

func (s Settings) String(key, def string) (string, error) {
	raw, ok := s[key]
	if !ok || raw == nil {
		return def, nil
	}
	value, ok := raw.(string)
	if !ok {
		return def, typeError(key, "string", raw)
	}
	return value, nil
}

func (s Settings) Bool(key string, def bool) (bool, error) {
	raw, ok := s[key]
	if !ok || raw == nil {
		return def, nil
	}
	value, ok := raw.(bool)
	if !ok {
		return def, typeError(key, "bool", raw)
	}
	return value, nil
}

func (s Settings) Int(key string, def int) (int, error) {
	raw, ok := s[key]
	if !ok || raw == nil {
		return def, nil
	}
	switch value := raw.(type) {
	case int:
		return value, nil
	case int64:
		return int(value), nil
	case float64:
		if value == float64(int(value)) {
			return int(value), nil
		}
	}
	return def, typeError(key, "int", raw)
}

func (s Settings) Duration(key string, def time.Duration) (time.Duration, error) {
	raw, ok := s[key]
	if !ok || raw == nil {
		return def, nil
	}
	switch value := raw.(type) {
	case string:
		parsed, err := time.ParseDuration(value)
		if err != nil {
			return def, fmt.Errorf("setting %q: invalid duration %q", key, value)
		}
		return parsed, nil
	case int:
		return time.Duration(value) * time.Second, nil
	case int64:
		return time.Duration(value) * time.Second, nil
	}
	return def, typeError(key, "duration", raw)
}

func (s Settings) Strings(key string) ([]string, error) {
	raw, ok := s[key]
	if !ok || raw == nil {
		return nil, nil
	}
	switch value := raw.(type) {
	case []string:
		return append([]string(nil), value...), nil
	case []any:
		items := make([]string, 0, len(value))
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, typeError(key, "string list", item)
			}
			items = append(items, text)
		}
		return items, nil
	}
	return nil, typeError(key, "string list", raw)
}

func typeError(key, want string, got any) error {
	return fmt.Errorf("setting %q: expected %s, got %T", key, want, got)
}
