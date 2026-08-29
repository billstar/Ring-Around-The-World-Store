package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

// Local is a filesystem-backed Store used for the local proof-of-concept.
// Generation numbers imitate GCS: microsecond-resolution and monotonically increasing.
type Local struct {
	dir    string
	bucket string
	seq    atomic.Int64
}

func NewLocal(dir, bucket string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Local{dir: dir, bucket: bucket}, nil
}

func (l *Local) Bucket() string { return l.bucket }

func (l *Local) path(name string) string { return filepath.Join(l.dir, filepath.FromSlash(name)) }

func (l *Local) Write(_ context.Context, name string, data []byte) (Object, error) {
	p := l.path(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return Object{}, err
	}
	// O_EXCL mirrors the GCS DoesNotExist precondition.
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return Object{}, fmt.Errorf("object %s already exists (replayed hop?)", name)
		}
		return Object{}, err
	}
	defer f.Close()
	if _, err := f.Write(data); err != nil {
		return Object{}, err
	}
	return Object{
		Generation: time.Now().UnixMicro() + l.seq.Add(1),
		CRC32C:     CRC32C(data),
	}, nil
}

func (l *Local) Read(_ context.Context, name string) ([]byte, Object, error) {
	b, err := os.ReadFile(l.path(name))
	if err != nil {
		return nil, Object{}, err
	}
	return b, Object{CRC32C: CRC32C(b)}, nil
}
