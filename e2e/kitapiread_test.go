//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// The server is the authority on what an agent has read. These checks acknowledge the
// agent's inbox the way any client may, through the HTTP API with the agent's token,
// never through aboard or this machine's control socket, and check the delivery daemon
// follows: nothing read is handed, queued, confirmed or named in a notice again.

// agentToken returns this machine's token for an agent, from its credentials file.
func (e *env) agentToken(board, name string) string {
	e.t.Helper()
	raw, err := os.ReadFile(filepath.Join(e.configDir(), "credentials.json"))
	if err != nil {
		e.t.Fatal(err)
	}
	var creds struct {
		Agents []struct{ Board, Name, Token string } `json:"agents"`
	}
	if err := json.Unmarshal(raw, &creds); err != nil {
		e.t.Fatal(err)
	}
	for _, a := range creds.Agents {
		if a.Board == board && a.Name == name {
			return a.Token
		}
	}
	e.t.Fatalf("no credentials for %s on %s", name, board)
	return ""
}

// apiReadInbox reads an agent's inbox and acknowledges all of it straight through the
// HTTP API, as an SDK, a bot or a raw HTTP client would, and returns what it read.
func (e *env) apiReadInbox(board, name string) map[int]string {
	e.t.Helper()
	token := e.agentToken(board, name)
	call := func(method, path string, body any) map[string]any {
		var payload bytes.Buffer
		if body != nil {
			_ = json.NewEncoder(&payload).Encode(body)
		}
		req, err := http.NewRequest(method, "http://"+e.addr+path, &payload)
		if err != nil {
			e.t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			e.t.Fatal(err)
		}
		defer resp.Body.Close()
		var v map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || resp.StatusCode != http.StatusOK {
			e.t.Fatalf("%s %s: status %d, %v %v", method, path, resp.StatusCode, err, v)
		}
		return v
	}
	read := map[int]string{}
	last := 0
	for _, m := range call("GET", "/v1/me/inbox", nil)["messages"].([]any) {
		msg := m.(map[string]any)
		seq := int(msg["seq"].(float64))
		read[seq], last = msg["body"].(string), max(last, seq)
	}
	if last > 0 {
		call("POST", "/v1/me/inbox/ack", map[string]any{"up_to": last})
	}
	return read
}

// kitAckedThroughTheAPI checks that messages an agent's inbox acknowledged through the
// HTTP API reach its session no more: mid-turn, not at a tool boundary, in a notice or
// when the turn ends; and while idle, a bundle handed and not yet confirmed (or, for a
// harness whose queue confirms as it takes, a message waiting for a closed session) is
// never handed again.
func kitAckedThroughTheAPI(t *testing.T, p support.Profile) {
	kitDelivers(t, p)
	if _, ok := p.Hook("prompt"); !ok && !p.Has("extension") {
		t.Skip("no prompt hook: the daemon can't tell a turn runs")
	}
	e := kitEnv(t, p)
	s := e.kitStart(p, kitID(), "startup", nil)
	kitPair(t, e, s)

	// Mid-turn.
	if r := s.op("prompt", `"prompt":"a long task"`); r.code != 0 {
		t.Fatalf("prompt hook failed\n%s", r)
	}
	e.postAsOwnerTo("writer-reviewer", "@reviewer", "owner: read through the API")
	writerSays(t, e, "peer: read through the API")
	read := e.apiReadInbox("writer-reviewer", "reviewer")
	// What a queue took before the API read it was received already; the rest must never
	// reach the session.
	queuedBefore := s.queuedText()
	var mustNotArrive []string
	for _, body := range read {
		if !strings.Contains(queuedBefore, body) {
			mustNotArrive = append(mustNotArrive, body)
		}
	}
	if !strings.Contains(strings.Join(mustNotArrive, "\n"), "owner: read through the API") {
		t.Fatalf("the API read %v; want the owner's message, which waits for a tool boundary", read)
	}
	if text := s.toolContext(t); text != "" {
		t.Fatalf("a tool boundary added messages read through the API:\n%s", text)
	}
	w := s.idle()
	writerSays(t, e, "after the turn")
	got := kitNext(t, s, w, "after the turn")
	for _, body := range mustNotArrive {
		if strings.Contains(strings.TrimPrefix(s.queuedText(), queuedBefore)+got, body) {
			t.Fatalf("%q, read through the API, reached the session again:\n%s%s", body, s.queuedText(), got)
		}
	}

	// Idle, with a bundle waiting for confirmation, or a message for a closed session.
	w = s.idle()
	switch {
	case s.ext != nil:
		writerSays(t, e, "idle: read through the API")
		s.ext.deliver(10*time.Second, false)
		_ = s.ext.conn.Close()
		e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
	case p.WaitsForIdle():
		writerSays(t, e, "idle: read through the API")
		if r := w.wait(10 * time.Second); r.code != 2 {
			t.Fatalf("the waiting hook should exit 2 with the bundle\n%s", r)
		}
		s.op("end", "")
	default:
		s.op("end", "")
		e.presenceIs("writer-reviewer", "reviewer", "no_session", "")
		writerSays(t, e, "idle: read through the API")
	}
	if read := e.apiReadInbox("writer-reviewer", "reviewer"); len(read) != 1 {
		t.Fatalf("the API read %v; want the one message sent while idle", read)
	}
	next := e.kitStart(p, kitID(), "startup", nil)
	next.seen = s.seen
	next.run("resume", "reviewer")
	nw := next.idle()
	writerSays(t, e, "after the idle read")
	if got := kitNext(t, next, nw, "after the idle read"); strings.Contains(got+next.queuedText(), "idle: read through the API") {
		t.Fatalf("a message read through the API while idle was handed again:\n%s%s", got, next.queuedText())
	}
}

// kitNext waits until the session is given a message whose body contains want, from its
// waiting hook w or extension (one bundle), or from its queue, and returns that bundle.
func kitNext(t *testing.T, s *kitSession, w *proc, want string) string {
	t.Helper()
	if s.ext != nil || s.p.WaitsForIdle() {
		return s.nextBundle(w, 10*time.Second)
	}
	eventually(t, 10*time.Second, "the queue to take "+want, func() bool { return strings.Contains(s.queuedText(), want) })
	return ""
}
