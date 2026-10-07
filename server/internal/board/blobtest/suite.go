// Package blobtest checks the immutable byte-store port against every adapter.
package blobtest

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// Run checks the byte-store guarantees against an isolated adapter.
func Run(t *testing.T, newStore func(*testing.T) board.Blobs) {
	t.Helper()
	t.Run("immutable bytes and limits", func(t *testing.T) {
		s := newStore(t)
		ctx := context.Background()
		first, err := s.Put(ctx, strings.NewReader("exact bytes\x00\n"), 100)
		if err != nil {
			t.Fatal(err)
		}
		again, err := s.Put(ctx, strings.NewReader("exact bytes\x00\n"), 100)
		if err != nil {
			t.Fatal(err)
		}
		if first.Digest != again.Digest || first.Size != 13 {
			t.Fatal("content identity changed")
		}
		r, err := s.Open(ctx, first.Digest)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil || string(body) != "exact bytes\x00\n" {
			t.Fatal("bytes changed")
		}
		if _, err := s.Put(ctx, strings.NewReader("12345"), 4); !errors.Is(err, board.ErrBlobTooLarge) {
			t.Fatalf("limit: %v", err)
		}
		count := 0
		if err := s.Walk(ctx, func(b board.Blob) error {
			count++
			if b.Digest != first.Digest {
				t.Fatal("rejected upload was published")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("stored %d blobs", count)
		}
		if _, err := s.Open(ctx, "sha256:../../outside"); err == nil {
			t.Fatal("invalid digest accepted")
		}
		if err := s.Delete(ctx, first.Digest); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Open(ctx, first.Digest); err == nil {
			t.Fatal("deleted blob remains")
		}
	})
}
