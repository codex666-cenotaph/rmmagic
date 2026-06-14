package recordings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// contentType is the media type used for asciinema cast recordings.
const contentType = "application/x-asciicast"

// ErrNotConfigured is returned by Open when no recording backend is set
// up; the caller should treat recording as disabled.
var ErrNotConfigured = errors.New("recordings: no storage backend configured")

// Store persists and retrieves shell-session recordings by key.
type Store interface {
	Put(ctx context.Context, key string, r io.Reader, size int64) error
	Get(ctx context.Context, key string) (io.ReadCloser, error)
}

// Config selects and configures a backend. Filesystem is the default;
// S3/MinIO is used when Endpoint is set.
type Config struct {
	Dir string // filesystem backend root

	Endpoint  string // S3 backend: host:port (presence selects S3)
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

// ConfigFromEnv reads recording storage configuration from the
// environment. RMM_S3_ENDPOINT selects the S3 backend; otherwise the
// filesystem backend at RMM_RECORDINGS_DIR (default
// /var/lib/rmmserver/recordings) is used.
func ConfigFromEnv() Config {
	dir := os.Getenv("RMM_RECORDINGS_DIR")
	if dir == "" {
		dir = "/var/lib/rmmserver/recordings"
	}
	return Config{
		Dir:       dir,
		Endpoint:  os.Getenv("RMM_S3_ENDPOINT"),
		Bucket:    envOr("RMM_S3_BUCKET", "rmm-recordings"),
		AccessKey: os.Getenv("RMM_S3_ACCESS_KEY"),
		SecretKey: os.Getenv("RMM_S3_SECRET_KEY"),
		Region:    envOr("RMM_S3_REGION", "us-east-1"),
		UseSSL:    os.Getenv("RMM_S3_USE_SSL") != "false",
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Open builds the configured Store, creating the filesystem directory or
// ensuring the S3 bucket exists.
func Open(ctx context.Context, cfg Config) (Store, error) {
	if cfg.Endpoint != "" {
		return openS3(ctx, cfg)
	}
	if cfg.Dir == "" {
		return nil, ErrNotConfigured
	}
	if err := os.MkdirAll(cfg.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("recordings dir: %w", err)
	}
	return &fsStore{dir: cfg.Dir}, nil
}

// safeKey rejects keys that could escape the storage root. Keys are
// server-generated from UUIDs, so this is defense in depth: any "..",
// ".", empty, or absolute segment is refused outright.
func safeKey(key string) (string, error) {
	key = strings.TrimPrefix(filepath.ToSlash(key), "/")
	if key == "" {
		return "", fmt.Errorf("recordings: empty key")
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("recordings: invalid key %q", key)
		}
	}
	return key, nil
}

// --- filesystem backend ---

type fsStore struct{ dir string }

func (s *fsStore) path(key string) (string, error) {
	clean, err := safeKey(key)
	if err != nil {
		return "", err
	}
	return filepath.Join(s.dir, filepath.FromSlash(clean)), nil
}

func (s *fsStore) Put(_ context.Context, key string, r io.Reader, _ int64) error {
	p, err := s.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".rec-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after successful rename
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, p)
}

func (s *fsStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	p, err := s.path(key)
	if err != nil {
		return nil, err
	}
	return os.Open(p)
}

// --- S3 / MinIO backend ---

type s3Store struct {
	client *minio.Client
	bucket string
}

func openS3(ctx context.Context, cfg Config) (Store, error) {
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("s3 client: %w", err)
	}
	exists, err := client.BucketExists(ctx, cfg.Bucket)
	if err != nil {
		return nil, fmt.Errorf("s3 bucket check: %w", err)
	}
	if !exists {
		if err := client.MakeBucket(ctx, cfg.Bucket, minio.MakeBucketOptions{Region: cfg.Region}); err != nil {
			return nil, fmt.Errorf("s3 make bucket: %w", err)
		}
	}
	return &s3Store{client: client, bucket: cfg.Bucket}, nil
}

func (s *s3Store) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	clean, err := safeKey(key)
	if err != nil {
		return err
	}
	_, err = s.client.PutObject(ctx, s.bucket, clean, r, size,
		minio.PutObjectOptions{ContentType: contentType})
	return err
}

func (s *s3Store) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	clean, err := safeKey(key)
	if err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, clean, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	// Surface a missing object as an error now rather than on first read.
	if _, err := obj.Stat(); err != nil {
		obj.Close()
		return nil, err
	}
	return obj, nil
}
