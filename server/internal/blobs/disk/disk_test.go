package disk_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/blobs/disk"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/board/blobtest"
)

func TestDiskContract(t *testing.T) {
	blobtest.Run(t, func(t *testing.T) board.Blobs {
		s, err := disk.Open(filepath.Join(t.TempDir(), "files"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return s
	})
}

func TestDiskRefusesSymlinkRootAndCorruptBytes(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if s, err := disk.Open(link); err == nil {
		_ = s.Close()
		t.Fatal("symlink root accepted")
	}
	s, err := disk.Open(filepath.Join(dir, "files"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	b, err := s.Put(context.Background(), strings.NewReader("original"), 100)
	if err != nil {
		t.Fatal(err)
	}
	h := strings.TrimPrefix(b.Digest, "sha256:")
	p := filepath.Join(dir, "files", "sha256", h[:2], h[2:4], h)
	if err := os.WriteFile(p, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if r, err := s.Open(context.Background(), b.Digest); err == nil {
		_ = r.Close()
		t.Fatal("corrupt bytes accepted")
	}
}

func TestDiskRefusesUnsafeExistingDirectories(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "open permissions", true: "symlink"}[symlink], func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "files")
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatal(err)
			}
			tmp := filepath.Join(root, "tmp")
			if symlink {
				if err := os.Symlink(t.TempDir(), tmp); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Mkdir(tmp, 0o700); err != nil {
					t.Fatal(err)
				}
				//nolint:gosec // The fixture deliberately exposes the directory to test startup refusal.
				if err := os.Chmod(tmp, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			if s, err := disk.Open(root); err == nil {
				_ = s.Close()
				t.Fatal("unsafe existing directory accepted")
			}
		})
	}
}
