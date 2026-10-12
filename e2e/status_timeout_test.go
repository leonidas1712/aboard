//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestStatusSucceedsAfterAStartupProbeRecovers(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("up")
	target, err := url.Parse("http://" + e.addr)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(r *http.Request) { director(r); r.Host = target.Host }
	var probes atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/info" && probes.Add(1) == 1 {
			<-r.Context().Done()
			return
		}
		proxy.ServeHTTP(w, r)
	}))
	t.Cleanup(peer.Close)
	out := e.exec([]string{"ABOARD_LOCAL_ADDR=" + strings.TrimPrefix(peer.URL, "http://")}, "", "status", "--json")
	if out.code != 0 || field(t, out.json(t), "server_running") != true {
		t.Fatalf("recovered startup probe made status fail: %s", out)
	}
}

func TestStatusKeepsDiagnosticsWhenServerTimesOut(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		infoAnswers bool
		json        bool
		bodyStalls  bool
	}{
		{"black-hole", false, false, false},
		{"board-read", true, false, false},
		{"json-board-read", true, true, false},
		{"body-read", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := newEnv(t)
			e.vars = append(e.vars, "ABOARD_E2E_STATUS_BUDGET=")
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.infoAnswers && r.URL.Path == "/v1/info" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"name":"aboard","version":"dev","server_id":"srv_timeout","mode":"team"}`))
					return
				}
				if tc.bodyStalls {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
				<-r.Context().Done()
			}))
			t.Cleanup(peer.Close)
			if err := os.MkdirAll(e.configDir(), 0o700); err != nil {
				t.Fatal(err)
			}
			config := `{"default":"` + peer.URL + `","servers":[{"url":"` + peer.URL + `","key":"isolated-test-key"}]}`
			if err := os.WriteFile(filepath.Join(e.configDir(), "servers.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			args := []string{"status", "--board", "general"}
			if tc.json {
				args = append(args, "--json")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 9*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, e.bin, args...)
			cmd.Env, cmd.Dir = e.vars, e.dir
			data, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("status exceeded its budget: %s", data)
			}
			if err == nil {
				t.Fatalf("timed-out status succeeded: %s", data)
			}
			out := string(data)
			if !strings.Contains(out, "could not reach "+peer.URL+":") {
				t.Fatalf("timeout lacks server/reason: %s", out)
			}
			if tc.json {
				v := (result{stdout: out}).json(t)
				if errorCode(t, v) != "server_unreachable" {
					t.Fatalf("unexpected error: %v", v)
				}
				partial := field(t, v, "error.details.status").(map[string]any)
				matchesCLISpec(t, "StatusOutput", partial)
			} else {
				for _, want := range []string{"Server:", "Daemon:", "Setup:"} {
					if !strings.Contains(out, want) {
						t.Errorf("lost %s diagnostics: %s", want, out)
					}
				}
			}
		})
	}
}
