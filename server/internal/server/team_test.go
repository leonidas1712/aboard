package server

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/ids"
	"github.com/leonidas1712/aboard/server/internal/notify"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

func TestParsePublicURL(t *testing.T) {
	for in, want := range map[string]PublicURL{
		"https://team.example.com":       {Origin: "https://team.example.com", Host: "team.example.com"},
		"https://Team.Example.com":       {Origin: "https://team.example.com", Host: "team.example.com"},
		"https://team.example.com:443":   {Origin: "https://team.example.com", Host: "team.example.com"},
		"https://team.example.com:8443":  {Origin: "https://team.example.com:8443", Host: "team.example.com:8443"},
		"https://10.0.0.5:9000":          {Origin: "https://10.0.0.5:9000", Host: "10.0.0.5:9000"},
		"https://[2001:db8::1]:443":      {Origin: "https://[2001:db8::1]", Host: "[2001:db8::1]"},
		"https://[2001:db8::1]:8443":     {Origin: "https://[2001:db8::1]:8443", Host: "[2001:db8::1]:8443"},
		"https://aboard.internal.test:1": {Origin: "https://aboard.internal.test:1", Host: "aboard.internal.test:1"},
	} {
		got, err := ParsePublicURL(in)
		if err != nil || got != want {
			t.Errorf("ParsePublicURL(%q) = %+v, %v; want %+v", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "team.example.com", "http://team.example.com", "https://", "https://team.example.com/aboard",
		"https://team.example.com?x=1", "https://team.example.com#x", "https://user@team.example.com", "https://team.example.com:",
		"https://team.example.com/", "https://team.example.com//", "https://team.example.com/?", "https://team.example.com:0",
		"https://team.example.com:65536", "https://team.example.com:99999999999", "https://team.example.com:+443", "https://[::1",
		"https://team.example.com:443:443", "https:team.example.com",
	} {
		if got, err := ParsePublicURL(in); err == nil {
			t.Errorf("ParsePublicURL(%q) = %+v, want an error", in, got)
		}
	}
}

// teamServer is a team server running in this process, at a public URL that a proxy in
// front of it would serve.
type teamServer struct {
	t    *testing.T
	addr string
	data string
	log  *syncBuffer
	stop func()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

const publicURL = "https://team.example.com:8443"

// startTeam runs a team server on data until the test ends or stop is called.
func startTeam(t *testing.T, data string) *teamServer {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	pub, err := ParsePublicURL(publicURL)
	if err != nil {
		t.Fatal(err)
	}
	logs := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Options{
			Addr: addr, DataDir: data, Version: "test",
			Log:  slog.New(slog.NewJSONHandler(logs, nil)),
			Team: &Team{PublicURL: pub, AdminName: "Alex"},
		})
	}()
	s := &teamServer{t: t, addr: addr, data: data, log: logs}
	var once sync.Once
	s.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				t.Errorf("the team server stopped with %v", err)
			}
		})
	}
	t.Cleanup(s.stop)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if c, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", addr); err == nil {
			_ = c.Close()
			break
		}
		select {
		case err := <-done:
			t.Fatalf("the team server didn't start: %v\n%s", err, logs)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the team server didn't start:\n%s", logs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return s
}

// do sends a request to the server as a proxy in front of it would, with host as its
// Host header and the extra headers given.
func (s *teamServer) do(method, path, host string, headers map[string]string, body any) (got reply, decoded map[string]any) {
	s.t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			s.t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, "http://"+s.addr+path, r)
	if err != nil {
		s.t.Fatal(err)
	}
	req.Host = host
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return reply{StatusCode: resp.StatusCode, Header: resp.Header}, decoded
}

// reply is a response's status and headers, its body already read.
type reply struct {
	StatusCode int
	Header     http.Header
}

func (s *teamServer) adminKey() string {
	s.t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.data, AdminKeyFile)) //nolint:gosec // the test's own folder
	if err != nil {
		s.t.Fatal(err)
	}
	return strings.TrimSpace(string(raw))
}

