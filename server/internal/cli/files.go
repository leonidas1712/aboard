package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// writeFileAtomic writes data to path through a temporary file in the same directory,
// so a reader never sees a half-written file.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file in %s: %w", dir, err)
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }() // a no-op once the rename succeeded
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set permissions on %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	return nil
}

// writeJSONFile writes v as indented JSON.
func writeJSONFile(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeFileAtomic(path, append(data, '\n'), perm)
}

// readJSONFile decodes path into v. It reports whether the file existed.
func readJSONFile(path string, v any) (bool, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		return true, &Error{
			Code: "invalid_request", Message: "The file " + path + " is not valid JSON.",
			Hint: "Fix or delete the file, then run the command again.", Err: err,
		}
	}
	return true, nil
}

// updateJSONFile reads path into v, calls change, and writes v back, holding an exclusive
// lock on path+".lock" throughout, so concurrent aboard commands on one machine never
// lose each other's changes.
func updateJSONFile(path string, v any, perm os.FileMode, change func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	lock, err := os.OpenFile(filepath.Clean(path+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open lock for %s: %w", path, err)
	}
	defer func() { _ = lock.Close() }() // closing the file releases the lock
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	if _, err := readJSONFile(path, v); err != nil {
		return err
	}
	if err := change(); err != nil {
		return err
	}
	return writeJSONFile(path, v, perm)
}
