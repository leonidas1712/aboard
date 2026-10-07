//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestDoctorShowsSelectedServerVersionWithoutSendingCredentials(t *testing.T) {
	for _, tc := range []struct{ version, level, code string }{
		{"0.0.9", "ok", ""},
		{"0.1.0", "ok", ""},
		{"0.2.0", "warning", "version_skew"},
		{"1.0.0", "ok", ""},
		{"2.0.0", "warning", "version_skew"},
		{"0.1.0-rc.1+dev.abc", "ok", ""},
		{"dev", "warning", "version_unknown"},
	} {
		t.Run(tc.version, func(t *testing.T) {
			e := newEnv(t)
			// A fixed stamped client keeps these boundary cases independent of release bumps.
			e.bin = oldBinary
			var mu sync.Mutex
			var ua, auth string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				ua, auth = r.UserAgent(), r.Header.Get("Authorization")
				mu.Unlock()
				if r.URL.Path != "/v1/info" {
					t.Errorf("unexpected API path %s", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]string{"name": "aboard", "version": tc.version, "server_id": "srv_peer", "mode": "team"})
			}))
			t.Cleanup(peer.Close)
			project, err := json.Marshal(map[string]any{"server": map[string]string{"name": "peer", "url": peer.URL}, "board": "docs"})
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(e.dir, ".aboard"), project, 0o600); err != nil {
				t.Fatal(err)
			}
			r := e.runExit("doctor", "--json")
			matchesCLISpec(t, "DoctorOutput", r.json(t))
			var check map[string]any
			for _, raw := range field(t, r.json(t), "checks").([]any) {
				c := raw.(map[string]any)
				if c["name"] == "server_version" {
					check = c
				}
			}
			if check == nil {
				t.Fatal("doctor omitted selected server version check")
			}
			if check["level"] != tc.level || (tc.code != "" && check["code"] != tc.code) || (tc.code == "" && check["code"] != nil) {
				t.Fatalf("version check: %v", check)
			}
			msg := check["message"].(string)
			if !strings.Contains(msg, peer.URL) || !strings.Contains(msg, tc.version) || !strings.Contains(msg, oldVersion) {
				t.Fatalf("versions or server missing: %s", msg)
			}
			if tc.code == "version_skew" && !strings.Contains(check["fix"].(string), "aboard upgrade") {
				t.Fatalf("older client fix: %v", check["fix"])
			}
			mu.Lock()
			gotUA, gotAuth := ua, auth
			mu.Unlock()
			if gotUA != "aboard/"+oldVersion || gotAuth != "" {
				t.Fatalf("public version request: User-Agent %q; credential sent=%v", gotUA, gotAuth != "")
			}
			// The warning never prevents a command that needs no server operation.
			e.run("version", "--json")
		})
	}
}