func errorCode(v map[string]any) string {
	e, _ := v["error"].(map[string]any)
	code, _ := e["code"].(string)
	return code
}

// A team server answers only its public host, port included, whatever forwarded
// headers claim; says it is a team server; and names its public host in join lines.
func TestATeamServerAnswersOnlyItsPublicHost(t *testing.T) {
	s := startTeam(t, t.TempDir())
	const host = "team.example.com:8443"
	resp, info := s.do("GET", "/v1/info", host, nil, nil)
	if resp.StatusCode != http.StatusOK || info["mode"] != "team" {
		t.Fatalf("GET /v1/info at the public host: %d %v", resp.StatusCode, info)
	}
	for _, c := range []struct {
		host    string
		headers map[string]string
	}{
		{"team.example.com", nil},
		{"team.example.com:443", nil},
		{s.addr, nil},
		{"localhost:" + strings.Split(s.addr, ":")[1], nil},
		{"attacker.example", nil},
		{"attacker.example", map[string]string{"X-Forwarded-Host": host, "Forwarded": "host=" + host + ";proto=https", "X-Forwarded-Proto": "https"}},
	} {
		resp, v := s.do("GET", "/v1/info", c.host, c.headers, nil)
		if resp.StatusCode != http.StatusMisdirectedRequest || errorCode(v) != "host_not_allowed" {
			t.Errorf("Host %q %v: %d %v", c.host, c.headers, resp.StatusCode, v)
		}
		if hint, _ := v["error"].(map[string]any)["hint"].(string); !strings.Contains(hint, publicURL+"/") {
			t.Errorf("the hint doesn't name the public URL: %q", hint)
		}
	}

	key := map[string]string{"Authorization": "Bearer " + s.adminKey()}
	resp, b := s.do("POST", "/v1/boards", host, key, map[string]any{"template": "general"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create a board: %d %v", resp.StatusCode, b)
	}
	name, _ := b["name"].(string)
	resp, code := s.do("POST", "/v1/boards/"+name+"/join-codes", host, key, map[string]any{"role": "member"})
	if line, _ := code["join_line"].(string); resp.StatusCode != http.StatusCreated || !strings.Contains(line, " on "+host+" as member ") {
		t.Fatalf("the join line doesn't name the public host: %d %v", resp.StatusCode, code)
	}
}

// A browser on a team server gets the __Host- cookie, Secure, though the request reached
// the server over plain HTTP from its proxy; and a sign-in or a write needs Origin to be
// exactly the public URL, port included, whatever forwarded headers say.
func TestATeamServersBrowserCookieAndOriginComeFromItsPublicURL(t *testing.T) {
	s := startTeam(t, t.TempDir())
	const host = "team.example.com:8443"
	signIn := map[string]any{"key": s.adminKey()}
	for _, origin := range []string{"https://team.example.com", "http://team.example.com:8443", "https://team.example.com:443", "https://attacker.example", ""} {
		headers := map[string]string{"X-Forwarded-Proto": "http", "X-Forwarded-Host": "attacker.example", "X-Forwarded-For": "203.0.113.9"}
		if origin != "" {
			headers["Origin"] = origin
		}
		resp, v := s.do("POST", "/v1/browser-sessions", host, headers, signIn)
		if resp.StatusCode != http.StatusForbidden || errorCode(v) != "origin_not_allowed" {
			t.Errorf("sign-in from Origin %q: %d %v", origin, resp.StatusCode, v)
		}
	}
	resp, v := s.do("POST", "/v1/browser-sessions", host, map[string]string{"Origin": publicURL, "X-Forwarded-Proto": "http"}, signIn)
	if resp.StatusCode >= 300 {
		t.Fatalf("sign-in from the public origin: %d %v", resp.StatusCode, v)
	}
	set := resp.Header.Get("Set-Cookie")
	for _, want := range []string{"__Host-aboard_session=abb_", "Path=/", "HttpOnly", "Secure", "SameSite=Lax"} {
		if !strings.Contains(set, want) {
			t.Fatalf("the cookie lacks %q: %s", want, set)
		}
	}
	if strings.Contains(strings.ToLower(set), "domain=") {
		t.Fatalf("the cookie names a domain: %s", set)
	}
	cookie := strings.Split(set, ";")[0]
	csrf, _ := v["csrf_token"].(string)
	if csrf == "" {
		t.Fatalf("no CSRF token in the sign-in: %v", v)
	}
	write := func(origin string) (reply, map[string]any) {
		return s.do("POST", "/v1/boards", host, map[string]string{"Cookie": cookie, "X-Aboard-CSRF": csrf, "Origin": origin}, map[string]any{"template": "general"})
	}
	if resp, v := write("https://team.example.com"); resp.StatusCode != http.StatusForbidden || errorCode(v) != "origin_not_allowed" {
		t.Fatalf("a write from the public host without its port: %d %v", resp.StatusCode, v)
	}
	if resp, v := write(publicURL); resp.StatusCode != http.StatusCreated {
		t.Fatalf("a write from the public origin: %d %v", resp.StatusCode, v)
	}
}

// The first start of an empty team server makes its admin and writes their key to the
// admin key file, readable only by its owner, logging the file and never the key. A
// later start makes no other admin or key, even with the file gone.
func TestATeamServersFirstStartMakesOneAdmin(t *testing.T) {
	data := t.TempDir()
	s := startTeam(t, data)
	path := filepath.Join(data, AdminKeyFile)
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the admin key file: %v %v", info, err)
	}
	key := s.adminKey()
	if !strings.HasPrefix(key, "abh_") || strings.Contains(s.log.String(), key) {
		t.Fatalf("the key %q, or the log shows it:\n%s", key, s.log)
	}
	if !strings.Contains(s.log.String(), `"msg":"first admin created","handle":"alex","key_file":"`+path+`"`) {
		t.Fatalf("the log doesn't name the key file:\n%s", s.log)
	}
	resp, me := s.do("GET", "/v1/me", "team.example.com:8443", map[string]string{"Authorization": "Bearer " + key}, nil)
	if resp.StatusCode != http.StatusOK || me["name"] != "alex" || me["server_role"] != "admin" {
		t.Fatalf("GET /v1/me with the key: %d %v", resp.StatusCode, me)
	}
	s.stop()

	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	again := startTeam(t, data)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a second start wrote the key file again: %v", err)
	}
	if strings.Contains(again.log.String(), "first admin created") {
		t.Fatalf("a second start made an admin:\n%s", again.log)
	}
}

