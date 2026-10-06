package server

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// dataFiles are the files a team server keeps in its data folder; each that exists must
// be a regular file only its owner can read.
var dataFiles = []string{
	"aboard.db", "aboard.db-wal", "aboard.db-shm", "server.pid",
	AdminKeyFile, pendingKeyFile, bootstrapLock,
}

// prepareTeamData checks a team server's data folder before anything in it is opened,
// since everything the server holds is there: the folder itself, not a link to one
// (links in the path above it are followed), owned by this user and opened by no one
// else; and each of the server's files in it a regular file, not a link, owned by this
// user and readable by no one else. A folder that doesn't exist is made owner-only.
// The database is created empty and owner-only, so that the files SQLite makes beside
// it, which take its mode, are owner-only too.
func prepareTeamData(dir string) error {
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
			return fmt.Errorf("create the data folder %s: %w", dir, err)
		}
		// Another start may make it at the same moment; what it made is checked below.
		if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("create the data folder %s: %w", dir, err)
		}
		info, err = os.Lstat(dir)
	}
	if err != nil {
		return fmt.Errorf("check the data folder %s: %w", dir, err)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("the data folder %s must be a folder, not a link or a file; give the folder's own path", dir)
	}
	if err := ownedByMe(dir, info); err != nil {
		return err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("others can open the data folder %s (mode %04o); run chmod 700 %s", dir, info.Mode().Perm(), dir)
	}
	for _, name := range dataFiles {
		path := filepath.Join(dir, name)
		info, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("check %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s must be a file, not a link or a folder; move it away", path)
		}
		if err := ownedByMe(path, info); err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			return fmt.Errorf("others can read %s (mode %04o); run chmod 600 %s", path, info.Mode().Perm(), path)
		}
	}
	db := filepath.Join(dir, "aboard.db")
	f, err := os.OpenFile(db, os.O_CREATE|os.O_EXCL|os.O_WRONLY|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // the server's own data folder
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("create the database %s: %w", db, err)
	}
	return f.Close()
}

// ownedByMe checks that the file or folder at path, described by info, belongs to the
// user this server runs as.
func ownedByMe(path string, info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := os.Getuid(); int(st.Uid) != uid {
		return fmt.Errorf("%s belongs to user %d, not to user %d, which this server runs as; give it to that user with chown", path, st.Uid, uid)
	}
	return nil
}
