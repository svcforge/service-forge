package objectstore

import (
	"context"
	"io"
	"time"
)

type Object struct {
	Bucket      string
	Key         string
	ContentType string
	Size        int64
	Metadata    map[string]string
	Body        io.ReadCloser
}

type PutOptions struct {
	ContentType string
	Metadata    map[string]string
}

type Store interface {
	Put(ctx context.Context, bucket, key string, body io.Reader, opts PutOptions) error
	Get(ctx context.Context, bucket, key string) (*Object, error)
	Delete(ctx context.Context, bucket, key string) error
	PresignGet(ctx context.Context, bucket, key string, ttl time.Duration) (string, error)
}
