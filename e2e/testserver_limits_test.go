//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestServerProvisioningLimits(t *testing.T) {
	for _, c := range []struct {
		name      string
		env, args []string
		raised    bool
	}{
		{name: "production default"},
		{name: "explicit flag", args: []string{"--test-server"}, raised: true},
		{name: "temporary container", env: []string{"ABOARD_TEST_SERVER=true"}, raised: true},
		{name: "flag disables environment", env: []string{"ABOARD_TEST_SERVER=true"}, args: []string{"--test-server=false"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := startTeamServerOptions(t, c.env, c.args)
			raw, err := os.ReadFile(filepath.Join(s.data, "admin-key"))
			if err != nil {
				t.Fatal(err)
			}
			key := strings.TrimSpace(string(raw))
			if status, _ := s.call(http.MethodGet, "/v1/me", "", nil); status != http.StatusUnauthorized {
				t.Fatalf("test limits bypassed authentication: %d", status)
			}
			if status, _ := s.call(http.MethodPost, "/v1/boards", key, map[string]any{"name": "limits", "template": "general"}); status != http.StatusCreated {
				t.Fatalf("create board: %d", status)
			}
			limited := false
			for i := 0; i < 65; i++ {
				status, _ := s.call(http.MethodPost, "/v1/join", key, map[string]any{"board": "limits", "role": "member", "name": fmt.Sprintf("agent-%d", i), "harness": "codex", "session": fmt.Sprintf("codex:limits-%d", i)})
				if status == http.StatusTooManyRequests {
					limited = true
					break
				}
				if status != http.StatusCreated {
					t.Fatalf("join %d: HTTP %d", i, status)
				}
			}
			if limited == c.raised {
				t.Fatalf("join limit: refused=%v, test-server=%v", limited, c.raised)
			}
			notice := false
			for _, line := range strings.Split(s.log.String(), "\n") {
				var log struct {
					Msg, Level     string
					Joins          int `json:"joins_per_minute"`
					Connects       int `json:"connects_per_minute"`
					ServerConnects int `json:"connects_per_minute_server"`
				}
				if json.Unmarshal([]byte(line), &log) == nil && log.Msg == "test rate limits enabled" {
					notice = true
					if log.Level != "WARN" || log.Joins != 10000 || log.Connects != 10000 || log.ServerConnects != 10000 {
						t.Fatalf("startup warning omitted raised limits: %+v", log)
					}
				}
			}
			if notice != c.raised {
				t.Fatalf("test mode startup notice=%v, want %v", notice, c.raised)
			}
			if !c.raised {
				return
			}
			for i := 0; i < 21; i++ {
				status, invite := s.call(http.MethodPost, "/v1/invites", key, map[string]any{})
				if status != http.StatusCreated {
					t.Fatalf("invite %d: %d", i, status)
				}
				status, _ = s.call(http.MethodPost, "/v1/connect", "", map[string]any{"invite": invite["invite"], "handle": fmt.Sprintf("person-%d", i), "key_name": "load"})
				if status != http.StatusCreated {
					t.Fatalf("connect %d: %d", i, status)
				}
			}
		})
	}
}

func TestTestServerRefusesMalformedEnvironmentBeforeState(t *testing.T) {
	e := newEnv(t)
	data := filepath.Join(e.home, "refused-data")
	r := e.exec([]string{"ABOARD_TEST_SERVER=not-a-boolean"}, "", "serve", "--team", "--public-url", "https://test.example.com", "--data", data, "--json")
	if r.code != 2 || errorCode(t, r.json(t)) != "invalid_request" || !strings.Contains(r.stdout, "ABOARD_TEST_SERVER") {
		t.Fatalf("bad test server environment: %s", r)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("invalid test opt-in opened state: %v", err)
	}
}
