package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type AppConfig struct {
	Name    string `yaml:"name" json:"name"`
	Version string `yaml:"version" json:"version"`
	Env     string `yaml:"env" json:"env"`
	Debug   bool   `yaml:"debug" json:"debug"`
}

type ServerConfig struct {
	ListenIP   string        `yaml:"listen_ip" json:"listen_ip"`
	RegisterIP string        `yaml:"register_ip" json:"register_ip"`
	Port       int           `yaml:"port" json:"port"`
	Timeout    time.Duration `yaml:"timeout" json:"timeout"`
}

type GRPCConfig struct {
	ListenIP   string `yaml:"listen_ip" json:"listen_ip"`
	RegisterIP string `yaml:"register_ip" json:"register_ip"`
	Port       int    `yaml:"port" json:"port"`
}

type GatewayConfig struct {
	ListenIP              string                `yaml:"listen_ip" json:"listen_ip"`
	Port                  int                   `yaml:"port" json:"port"`
	DisableStartupMessage bool                  `yaml:"disable_startup_message" json:"disable_startup_message"`
	Plugins               []GatewayPluginConfig `yaml:"plugins" json:"plugins"`
	Routes                []GatewayRouteConfig  `yaml:"routes" json:"routes"`
}

type GatewayRouteConfig struct {
	Name     string                `yaml:"name" json:"name"`
	Method   string                `yaml:"method" json:"method"`
	Path     string                `yaml:"path" json:"path"`
	Service  string                `yaml:"service" json:"service"`
	Target   string                `yaml:"target" json:"target"`
	RPC      string                `yaml:"rpc" json:"rpc"`
	Timeout  time.Duration         `yaml:"timeout" json:"timeout"`
	PoolSize int                   `yaml:"pool_size" json:"pool_size"`
	Plugins  []GatewayPluginConfig `yaml:"plugins" json:"plugins"`
}

// GatewayPluginConfig selects a gateway plugin by name. Plugins are off by
// default: only plugins listed in config (and not explicitly disabled) run.
type GatewayPluginConfig struct {
	Name    string         `yaml:"name" json:"name"`
	Enabled *bool          `yaml:"enabled" json:"enabled"`
	Config  map[string]any `yaml:"config" json:"config"`
}

func (c GatewayPluginConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

type LogConfig struct {
	Format          string `yaml:"format" json:"format"`
	Level           string `yaml:"level" json:"level"`
	ModuleLifecycle bool   `yaml:"module_lifecycle" json:"module_lifecycle"`
}

type RuntimeConfig struct {
	Components []ComponentConfig `yaml:"components" json:"components"`
}

type ComponentConfig struct {
	Name     string `yaml:"name" json:"name"`
	Provider string `yaml:"provider" json:"provider"`
	Enabled  *bool  `yaml:"enabled" json:"enabled"`
}

func (c ComponentConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

// Config contains framework-owned config only. Component-specific settings
// live under Modules and are decoded by adapters.
type Config struct {
	App     AppConfig                 `yaml:"app" json:"app"`
	Server  ServerConfig              `yaml:"server" json:"server"`
	GRPC    GRPCConfig                `yaml:"grpc" json:"grpc"`
	Gateway GatewayConfig             `yaml:"gateway" json:"gateway"`
	Log     LogConfig                 `yaml:"log" json:"log"`
	Runtime RuntimeConfig             `yaml:"runtime" json:"runtime"`
	Modules map[string]map[string]any `yaml:"modules" json:"modules"`
	Raw     map[string]any            `yaml:"-" json:"-"`
}

type LoadOptions struct {
	ConfigDir   string
	ServiceName string
	Environment string
	EnableLocal bool
}

type Bundle[T any] struct {
	Core *Config
	App  *T
}

func Default() *Config {
	return &Config{
		App: AppConfig{
			Version: "v0.1.0",
			Env:     "development",
		},
		Server: ServerConfig{
			ListenIP: "0.0.0.0",
			Port:     8080,
			Timeout:  30 * time.Second,
		},
		GRPC: GRPCConfig{
			ListenIP: "0.0.0.0",
			Port:     9000,
		},
		Gateway: GatewayConfig{
			ListenIP:              "0.0.0.0",
			Port:                  8080,
			DisableStartupMessage: true,
		},
		Log: LogConfig{
			Format:          "text",
			Level:           "info",
			ModuleLifecycle: false,
		},
		Modules: map[string]map[string]any{},
		Raw:     map[string]any{},
	}
}

func Load[T any](opts LoadOptions) (*Bundle[T], error) {
	if opts.ConfigDir == "" {
		opts.ConfigDir = "config"
	}
	if opts.Environment == "" {
		opts.Environment = os.Getenv("APP_ENV")
	}
	if opts.Environment == "" {
		opts.Environment = "development"
	}

	raw := map[string]any{}
	files := []string{
		"base.yaml",
		filepath.Join("envs", opts.Environment+".yaml"),
	}
	if opts.ServiceName != "" {
		files = append(files, filepath.Join("services", opts.ServiceName+".yaml"))
	}
	if opts.EnableLocal {
		files = append(files, "local.yaml")
	}

	for _, name := range files {
		path := filepath.Join(opts.ConfigDir, name)
		if err := mergeFile(raw, path); err != nil && !os.IsNotExist(err) {
			return nil, err
		}
	}

	core := Default()
	core.Raw = raw
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal merged config: %w", err)
	}
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, core); err != nil {
			return nil, fmt.Errorf("decode core config: %w", err)
		}
	}
	if opts.ServiceName != "" {
		core.App.Name = opts.ServiceName
	}
	if core.App.Env == "" {
		core.App.Env = opts.Environment
	}

	var appConfig T
	if len(data) > 0 {
		if err := yaml.Unmarshal(data, &appConfig); err != nil {
			return nil, fmt.Errorf("decode app config: %w", err)
		}
	}
	return &Bundle[T]{Core: core, App: &appConfig}, nil
}

func ModuleConfig[T any](cfg *Config, name string) (*T, error) {
	var out T
	if cfg == nil || cfg.Modules == nil {
		return &out, nil
	}
	raw, ok := cfg.Modules[name]
	if !ok {
		return &out, nil
	}
	data, err := yaml.Marshal(raw)
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func mergeFile(dst map[string]any, path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	content = []byte(os.ExpandEnv(string(content)))
	src := map[string]any{}
	if err := yaml.Unmarshal(content, &src); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	mergeMap(dst, normalizeMap(src))
	return nil
}

func normalizeMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		switch typed := v.(type) {
		case map[string]any:
			out[k] = normalizeMap(typed)
		case map[any]any:
			nested := make(map[string]any, len(typed))
			for nk, nv := range typed {
				nested[fmt.Sprint(nk)] = nv
			}
			out[k] = normalizeMap(nested)
		default:
			out[k] = v
		}
	}
	return out
}

func mergeMap(dst, src map[string]any) {
	for key, value := range src {
		if srcMap, ok := value.(map[string]any); ok {
			if dstMap, ok := dst[key].(map[string]any); ok {
				mergeMap(dstMap, srcMap)
				continue
			}
		}
		dst[key] = value
	}
}

func Address(ip string, port int) string {
	if strings.TrimSpace(ip) == "" {
		ip = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", ip, port)
}
