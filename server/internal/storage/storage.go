// Package storage abstracts blob storage for large artifacts the control
// plane serves to agents — currently signed agent release binaries. The
// interface keeps the rest of the server agnostic to whether bytes live on
// a local volume (fs backend, default) or in S3/MinIO (s3 backend).
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound is returned by Get/Exists when the key is absent.
var ErrNotFound = errors.New("storage: object not found")

// Store is the blob storage contract. Keys are server-generated, opaque,
// slash-delimited paths (e.g. "releases/<uuid>").
type Store interface {
	// Put stores r under key, overwriting any existing object. size is the
	// content length (-1 if unknown; backends that need it will buffer).
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	// Get opens the object for reading and returns its size.
	Get(ctx context.Context, key string) (io.ReadCloser, int64, error)
	// Exists reports whether key is present.
	Exists(ctx context.Context, key string) (bool, error)
}
