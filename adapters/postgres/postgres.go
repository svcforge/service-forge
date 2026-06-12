package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/svcforge/service-forge/core/config"
	"github.com/svcforge/service-forge/core/module"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

type Config struct {
	Host         string        `yaml:"host"`
	Port         int           `yaml:"port"`
	User         string        `yaml:"user"`
	Password     string        `yaml:"password"`
	Database     string        `yaml:"database"`
	SSLMode      string        `yaml:"sslmode"`
	TimeZone     string        `yaml:"timezone"`
	MaxOpenConns int           `yaml:"max_open_conns"`
	MaxIdleConns int           `yaml:"max_idle_conns"`
	MaxLifetime  time.Duration `yaml:"max_lifetime"`
}

type Store struct {
	db *gorm.DB
}

type txKey struct{}

func New(cfg Config) (*Store, error) {
	if cfg.Host == "" {
		cfg.Host = "localhost"
	}
	if cfg.Port == 0 {
		cfg.Port = 5432
	}
	if cfg.SSLMode == "" {
		cfg.SSLMode = "disable"
	}
	if cfg.TimeZone == "" {
		cfg.TimeZone = "Asia/Shanghai"
	}
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Database, cfg.SSLMode, cfg.TimeZone,
	)
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if cfg.MaxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.MaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(cfg.MaxLifetime)
	}
	return &Store{db: db}, nil
}

func (s *Store) Raw() any {
	return s.db
}

func (s *Store) DB(ctx context.Context) *gorm.DB {
	if tx, ok := ctx.Value(txKey{}).(*gorm.DB); ok {
		return tx
	}
	return s.db.WithContext(ctx)
}

func (s *Store) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
}

func (s *Store) Close() error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

func (s *Store) Health(ctx context.Context) error {
	sqlDB, err := s.db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

type Module struct {
	store *Store
}

func NewModule() *Module {
	return &Module{}
}

func (m *Module) Name() string { return "store.postgres" }

func (m *Module) Init(ctx context.Context, app module.Runtime) error {
	cfg, err := config.ModuleConfig[Config](app.Config(), "postgres")
	if err != nil {
		return err
	}
	store, err := New(*cfg)
	if err != nil {
		return err
	}
	m.store = store
	app.Set("store", store)
	app.Set("tx_manager", store)
	app.Set("gorm", store.Raw())
	return nil
}

func (m *Module) Start(context.Context) error { return nil }

func (m *Module) Stop(context.Context) error {
	if m.store == nil {
		return nil
	}
	return m.store.Close()
}

func (m *Module) Health(ctx context.Context) error {
	if m.store == nil {
		return nil
	}
	return m.store.Health(ctx)
}
