package redis

import (
	"context"
	"errors"
	"time"

	goredis "github.com/redis/go-redis/v9"
	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/module"
)

type Config struct {
	Addr         string        `yaml:"addr"`
	Username     string        `yaml:"username"`
	Password     string        `yaml:"password"`
	DB           int           `yaml:"db"`
	PoolSize     int           `yaml:"pool_size"`
	DialTimeout  time.Duration `yaml:"dial_timeout"`
	ReadTimeout  time.Duration `yaml:"read_timeout"`
	WriteTimeout time.Duration `yaml:"write_timeout"`
}

type Cache struct {
	client goredis.UniversalClient
}

func New(cfg Config) (*Cache, error) {
	if cfg.Addr == "" {
		cfg.Addr = "localhost:6379"
	}
	client := goredis.NewClient(&goredis.Options{
		Addr:         cfg.Addr,
		Username:     cfg.Username,
		Password:     cfg.Password,
		DB:           cfg.DB,
		PoolSize:     cfg.PoolSize,
		DialTimeout:  cfg.DialTimeout,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
	})
	return &Cache{client: client}, nil
}

func (c *Cache) Raw() goredis.UniversalClient {
	return c.client
}

func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	value, err := c.client.Get(ctx, key).Bytes()
	if errors.Is(err, goredis.Nil) {
		return nil, err
	}
	return value, err
}

func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return c.client.Set(ctx, key, value, ttl).Err()
}

func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	return c.client.Del(ctx, keys...).Err()
}

func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	n, err := c.client.Exists(ctx, key).Result()
	return n > 0, err
}

func (c *Cache) Close() error {
	return c.client.Close()
}

func (c *Cache) Health(ctx context.Context) error {
	return c.client.Ping(ctx).Err()
}

type Module struct {
	cache *Cache
}

func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string { return "cache.redis" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg, err := config.ModuleConfig[Config](app.Config(), "redis")
	if err != nil {
		return err
	}
	cache, err := New(*cfg)
	if err != nil {
		return err
	}
	m.cache = cache
	app.Set("cache", cache)
	app.Set("redis", cache.Raw())
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error {
	if m.cache == nil {
		return nil
	}
	return m.cache.Close()
}

func (m *Module) Health(ctx context.Context) error {
	if m.cache == nil {
		return nil
	}
	return m.cache.Health(ctx)
}
