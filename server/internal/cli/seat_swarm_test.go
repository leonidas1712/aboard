package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
	"github.com/leonidas1712/aboard/server/internal/delivery/control"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
	"github.com/leonidas1712/aboard/server/internal/delivery/launchtickets"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
	"github.com/leonidas1712/aboard/server/internal/launcher"
)

func TestSwarmChecksItsRecordedSeatWithoutUsingAReusedNamesToken(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Error("swarm sent a replacement seat's credential")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"ended","hint":"join"}}`))
	}))
	defer srv.Close()
	a := seatApp(t)
	for _, c := range []agentCredential{
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"},
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"},
	} {
		if err := a.saveCredential(c); err != nil {
			t.Fatal(err)
		}
	}
	rows := []swarmAgent{{Name: "writer", memberID: "mem_old"}}
	a.checkSeats(context.Background(), a.serverRefFor(srv.URL), "docs", rows)
	if requests.Load() != 1 || deref(rows[0].SeatCredential) != seatEnded {
		t.Fatalf("recorded seat check: %+v, %d requests", rows, requests.Load())
	}
}

func TestSwarmDoesNotShowAReplacementSeatsPresence(t *testing.T) {
	a := seatApp(t)
	presence := api.MemberPresenceWorking
	got := a.swarmRow("writer", &swarmAgentRecord{MemberID: "mem_old", Mode: "headless"}, "running",
		delivery.BindingStatus{}, map[string]api.Member{"writer": {Id: "mem_new", Name: "writer", Presence: &presence}})
	if got.Presence != nil || got.Seated {
		t.Fatalf("replacement presence attached to old record: %+v", got)
	}
}

func TestSwarmSelectsItsRecordedIdBeforeTheSharedDisplayName(t *testing.T) {
	a := seatApp(t)
	creds := credentials{Agents: []agentCredential{
		{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"},
		{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"},
	}}
	in := upInput{
		srv: serverRef{URL: "https://team.example"}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: creds,
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {MemberID: "mem_old", Handle: "old-session"}}},
	}
	ref, err := a.swarmSeat(t.Context(), in)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("recorded seat not selected: %+v %v", ref, err)
	}
}

func TestLegacySwarmCannotAdoptUnrelatedBackfillProvenance(t *testing.T) {
	a := seatApp(t)
	cred := agentCredential{
		Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new",
		Legacy: &legacyCredentialIdentity{Board: "docs", Name: "different-old-name"},
	}
	if err := a.saveCredential(cred); err != nil {
		t.Fatal(err)
	}
	in := upInput{
		srv: serverRef{URL: cred.Server}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: credentials{Agents: []agentCredential{cred}},
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {Handle: "old-session"}}},
	}
	if ref, err := a.swarmSeat(t.Context(), in); err == nil {
		t.Fatalf("unrelated provenance adopted old session: %+v", ref)
	}
}

func TestUnresolvedSwarmDoesNotShowAReplacementsServerMetadata(t *testing.T) {
	a := seatApp(t)
	presence := api.MemberPresenceWorking
	mode := api.MemberDeliveryModeAll
	got := a.swarmRow("writer", &swarmAgentRecord{Mode: "headless"}, "running", delivery.BindingStatus{},
		map[string]api.Member{"writer": {Id: "mem_new", Name: "writer", Presence: &presence, DeliveryMode: &mode}})
	if got.Presence != nil || got.Delivery != nil || got.Seated {
		t.Fatalf("unresolved legacy row adopted replacement metadata: %+v", got)
	}
}

func TestLegacySwarmResumesOnlyItsVerifiedOriginalToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer aba_old" {
			t.Errorf("replacement token used: %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.Me{Id: "mem_old", Kind: api.MeKindAgent, Name: "writer", Board: ptr("docs")})
	}))
	defer srv.Close()
	a := seatApp(t)
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", Token: "aba_old"}); err != nil {
		t.Fatal(err)
	}
	original := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: "writer"}
	if _, err := (daemonTokens{a}).ResolveAgent(t.Context(), original); err != nil {
		t.Fatal(err)
	}
	if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_new", Token: "aba_new"}); err != nil {
		t.Fatal(err)
	}
	creds, err := a.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	in := upInput{
		srv: serverRef{URL: srv.URL}, board: "docs", spec: swarmSpec{Name: "writer"}, creds: creds,
		rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {Handle: "old-session"}}},
	}
	ref, err := a.swarmSeat(t.Context(), in)
	if err != nil || ref.MemberID != "mem_old" {
		t.Fatalf("verified original seat not resumed: %+v %v", ref, err)
	}
}

func TestSwarmRefusesARenamedRecordedSeatBeforeAnyJoin(t *testing.T) {
	bin := t.TempDir()
	for name, body := range map[string]string{
		"aboard-launcher-seat-test": "#!/bin/sh\ncat >/dev/null\necho '{\"v\":1,\"name\":\"seat-test\",\"modes\":[\"interactive\"],\"state\":\"running\",\"handle\":\"old-session\"}'\n",
		"codex":                     "#!/bin/sh\nexit 0\n",
	} {
		if err := writeFileAtomic(filepath.Join(bin, name), []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, tc := range []struct {
		label                  string
		replacement, staleName bool
	}{
		{label: "old name unused"},
		{label: "old name belongs to another seat", replacement: true},
		{label: "server renamed but cached name is stale", staleName: true},
	} {
		label := tc.label
		t.Run(label, func(t *testing.T) {
			var joins atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.URL.Path == "/v1/me":
					if r.Header.Get("Authorization") != "Bearer aba_old" {
						t.Errorf("original seat verification used %q", r.Header.Get("Authorization"))
					}
					_ = json.NewEncoder(w).Encode(api.Me{Id: "mem_old", Kind: api.MeKindAgent, Name: "renamed", Board: ptr("docs")})
				case r.URL.Path == "/v1/join" && r.Method == http.MethodPost:
					joins.Add(1)
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(api.JoinResult{Board: api.Board{Name: "docs"}, Agent: api.Member{Id: "mem_unintended", Name: "writer", Kind: api.MemberKindAgent}, Token: "aba_unintended"})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			a := seatApp(t)
			original := agentCredential{Server: srv.URL, Board: "docs", Name: "renamed", MemberID: "mem_old", Token: "aba_old"}
			if tc.staleName {
				original.Name = "writer"
			}
			if err := a.saveCredential(original); err != nil {
				t.Fatal(err)
			}
			c, err := a.newClient(serverRef{URL: srv.URL}, "abh_person", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			members := map[string]api.Member{}
			if tc.replacement {
				members["writer"] = api.Member{Id: "mem_other_owner", Name: "writer", Kind: api.MemberKindAgent, Status: api.Active}
			}
			in := upInput{
				c: c, srv: serverRef{URL: srv.URL}, board: "docs", swarm: "s", file: swarmFile{dir: t.TempDir()},
				spec: swarmSpec{Name: "writer", Harness: "codex", Launcher: "seat-test"}, creds: credentials{Agents: []agentCredential{original}}, members: members,
				rec: &swarmRecord{Agents: map[string]*swarmAgentRecord{"writer": {MemberID: "mem_old", Handle: "old-session", Launcher: "seat-test"}}},
			}
			_, err = a.upAgent(t.Context(), in)
			if err == nil || asError(err).Code != "agent_not_selected" {
				t.Errorf("rename refusal: %v", err)
			}
			if err != nil && (!strings.Contains(asError(err).Hint, "writer") || !strings.Contains(asError(err).Hint, "renamed") || !strings.Contains(asError(err).Hint, "aboard.yaml")) {
				t.Errorf("rename hint misses configured/current names: %v", err)
			}
			if joins.Load() != 0 {
				t.Fatalf("rename attempted %d new joins", joins.Load())
			}
		})
	}
}

func TestRenamedSwarmStopsTheOriginalHandleBeforeStartingAgain(t *testing.T) {
	bin := t.TempDir()
	stopped, logPath := filepath.Join(bin, "old-stopped"), filepath.Join(bin, "launcher.jsonl")
	script := `#!/bin/sh
