package server

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/rules"
)

// bootstrapStep is called at each step of making the first admin. Tests make it fail
// to stand in for a crash or a failed write at that step.
var bootstrapStep = func(string) error { return nil }

// Files in a team server's data folder that making the first admin uses, beside
// AdminKeyFile.
const (
	// pendingKeyFile holds the first admin's key from before the admin is made until the
	// key is delivered as AdminKeyFile.
	pendingKeyFile = AdminKeyFile + ".pending"
	// bootstrapLock is locked while a start makes or recovers the first admin, so starts
	// on one data folder take turns.
	bootstrapLock = "bootstrap.lock"
)

// firstAdmin makes a team server's first person, its admin, on the first start of an
// empty database, and delivers their key as the admin key file in dataDir. It can stop
// at any step, by a crash or a failed write, and the next start finishes the work:
//
//  1. The key is written to a pending file and synced, before anything else.
//  2. The admin is made with that key, in one transaction that first checks the server
//     has no person, so there is never a second admin.
//  3. The pending file is linked to the admin key file, which never replaces a file,
//     and removed.
//
// A start first finishes an earlier one: a pending key that works as an admin's is
// delivered, and any other is removed. Starts on one data folder take turns through a
// lock file. The key is never logged, only the file that holds it.
func firstAdmin(ctx context.Context, svc *board.Service, dataDir, name string, log *slog.Logger) error {
	unlock, err := lockFile(filepath.Join(dataDir, bootstrapLock))
	if err != nil {
		return err
	}
	defer unlock()
	keyPath, pendingPath := filepath.Join(dataDir, AdminKeyFile), filepath.Join(dataDir, pendingKeyFile)
	if err := finishBootstrap(ctx, svc, dataDir, log); err != nil {
		return err
	}
	if has, err := svc.HasPeople(ctx); err != nil || has {
		return err
	}
	if _, err := os.Lstat(keyPath); err == nil {
		return fmt.Errorf("%s exists, but this server has no one yet, so it isn't this server's key; move it away and start again", keyPath)
	}
	handle := rules.NormalizeName(name)
	secret, err := svc.NewKeySecret()
	if err != nil {
		return err
	}
	if err := bootstrapStep("write key"); err != nil {
		return err
	}
	if err := writeSynced(pendingPath, secret+"\n"); err != nil {
		return fmt.Errorf("save the first admin's key: %w", err)
	}
	if err := bootstrapStep("key written"); err != nil {
		return err
	}
	created, err := svc.BootstrapAdmin(ctx, handle, AdminKeyFile, secret)
	if err != nil || !created {
		_ = os.Remove(pendingPath)
		return err
	}
	if err := bootstrapStep("admin created"); err != nil {
		return err
	}
	if err := deliver(pendingPath, keyPath, dataDir); err != nil {
		return err
	}
	log.Info("first admin created", "handle", handle, "key_file", keyPath)
	return nil
}

// finishBootstrap finishes a start that stopped while making the first admin: a pending
// key that works as an admin's is delivered as the admin key file, and one that doesn't
// (the start stopped before the admin was made) is removed.
func finishBootstrap(ctx context.Context, svc *board.Service, dataDir string, log *slog.Logger) error {
	keyPath, pendingPath := filepath.Join(dataDir, AdminKeyFile), filepath.Join(dataDir, pendingKeyFile)
	raw, err := readPrivate(pendingPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the first admin's pending key: %w", err)
	}
	p, err := svc.Authenticate(ctx, strings.TrimSpace(string(raw)))
	// Only a key the server says doesn't work is from a start that stopped before the
	// admin was made. Any other failure, such as a database error or a canceled start,
	// says nothing about the key, so it stays for the next start.
	if e, ok := apierr.As(err); err != nil && (!ok || e.Code != "unauthorized") {
		return fmt.Errorf("check the first admin's pending key in %s: %w", pendingPath, err)
	}
	if err != nil || p.Human == nil || p.Human.Role != board.ServerAdmin || p.Browser || p.Agent != nil {
		if err := os.Remove(pendingPath); err != nil {
			return fmt.Errorf("remove the unused pending key %s: %w", pendingPath, err)
		}
		return nil
	}
	if err := deliver(pendingPath, keyPath, dataDir); err != nil {
		return err
	}
	log.Info("first admin's key delivered", "handle", p.Human.Name, "key_file", keyPath)
	return nil
}

// deliver links the pending key file to the admin key file, which must not exist, and
// removes the pending one.
//
// An admin key file that already holds exactly the pending key is a delivery that
// stopped before the pending file was removed, so it finishes it. One that holds
// anything else is left alone, with the pending file, and the start stops.
func deliver(pendingPath, keyPath, dataDir string) error {
	if err := os.Link(pendingPath, keyPath); err != nil {
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("deliver the first admin's key to %s: %w; the key stays in %s for the next start", keyPath, err, pendingPath)
		}
		pending, perr := readPrivate(pendingPath)
		delivered, kerr := readPrivate(keyPath)
		if perr != nil || kerr != nil || !bytes.Equal(pending, delivered) {
			return fmt.Errorf("%s holds a different key from %s; move away the one that isn't this server's admin's and start again", keyPath, pendingPath)
		}
	}
	if err := bootstrapStep("key linked"); err != nil {
		return err
	}
	if err := os.Remove(pendingPath); err != nil {
		return fmt.Errorf("remove %s: %w", pendingPath, err)
	}
	return syncDir(dataDir)
}

// writeSynced creates path, readable only by its owner, failing if it exists, writes
// content and syncs the file and its folder to disk.
func writeSynced(path, content string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // the server's own data folder
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return syncDir(filepath.Dir(path))
}

// readPrivate reads a file that must be a regular file, not a link, readable only by
// its owner.
func readPrivate(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("%s is a link, not a file, or others can read it (mode %v)", path, info.Mode())
	}
	return os.ReadFile(path) //nolint:gosec // checked above, in the server's own data folder
}

// syncDir syncs a folder, so a file created, linked or removed in it survives a crash.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // the server's own data folder
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// lockFile takes an exclusive lock on path, creating it readable only by its owner, and
// returns the function that releases it.
func lockFile(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // the server's own data folder
	if err != nil {
		return nil, fmt.Errorf("open the lock file %s: %w", path, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil { //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
		return nil, fmt.Errorf("lock %s: %w", path, err)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) //nolint:gosec // a file descriptor fits an int
		_ = f.Close()
	}, nil
}
