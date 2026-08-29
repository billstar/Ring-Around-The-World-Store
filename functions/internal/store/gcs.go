package store

import (
	"context"
	"fmt"
	"io"

	"cloud.google.com/go/storage"
)

// GCS is the production Store. Semantics match Local exactly, including the
// does-not-exist precondition on write.
type GCS struct {
	client *storage.Client
	bucket string
}

func NewGCS(ctx context.Context, bucket string) (*GCS, error) {
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return &GCS{client: c, bucket: bucket}, nil
}

func (g *GCS) Bucket() string { return g.bucket }

func (g *GCS) Write(ctx context.Context, name string, data []byte) (Object, error) {
	// DoesNotExist: a replayed hop collides loudly rather than overwriting a receipt.
	w := g.client.Bucket(g.bucket).Object(name).If(storage.Conditions{DoesNotExist: true}).NewWriter(ctx)
	w.ContentType = "application/json"
	if _, err := w.Write(data); err != nil {
		w.Close()
		return Object{}, err
	}
	if err := w.Close(); err != nil {
		return Object{}, fmt.Errorf("writing gs://%s/%s: %w", g.bucket, name, err)
	}
	attrs := w.Attrs()
	return Object{Generation: attrs.Generation, CRC32C: CRC32C(data)}, nil
}

func (g *GCS) Read(ctx context.Context, name string) ([]byte, Object, error) {
	obj := g.client.Bucket(g.bucket).Object(name)
	r, err := obj.NewReader(ctx)
	if err != nil {
		return nil, Object{}, fmt.Errorf("reading gs://%s/%s: %w", g.bucket, name, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		return nil, Object{}, err
	}
	attrs, err := obj.Attrs(ctx)
	if err != nil {
		return nil, Object{}, err
	}
	return b, Object{Generation: attrs.Generation, CRC32C: CRC32C(b)}, nil
}
