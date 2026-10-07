// Package disk stores immutable, digest-verified bytes under one filesystem root.
package disk

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/board"
)

// Store confines content-addressed bytes to an owner-only directory.
type Store struct{ root *os.Root }

// Open checks the storage directory before opening its confined root.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return nil, err
	}
	return openRoot(path, true)
}

// OpenExisting opens a store for inspection without creating directories.
func OpenExisting(path string) (*Store, error) { return openRoot(path, false) }

func openRoot(path string, create bool) (*Store, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || !owned(info) {
		return nil, fmt.Errorf("blob directory must be a real owner-only directory")
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := ensureDirs(root, "tmp"); err != nil {
			_ = root.Close()
			return nil, err
		}
	}
	return &Store{root: root}, nil
}

// Close releases the root directory descriptor.
func (s *Store) Close() error { return s.root.Close() }

func name(digest string) (string, error) {
	h := strings.TrimPrefix(digest, "sha256:")
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != sha256.Size || digest != "sha256:"+hex.EncodeToString(b) {
		return "", fmt.Errorf("invalid blob digest")
	}
	return "sha256/" + h[:2] + "/" + h[2:4] + "/" + h, nil
}

// Put publishes a bounded upload only after its bytes have reached disk.
func (s *Store) Put(ctx context.Context, r io.Reader, limit int64) (board.Blob, error) {
	var out board.Blob
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return out, err
	}
	tmp := "tmp/" + hex.EncodeToString(nonce[:])
	f, err := s.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return out, err
	}
	defer func() { _ = f.Close(); _ = s.root.Remove(tmp) }()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), io.LimitReader(r, limit+1))
	if err != nil {
		return out, err
	}
	if n > limit {
		return out, board.ErrBlobTooLarge
	}
	if err := ctx.Err(); err != nil {
		return out, err
	}
	if err := f.Sync(); err != nil {
		return out, err
	}
	if err := f.Close(); err != nil {
		return out, err
	}
	out.Digest = "sha256:" + hex.EncodeToString(hash.Sum(nil))
	out.Size = n
	dest, _ := name(out.Digest)
	if err := ensureDirs(s.root, filepath.Dir(dest)); err != nil {
		return out, err
	}
	// Linking publishes once without replacing an existing digest.
	if err := s.root.Link(tmp, dest); err != nil && !errors.Is(err, fs.ErrExist) {
		return out, err
	}
	check, err := s.Open(ctx, out.Digest)
	if err != nil {
		return out, err
	}
	_ = check.Close()
	dir, err := s.root.Open(filepath.Dir(dest))
	if err != nil {
		return out, err
	}
	err = dir.Sync()
	_ = dir.Close()
	return out, err
}

// Open verifies a regular blob against its digest before returning the bytes.
func (s *Store) Open(ctx context.Context, digest string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, err := name(digest)
	if err != nil {
		return nil, err
	}
	info, err := s.root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || !owned(info) {
		return nil, fmt.Errorf("blob must be an owner-only regular file")
	}
	f, err := s.root.Open(path)
	if err != nil {
		return nil, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !owned(opened) {
		_ = f.Close()
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("blob must be an owner-only regular file")
	}
	hash := sha256.New()
	_, err = io.Copy(hash, f)
	if err == nil && "sha256:"+hex.EncodeToString(hash.Sum(nil)) != digest {
		err = fmt.Errorf("blob digest mismatch")
	}
	if err == nil {
		_, err = f.Seek(0, io.SeekStart)
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// Stat returns size and modification time after verifying the blob.
func (s *Store) Stat(ctx context.Context, digest string) (board.Blob, error) {
	f, err := s.Open(ctx, digest)
	if err != nil {
		return board.Blob{}, err
	}
	defer func() { _ = f.Close() }()
	opened, ok := f.(*os.File)
	if !ok {
		return board.Blob{}, fmt.Errorf("unexpected disk reader")
	}
	info, err := opened.Stat()
	if err != nil {
		return board.Blob{}, err
	}
	return board.Blob{Digest: digest, Size: info.Size(), Modified: info.ModTime()}, nil
}

// Walk visits published blobs, excluding temporary uploads.
func (s *Store) Walk(ctx context.Context, visit func(board.Blob) error) error {
	return fs.WalkDir(s.root.FS(), ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			if path == "tmp" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasPrefix(path, "sha256/") {
			return nil
		}
		digest := "sha256:" + filepath.Base(path)
		expected, err := name(digest)
		if err != nil || expected != path {
			return fmt.Errorf("invalid blob path")
		}
		blob, err := s.Stat(ctx, digest)
		if err != nil {
			return err
		}
		return visit(blob)
	})
}

// Delete removes a blob; the caller must establish that no record references it.
func (s *Store) Delete(ctx context.Context, digest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := name(digest)
	if err != nil {
		return err
	}
	return s.root.Remove(path)
}

func owned(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid() && info.Mode()&0o077 == 0
}

func ensureDirs(root *os.Root, path string) error {
	current := ""
	for _, part := range strings.Split(path, "/") {
		if current == "" {
			current = part
		} else {
			current += "/" + part
		}
		if err := root.Mkdir(current, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		info, err := root.Lstat(current)
		if err != nil {
			return err
		}
		if !info.IsDir() || !owned(info) {
			return fmt.Errorf("blob directories must be real owner-only directories")
		}
	}
	return nil
}
