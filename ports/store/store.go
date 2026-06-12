package store

import "context"

type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Handle exposes an implementation-specific database handle to repository
// adapters. Business services should depend on repositories, not this type.
type Handle interface {
	Raw() any
}

type Repository[T any, ID comparable] interface {
	Get(ctx context.Context, id ID) (*T, error)
	Save(ctx context.Context, entity *T) error
	Delete(ctx context.Context, id ID) error
}
