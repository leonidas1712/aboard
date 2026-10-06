package server

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stopAt makes the first admin's bootstrap fail at step, as a crash or a failed write
// there would, until the test ends or the returned function is called.
func stopAt(t *testing.T, step string) func() {
	t.Helper()
	bootstrapStep = func(s string) error {
		if s == step {
			return errors.New("stopped at " + s)
		}
		return nil
	}
	undo := func() { bootstrapStep = func(string) error { return nil } }
	t.Cleanup(undo)
	return undo
}

// adminWorks checks that key signs in as the server's admin, alex.
func adminWorks(t *testing.T, s *teamServer, key string) {
	t.Helper()
	resp, me := s.do("GET", "/v1/me", "team.example.com:8443", map[string]string{"Authorization": "Bearer " + key}, nil)
	if resp.StatusCode != http.StatusOK || me["name"] != "alex" || me["server_role"] != "admin" {
		t.Fatalf("the admin key: %d %v", resp.StatusCode, me)
	}
}

// onlyTheKeyFile checks that the data folder holds the admin key file and no file left
// over from a bootstrap.
func onlyTheKeyFile(t *testing.T, data string) {
	t.Helper()
	left, _ := filepath.Glob(filepath.Join(data, AdminKeyFile+"*"))
	if len(left) != 1 || left[0] != filepath.Join(data, AdminKeyFile) {
		t.Fatalf("files left in the data folder: %v", left)
	}
}

// A start that fails before the admin is made, whether writing the key failed or the
// server stopped after writing it, makes no admin; the next start makes one and
// delivers its key.
func TestABootstrapStoppedBeforeTheAdminStartsOver(t *testing.T) {
	for _, step := range []string{"write key", "key written"} {
		t.Run(step, func(t *testing.T) {
			data := dataDir(t)
			undo := stopAt(t, step)
			if _, err := launchTeam(t, data); err == nil || !strings.Contains(err.Error(), "stopped at "+step) {
				t.Fatalf("the start went on: %v", err)
			}
			if _, err := os.Stat(filepath.Join(data, AdminKeyFile)); !os.IsNotExist(err) {
				t.Fatalf("a key file was delivered: %v", err)
			}
			undo()
			s := startTeam(t, data)
			if !strings.Contains(s.log.String(), "first admin created") {
				t.Fatalf("the next start made no admin:\n%s", s.log)
			}
			adminWorks(t, s, s.adminKey())
			onlyTheKeyFile(t, data)
		})
	}
}

// A start that stops after making the admin but before delivering the key leaves the
// key waiting; the next start delivers that same key and makes no other admin.
func TestABootstrapStoppedAfterTheAdminDeliversItsKeyOnRestart(t *testing.T) {
	data := dataDir(t)
	undo := stopAt(t, "admin created")
	if _, err := launchTeam(t, data); err == nil {
		t.Fatal("the start went on")
	}
	if _, err := os.Stat(filepath.Join(data, AdminKeyFile)); !os.IsNotExist(err) {
		t.Fatalf("a key file was delivered: %v", err)
	}
	undo()
	s := startTeam(t, data)
	if strings.Contains(s.log.String(), "first admin created") || !strings.Contains(s.log.String(), `"msg":"first admin's key delivered"`) {
		t.Fatalf("the restart's log:\n%s", s.log)
	}
	key := s.adminKey()
	if strings.Contains(s.log.String(), key) {
		t.Fatalf("the log shows the key:\n%s", s.log)
	}
	adminWorks(t, s, key)
	onlyTheKeyFile(t, data)
	resp, people := s.do("GET", "/v1/people", "team.example.com:8443", map[string]string{"Authorization": "Bearer " + key}, nil)
	if list, _ := people["people"].([]any); resp.StatusCode != http.StatusOK || len(list) != 1 {
		t.Fatalf("people after the restart: %d %v", resp.StatusCode, people)
	}
}

// An admin key file in a data folder whose database has no one is from somewhere else:
// the start refuses, makes no admin and leaves the file alone.
func TestAStrayAdminKeyFileStopsTheFirstStart(t *testing.T) {
	data := dataDir(t)
	stray := filepath.Join(data, AdminKeyFile)
	mkdir(t, data, 0o700)
	if err := os.WriteFile(stray, []byte("abh_not_this_servers\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := launchTeam(t, data)
	if err == nil || !strings.Contains(err.Error(), stray) {
		t.Fatalf("the start with a stray key file: %v", err)
	}
	if raw, _ := os.ReadFile(stray); string(raw) != "abh_not_this_servers\n" { //nolint:gosec // the test's own folder
		t.Fatalf("the stray file changed: %q", raw)
	}
	if err := os.Remove(stray); err != nil {
		t.Fatal(err)
	}
	s := startTeam(t, data)
	adminWorks(t, s, s.adminKey())
}
