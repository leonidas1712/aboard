package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/harness"
)

func TestOpenOffersCommandsWithoutUsingCredentialsOrContactingServers(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "never-send", agentCredential{})
	p, _ := resolvePaths(e.getenv)
	if err := writeJSONFile(p.servers(), serverLogins{Names: map[string]string{srv.URL: "team"}, Servers: []serverLogin{{URL: srv.URL, Key: "never-send"}}}, 0o600); err != nil {
		t.Fatal(err)
	}
	r := e.run("open", "--json")
	var out wireError
	if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
		t.Fatal(err)
	}
	choices, _ := out.Error.Details["server_choices"].([]any)
	if out.Error.Code != "server_not_selected" || len(choices) != 1 {
		t.Fatalf("open choices: %s", r.stdout)
	}
	choice, ok := choices[0].(map[string]any)
	if !ok || choice["command"] != "aboard open team" {
		t.Fatalf("open choices: %s", r.stdout)
	}
	r = e.run("open")
	if !strings.Contains(r.stderr, "aboard open team") || calls.Load() != 0 {
		t.Fatalf("selection contacted server or omitted text choice: %+v calls=%d", r, calls.Load())
	}
}

func TestTargetedInviteWithoutPersonUsesSoleBoard(t *testing.T) {
	var codes, people atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/info":
			_, _ = w.Write([]byte(`{"version":"dev","mode":"team"}`))
		case "/v1/boards":
			_, _ = w.Write([]byte(`{"boards":[{"name":"work","id":"brd_work","visibility":"open","on_board":true}]}`))
		case "/v1/boards/work":
			_, _ = w.Write([]byte(`{"name":"work","id":"brd_work","visibility":"open","on_board":true}`))
		case "/v1/boards/work/join-codes":
			codes.Add(1)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"jc_test","role":"member","code":"TEST","join_line":"Join Aboard board work as member with code TEST","expires_at":"2030-01-01T00:00:00Z"}`))
		case "/v1/invites":
			people.Add(1)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"id":"inv_test","invite":"TEST","expires_at":"2030-01-01T00:00:00Z"}`))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	e := lifecycleMachine(t, srv.URL, "abh_person", agentCredential{})
	for _, args := range [][]string{{"invite", "--server", srv.URL, "--json"}, {"invite", "--server=" + srv.URL, "--json"}} {
		if r := e.run(args...); r.code != 0 {
			t.Errorf("targeted board invitation: %+v", r)
		}
	}
	if codes.Load() != 2 || people.Load() != 0 {
		t.Errorf("codes=%d person invites=%d", codes.Load(), people.Load())
	}
	for _, args := range [][]string{{"invite", "--server", srv.URL, "--pairing", "work"}, {"invite", "--person", "--role", "member"}, {"invite", "--person", "--guest", "pat"}} {
		if r := e.run(args...); r.code != exitUsage {
			t.Errorf("incompatible flags must be usage: %v %+v", args, r)
		}
	}
	if codes.Load() != 2 || people.Load() != 0 {
		t.Error("invalid flags made a write")
	}
	if r := e.run("invite", "--server", "--json"); r.code != 0 || people.Load() != 1 || codes.Load() != 2 {
		t.Fatalf("bare server alias must remain person invitation: %+v", r)
	}
}

func TestDoctorLegacyLinkWarningOnlyForRegularFiles(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	for _, kind := range []string{"regular", "directory", "symlink", "missing"} {
		t.Run(kind, func(t *testing.T) {
			e := lifecycleMachine(t, "https://unused.example", "never-send", agentCredential{})
			p, _ := resolvePaths(e.getenv)
			if err := writeJSONFile(p.servers(), serverLogins{}, 0o600); err != nil {
				t.Fatal(err)
			}
			if kind == "directory" {
				e.dir = e.home
			}
			link := filepath.Join(e.dir, projectFileName)
			if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			switch kind {
			case "regular":
				if err := os.WriteFile(link, []byte("legacy"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				if err := os.Mkdir(link, 0o700); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				target := filepath.Join(e.dir, "target")
				if err := os.WriteFile(target, []byte("legacy"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, link); err != nil {
					t.Fatal(err)
				}
			}
			var stdout bytes.Buffer
			a := e.app(&stdout, &bytes.Buffer{})
			a.json = true
			a.harnesses = harness.Set{}
			fakeDaemonAnswering(t, a, func(delivery.Request) delivery.Response { return delivery.Response{V: 1} })
			_ = runDoctor(context.Background(), a, []string{"--json"})
			r := lifecycleRun{stdout: stdout.String()}
			var out struct {
				Checks []doctorCheck `json:"checks"`
			}
			if err := json.Unmarshal([]byte(r.stdout), &out); err != nil {
				t.Fatalf("doctor crashed: %+v %v", r, err)
			}
			found := false
			for _, check := range out.Checks {
				if check.Name == "legacy_folder_link" {
					found = true
					if !strings.Contains(check.Message, "can be deleted") {
						t.Errorf("warning: %+v", check)
					}
				}
			}
			if kind == "directory" {
				if r := e.run("servers", "--json"); r.code != 0 {
					t.Fatalf("home-directory command: %+v", r)
				}
			}
			if found != (kind == "regular") {
				t.Errorf("legacy warning for %s=%v", kind, found)
			}
		})
	}
}

func TestLegacyDaemonIssuerRefusalRequiresUpgradeWithoutRetry(t *testing.T) {
	e := lifecycleMachine(t, "https://first.example", "never-human", agentCredential{})
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	var calls atomic.Int32
	fakeDaemonAnswering(t, a, func(delivery.Request) delivery.Response {
		calls.Add(1)
		return delivery.Response{V: 1, Error: &delivery.WireError{Code: "session_on_another_server", Message: "one issuer only", Hint: "Use another session"}}
	})
	_, err := a.callDaemon(context.Background(), delivery.Request{Op: delivery.OpJoin, Harness: "codex", Session: "same", Agent: &delivery.AgentRef{Server: "https://second.example", Board: "work"}})
	if err == nil || asError(err).Code != "session_on_another_server" || !strings.Contains(asError(err).Hint, "upgrade") || !strings.Contains(asError(err).Hint, "restart") || calls.Load() != 1 {
		t.Fatalf("legacy refusal retried or missing fix: %v calls=%d", err, calls.Load())
	}
}
