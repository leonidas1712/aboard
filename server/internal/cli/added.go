package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Telling a person they were added to a board. The server marks a board added while
// someone else's add is new to its person (Board.added, read from the record); the
// commands here only say so: aboard boards, status and inbox, and once, quietly, the
// person's sessions (spec/delivery.md, "When the person is added to a board").

// addedNotice is the AddedNotice of spec/cli.yaml.
type addedNotice struct {
	Board   string  `json:"board"`
	By      string  `json:"by"`
	ByOwner *string `json:"by_owner"`
	Seq     int64   `json:"seq"`
	Join    string  `json:"join"`
}

// addedOf is b's notice, or nil when the server marks no add on it.
func addedOf(b api.Board) *addedNotice {
	if b.Added == nil {
		return nil
	}
	n := &addedNotice{Board: b.Name, By: deref(b.Added.By.Name), Seq: int64(b.Added.Seq), Join: "aboard join --board " + b.Name}
	if b.Added.By.Kind == api.ActorKindAgent {
		n.ByOwner = b.Added.By.Owner
	}
	return n
}

// by says who added them: "leo", or "leo's agent claude".
func (n addedNotice) by() string {
	if n.ByOwner != nil {
		return *n.ByOwner + "'s agent " + n.By
	}
	return n.By
}

// addedText is one line per notice, as status and inbox print them.
func addedText(ns []addedNotice) string {
	var b strings.Builder
	for _, n := range ns {
		fmt.Fprintf(&b, "Added to %s by %s · %s\n", n.Board, n.by(), n.Join)
	}
	return b.String()
}

// addedFrom collects the notices of a board list.
func addedFrom(boards []api.Board) []addedNotice {
	var out []addedNotice
	for _, b := range boards {
		if n := addedOf(b); n != nil {
			out = append(out, *n)
		}
	}
	return out
}

// personAdded reads the notices with the person's login on srv; nil when it can't.
func (a *app) personAdded(ctx context.Context, srv serverRef) []addedNotice {
	token, err := a.readOwnerToken(srv)
	if err != nil {
		return nil
	}
	c, err := a.client(ctx, srv, token, requestTimeout)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	r, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{})
	if err != nil || r.JSON200 == nil {
		return nil
	}
	return addedFrom(r.JSON200.Boards)
}

// sessionAdded reads the notices through the machine's delegation, for the session's
// person on its server, and says which server that is; nil when it can't.
func (a *app) sessionAdded(ctx context.Context, key delivery.SessionKey) (string, []addedNotice) {
	server := ""
	if agents, err := a.sessionAgents(ctx, key); err == nil && len(agents) > 0 {
		server = agents[0].Server
	}
	srv, err := a.boardServer(ctx, "", server)
	if err != nil {
		return "", nil
	}
	resp, err := a.callDaemon(ctx, delivery.Request{Op: delivery.OpBoards, Harness: key.Harness, Session: key.ID, Server: srv.URL})
	if err != nil {
		return "", nil
	}
	var boards []api.Board
	for _, raw := range resp.Boards {
		var b api.Board
		if json.Unmarshal(raw, &b) == nil {
			boards = append(boards, b)
		}
	}
	return srv.URL, addedFrom(boards)
}

// addedAround is aboard status's and aboard inbox's notices: through the delegation in
// a session, else with the person's login on srv.
func (a *app) addedAround(ctx context.Context, srv serverRef) []addedNotice {
	if key, ok := a.sessionKey(); ok {
		_, ns := a.sessionAdded(ctx, key)
		return ns
	}
	return a.personAdded(ctx, srv)
}

// addedLookEvery is how often a prompt hook looks for new adds; a session start always
// looks.
const addedLookEvery = 5 * time.Minute

// addedState is added-notices.json in the state folder: when a hook last looked, and
// each add a session on this machine was told of, as "<server> <board> <seq>".
type addedState struct {
	LookedAt time.Time `json:"looked_at"`
	Told     []string  `json:"told"`
}

// addedNote is the quiet line for a session's context about adds its person hasn't
// heard of on this machine, and marks them told. A prompt hook (always false) looks at
// most once every addedLookEvery. Any failure says nothing.
func (h hookCall) addedNote(ctx context.Context, always bool) string {
	p, err := h.a.paths()
	if err != nil {
		return ""
	}
	file := filepath.Join(p.state, "added-notices.json")
	var st addedState
	if raw, err := os.ReadFile(filepath.Clean(file)); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	now := time.Now()
	if !always && now.Sub(st.LookedAt) < addedLookEvery && !st.LookedAt.After(now) {
		return ""
	}
	server, notices := h.a.sessionAdded(ctx, delivery.SessionKey{Harness: h.harness, ID: h.in.SessionID})
	if !always {
		// Only prompts count towards a prompt's wait: a session start that just looked
		// doesn't keep the next prompt from looking.
		st.LookedAt = now
	}
	var lines []string
	for _, n := range notices {
		id := fmt.Sprintf("%s %s %d", server, n.Board, n.Seq)
		if !slices.Contains(st.Told, id) {
			st.Told = append(st.Told, id)
			lines = append(lines, fmt.Sprintf("Your person was added to %s by %s; join with %s if they ask.", n.Board, n.by(), n.Join))
		}
	}
	// The newest few hundred are plenty to remember: an add stays new only until the
	// person or their agent follows it.
	if len(st.Told) > 500 {
		st.Told = st.Told[len(st.Told)-500:]
	}
	if raw, err := json.Marshal(st); err == nil {
		_ = os.MkdirAll(p.state, 0o700)
		_ = os.WriteFile(file, raw, 0o600)
	}
	return strings.Join(lines, "\n")
}

// printAdded prints a session start's quiet line, if any, which the harness adds to the
// session's context.
func (h hookCall) printAdded(ctx context.Context) {
	if note := h.addedNote(ctx, true); note != "" {
		_, _ = fmt.Fprintln(h.a.env.Stdout, note)
	}
}