read request
echo "$request" >> ` + shellWord(logPath) + `
case "$request" in
 *'"op":"info"'*) echo '{"v":1,"name":"seat-test","modes":["interactive"]}' ;;
 *'"op":"status"'*)
  case "$request" in
   *'"handle":"old-session"'*)
    if [ -f ` + shellWord(stopped) + ` ]; then echo '{"v":1,"state":"exited"}'; else echo '{"v":1,"state":"running"}'; fi ;;
   *) echo '{"v":1,"state":"running"}' ;;
  esac ;;
 *'"op":"stop"'*) touch ` + shellWord(stopped) + `; echo '{"v":1,"state":"exited"}' ;;
 *'"op":"start"'*) echo '{"v":1,"handle":"new-session"}' ;;
esac
`
	if err := writeFileAtomic(filepath.Join(bin, "aboard-launcher-seat-test"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(filepath.Join(bin, "codex"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	var joins atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		id, name := "mem_original", "renamed"
		if r.Header.Get("Authorization") == "Bearer aba_replacement" {
			id, name = "mem_replacement", "writer"
		}
		switch r.URL.Path {
		case "/v1/me":
			_ = json.NewEncoder(w).Encode(api.Me{Id: id, Kind: api.MeKindAgent, Name: name, Board: ptr("docs")})
		case "/v1/inbox":
			_ = json.NewEncoder(w).Encode(api.Inbox{Board: "docs", Agent: name, MemberId: &id, Messages: []api.Message{}})
		case "/v1/join":
			joins.Add(1)
			w.WriteHeader(http.StatusForbidden)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	a := seatApp(t)
	a.env.Stdout, a.env.Stderr = io.Discard, io.Discard
	a.env.Executable = os.Executable
	for _, cred := range []agentCredential{
		{Server: srv.URL, Board: "docs", Name: "renamed", MemberID: "mem_original", Token: "aba_original"},
		{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_replacement", Token: "aba_replacement"},
	} {
		if err := a.saveCredential(cred); err != nil {
			t.Fatal(err)
		}
	}
	c, err := a.newClient(serverRef{URL: srv.URL}, "abh_person", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(p.state, 0o700); err != nil {
		t.Fatal(err)
	}
	journal, err := sqlitejournal.Open(t.Context(), p.deliveryDB())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := control.Listen(p.socket())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- delivery.Run(ctx, delivery.Config{
			Journal: journal, Control: listener, Clock: clock.Real{}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PID: os.Getpid(), Build: currentBuild(),
			Adapters:     []delivery.Adapter{deliverytest.NewFakeAdapter("codex", false)},
			ResolveAgent: (daemonTokens{a: a}).ResolveAgent,
			Connect:      func(url string) delivery.Server { return apiserver.New(url, daemonTokens{a: a}, a.env.Rand) },
		})
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		if err := journal.Close(); err != nil {
			t.Error(err)
		}
	})
	bindSession := func(session, id, name string) {
		t.Helper()
		if _, err := a.callDaemon(t.Context(), delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: session}); err != nil {
			t.Fatal(err)
		}
		ref := delivery.AgentRef{Server: srv.URL, Board: "docs", Name: name, MemberID: id}
		if _, err := a.callDaemon(t.Context(), delivery.Request{Op: delivery.OpBind, Harness: "codex", Session: session, Agent: &ref}); err != nil {
			t.Fatal(err)
		}
	}
	bindSession("old-conversation", "mem_original", "renamed")
	bindSession("replacement-conversation", "mem_replacement", "writer")
	rec := &swarmRecord{Server: srv.URL, Board: "docs", Agents: map[string]*swarmAgentRecord{
		"writer": {MemberID: "mem_original", Handle: "old-session", Launcher: "seat-test", Harness: "codex", Mode: "interactive"},
	}}
	if err := a.saveSwarm("s", rec); err != nil {
		t.Fatal(err)
	}
	creds, err := a.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	in := upInput{
		c: c, srv: serverRef{URL: srv.URL}, board: "docs", swarm: "s", file: swarmFile{dir: t.TempDir()},
		spec: swarmSpec{Name: "renamed", Harness: "codex", Launcher: "seat-test"}, rec: rec, creds: creds, tickets: launchtickets.Dir(p.launches()),
		members: map[string]api.Member{"writer": {Id: "mem_replacement", Name: "writer", Status: api.Active}, "renamed": {Id: "mem_original", Name: "renamed", Status: api.Active}},
	}
	if _, err := a.upAgent(t.Context(), in); err == nil || asError(err).Code != "agent_not_selected" || !strings.Contains(asError(err).Hint, "aboard swarm down writer --swarm s") {
		t.Fatalf("running renamed handle was not refused with working recovery: %v", err)
	}
	if err := runSwarmDown(t.Context(), a, []string{"writer", "--swarm", "s"}); err != nil {
		t.Fatal(err)
	}
	afterDown := a.daemonBindings(t.Context())
	if afterDown[(delivery.AgentRef{Server: srv.URL, MemberID: "mem_original"}).Key()].Open ||
		!afterDown[(delivery.AgentRef{Server: srv.URL, MemberID: "mem_replacement"}).Key()].Open {
		t.Fatal("down did not end only the original seat's session")
	}
	rec, _, err = a.readSwarm("s")
	if err != nil {
		t.Fatal(err)
	}
	in.rec = rec
	got, err := a.upAgent(t.Context(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Action != actionStarted || in.rec.Agents["renamed"].MemberID != "mem_original" || in.rec.Agents["renamed"].Handle != "new-session" {
		t.Fatalf("wrong new launch: %+v %+v", got, in.rec)
	}
	if _, exists := in.rec.Agents["writer"]; exists {
		t.Fatal("old selector retained authority to end the new binding")
	}
	if err := a.saveSwarm("s", in.rec); err != nil {
		t.Fatal(err)
	}
	bindSession("new-conversation", "mem_original", "renamed")
	if err := runSwarmDown(t.Context(), a, []string{"writer", "--swarm", "s"}); err != nil {
		t.Fatal(err)
	}
	states := a.daemonBindings(t.Context())
	for _, id := range []string{"mem_original", "mem_replacement"} {
		if !states[(delivery.AgentRef{Server: srv.URL, MemberID: id}).Key()].Open {
			t.Fatalf("stale selector ended seat %s", id)
		}
	}
	raw, err := os.ReadFile(filepath.Clean(logPath))
	if err != nil {
		t.Fatal(err)
	}
	stops, starts := 0, 0
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var request launcher.Request
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatal(err)
		}
		switch request.Op {
		case launcher.OpStop:
			stops++
			if request.Agent != "writer" || request.Handle != "old-session" {
				t.Fatalf("replacement/current handle stopped: %+v", request)
			}
		case launcher.OpStart:
			starts++
			if request.Agent != "renamed" {
				t.Fatalf("wrong configured-name launch: %+v", request)
			}
		}
	}
	if joins.Load() != 0 || stops != 1 || starts != 1 {
		t.Fatalf("side effects: joins=%d stops=%d starts=%d", joins.Load(), stops, starts)
	}
}
