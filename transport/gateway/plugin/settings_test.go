package plugin

import (
	"testing"
	"time"
)

func TestSettingsDefaults(t *testing.T) {
	s := Settings{}
	if v, err := s.String("missing", "def"); err != nil || v != "def" {
		t.Fatalf("expected default string, got %q err=%v", v, err)
	}
	if v, err := s.Bool("missing", true); err != nil || !v {
		t.Fatalf("expected default bool, got %v err=%v", v, err)
	}
	if v, err := s.Int("missing", 7); err != nil || v != 7 {
		t.Fatalf("expected default int, got %d err=%v", v, err)
	}
	if v, err := s.Duration("missing", time.Minute); err != nil || v != time.Minute {
		t.Fatalf("expected default duration, got %v err=%v", v, err)
	}
	if v, err := s.Strings("missing"); err != nil || v != nil {
		t.Fatalf("expected nil strings, got %v err=%v", v, err)
	}
}

func TestSettingsTypedValues(t *testing.T) {
	s := Settings{
		"name":    "svc",
		"flag":    true,
		"max":     100,
		"window":  "30s",
		"seconds": 5,
		"origins": []any{"https://a.com", "https://b.com"},
	}
	if v, _ := s.String("name", ""); v != "svc" {
		t.Fatalf("string: got %q", v)
	}
	if v, _ := s.Bool("flag", false); !v {
		t.Fatal("bool: expected true")
	}
	if v, _ := s.Int("max", 0); v != 100 {
		t.Fatalf("int: got %d", v)
	}
	if v, _ := s.Duration("window", 0); v != 30*time.Second {
		t.Fatalf("duration string: got %v", v)
	}
	if v, _ := s.Duration("seconds", 0); v != 5*time.Second {
		t.Fatalf("duration int: got %v", v)
	}
	origins, err := s.Strings("origins")
	if err != nil || len(origins) != 2 || origins[0] != "https://a.com" {
		t.Fatalf("strings: got %v err=%v", origins, err)
	}
}

func TestSettingsTypeErrors(t *testing.T) {
	s := Settings{
		"name":    123,
		"flag":    "yes",
		"max":     "many",
		"window":  "not-a-duration",
		"origins": []any{1, 2},
	}
	if _, err := s.String("name", ""); err == nil {
		t.Fatal("expected string type error")
	}
	if _, err := s.Bool("flag", false); err == nil {
		t.Fatal("expected bool type error")
	}
	if _, err := s.Int("max", 0); err == nil {
		t.Fatal("expected int type error")
	}
	if _, err := s.Duration("window", 0); err == nil {
		t.Fatal("expected duration parse error")
	}
	if _, err := s.Strings("origins"); err == nil {
		t.Fatal("expected string list type error")
	}
}
