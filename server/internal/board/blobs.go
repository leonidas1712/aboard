package board

import (
	"context"
	"errors"
	"io"
	"time"
)

// Blob is immutable content identified by its SHA-256 digest.
type Blob struct {
	Digest   string
	Size     int64
	Modified time.Time
}

// Blobs stores bytes independently of the record that references them.
type Blobs interface {
	Put(context.Context, io.Reader, int64) (Blob, error)
	Open(context.Context, string) (io.ReadCloser, error)
	Stat(context.Context, string) (Blob, error)
	Walk(context.Context, func(Blob) error) error
	Delete(context.Context, string) error
}

// ErrBlobTooLarge means an upload exceeded its supplied byte limit.
var ErrBlobTooLarge = errors.New("blob exceeds size limit")
