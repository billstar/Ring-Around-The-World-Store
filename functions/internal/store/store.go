// Package store abstracts the per-region object store. The local filesystem
// implementation lets the entire ring run and be verified before any cloud spend;
// the GCS implementation is a drop-in with identical semantics.
package store

import (
	"context"
	"encoding/base64"
	"hash/crc32"
)

// Object is the durable identity of a written blob: the pair that makes a receipt
// checkable by a third party who has the bucket.
type Object struct {
	Generation int64  `json:"generation"`
	CRC32C     string `json:"crc32c"`
}

type Store interface {
	// Write persists data under name. It MUST fail if the object already exists,
	// so a replayed hop collides loudly instead of silently overwriting a receipt.
	Write(ctx context.Context, name string, data []byte) (Object, error)
	Read(ctx context.Context, name string) ([]byte, Object, error)
	Bucket() string
}

// castagnoli is the CRC32C polynomial GCS uses; computing it the same way locally
// means the readback check is byte-identical in both implementations.
var castagnoli = crc32.MakeTable(crc32.Castagnoli)

func CRC32C(b []byte) string {
	sum := crc32.Checksum(b, castagnoli)
	return base64.StdEncoding.EncodeToString([]byte{
		byte(sum >> 24), byte(sum >> 16), byte(sum >> 8), byte(sum),
	})
}
