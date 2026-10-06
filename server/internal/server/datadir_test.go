package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A team server's data folder is made owner-only, and every file the server writes in
// it is readable only by its owner, the database's WAL and shared-memory files included.
func TestATeamServersDataIsOwnerOnly(t *testing.T) {
	data := filepath.Join(t.TempDir(), "nested", "data")
	s := startTeam(t, data)
	s.stop()
	info, err := os.Lstat(data)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the data folder: %v %v", info, err)
	}
	entries, err := os.ReadDir(data)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s is mode %v", e.Name(), info.Mode())
		}
	}
	// A restart accepts what the first start left.
	startTeam(t, data)
}

// A data folder, or a file of the server's in it, that is a link or that others can
// open stops a team server's start before it opens anything, with an error that names
// the path and how to fix it.
func TestAnUnsafeDataFolderStopsATeamServer(t *testing.T) {
	cases := map[string]struct {
		prepare func(t *testing.T, data string) string // returns the path the error names
		hint    string
	}{
		"a folder others can open": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o755)
			return data
		}, "chmod 700"},
		"a folder that is a link": {func(t *testing.T, data string) string {
			target := data + "-real"
			mkdir(t, target, 0o700)
			if err := os.Symlink(target, data); err != nil {
				t.Fatal(err)
			}
			return data
		}, "not a link"},
		"a file, not a folder": {func(t *testing.T, data string) string {
			write(t, data, 0o600)
			return data
		}, "not a link"},
		"a database that is a link": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o700)
			write(t, data+"-elsewhere.db", 0o600)
			if err := os.Symlink(data+"-elsewhere.db", filepath.Join(data, "aboard.db")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(data, "aboard.db")
		}, "not a link"},
		"a database others can read": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o700)
			write(t, filepath.Join(data, "aboard.db"), 0o644)
			return filepath.Join(data, "aboard.db")
		}, "chmod 600"},
		"a pid file that is a link": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o700)
			write(t, data+"-target", 0o600)
			if err := os.Symlink(data+"-target", filepath.Join(data, "server.pid")); err != nil {
				t.Fatal(err)
			}
			return filepath.Join(data, "server.pid")
		}, "not a link"},
		"an admin key others can read": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o700)
			write(t, filepath.Join(data, AdminKeyFile), 0o644)
			return filepath.Join(data, AdminKeyFile)
		}, "chmod 600"},
		"a WAL file others can read": {func(t *testing.T, data string) string {
			mkdir(t, data, 0o700)
			write(t, filepath.Join(data, "aboard.db-wal"), 0o640)
			return filepath.Join(data, "aboard.db-wal")
		}, "chmod 600"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			data := filepath.Join(t.TempDir(), "data")
			named := c.prepare(t, data)
			_, err := launchTeam(t, data)
			if err == nil || !strings.Contains(err.Error(), named) || !strings.Contains(err.Error(), c.hint) {
				t.Fatalf("the start: %v", err)
			}
			if _, err := os.Lstat(filepath.Join(data, "aboard.db-wal")); err == nil && name != "a WAL file others can read" {
				t.Fatal("the database was opened")
			}
		})
	}
}

// dataDir is a data folder for a team server that doesn't exist yet, so the server
// makes it owner-only, as on a fresh volume.
func dataDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "data")
}

func mkdir(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Mkdir(path, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // past the umask
		t.Fatal(err)
	}
}

func write(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, nil, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil { // past the umask
		t.Fatal(err)
	}
}
