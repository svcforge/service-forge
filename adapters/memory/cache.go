package memory

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/svcforge/service-forge/core/module"
)

var ErrCacheMiss = errors.New("cache miss")

type cacheItem struct {
	value     []byte
	expiresAt time.Time
}

type Cache struct {
	mu    sync.RWMutex
	items map[string]cacheItem
	locks map[string]time.Time
}

func NewCache() *Cache {
	return &Cache{
		items: map[string]cacheItem{},
		locks: map[string]time.Time{},
	}
}

func (c *Cache) Get(ctx context.Context, key string) ([]byte, error) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || expired(item.expiresAt) {
		return nil, ErrCacheMiss
	}
	out := make([]byte, len(item.value))
	copy(out, item.value)
	return out, nil
}

func (c *Cache) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items[key] = cacheItem{value: append([]byte(nil), value...), expiresAt: deadline(ttl)}
	return nil
}

func (c *Cache) Delete(ctx context.Context, keys ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, key := range keys {
		delete(c.items, key)
	}
	return nil
}

func (c *Cache) Exists(ctx context.Context, key string) (bool, error) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	return ok && !expired(item.expiresAt), nil
}

func (c *Cache) Lock(ctx context.Context, key string, ttl time.Duration) (Lock, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until, ok := c.locks[key]; ok && time.Now().Before(until) {
		return Lock{}, errors.New("lock already held")
	}
	c.locks[key] = deadline(ttl)
	return Lock{cache: c, key: key}, nil
}

type Lock struct {
	cache *Cache
	key   string
}

func (l Lock) Unlock(ctx context.Context) error {
	if l.cache == nil {
		return nil
	}
	l.cache.mu.Lock()
	defer l.cache.mu.Unlock()
	delete(l.cache.locks, l.key)
	return nil
}

func (c *Cache) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, error) {
	if limit <= 0 {
		return false, nil
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	var count int
	if ok && !expired(item.expiresAt) {
		count = int(item.value[0])
	}
	if count >= limit {
		return false, nil
	}
	count++
	c.items[key] = cacheItem{value: []byte{byte(count)}, expiresAt: now.Add(window)}
	return true, nil
}

func expired(t time.Time) bool {
	return !t.IsZero() && time.Now().After(t)
}

func deadline(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

type CacheModule struct {
	*Cache
}

func NewCacheModule() *CacheModule {
	return &CacheModule{Cache: NewCache()}
}

func (m *CacheModule) Name() string {
	return "cache.memory"
}

func (m *CacheModule) Init(ctx context.Context, app module.Runtime) error {
	app.Set("cache", m.Cache)
	app.Set("locker", m.Cache)
	app.Set("rate_limiter", m.Cache)
	return nil
}

func (m *CacheModule) Start(context.Context) error  { return nil }
func (m *CacheModule) Stop(context.Context) error   { return nil }
func (m *CacheModule) Health(context.Context) error { return nil }