// Several first starts at once make one admin and one key, written once.
func TestConcurrentFirstStartsMakeOneAdmin(t *testing.T) {
	ctx := context.Background()
	data := t.TempDir()
	logs := &syncBuffer{}
	log := slog.New(slog.NewJSONHandler(logs, nil))
	const starts = 4
	svcs := make([]*board.Service, starts)
	for i := range svcs {
		st, err := sqlite.Open(ctx, filepath.Join(data, "aboard.db"), clock.Real{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close() })
		svcs[i] = board.New(st, notify.NewInProcess(), clock.Real{}, ids.New(rand.Reader), []byte("digest key"), board.Config{Mode: "team"}, log)
	}
	var wg sync.WaitGroup
	errs := make([]error, starts)
	for i, svc := range svcs {
		wg.Go(func() { errs[i] = firstAdmin(ctx, svc, data, "alex", log) })
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := strings.Count(logs.String(), "first admin created"); n != 1 {
		t.Fatalf("%d admins were made:\n%s", n, logs)
	}
	raw, err := os.ReadFile(filepath.Join(data, AdminKeyFile)) //nolint:gosec // the test's own folder
	if err != nil || strings.Count(string(raw), "abh_") != 1 {
		t.Fatalf("the key file: %q %v", raw, err)
	}
}
