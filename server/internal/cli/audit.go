package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/events"
)

// historyRewritten is the reason given when the log no longer has the event this
// machine verified last time.
const historyRewritten = "history_rewritten"

// eventsPageSize is how many events audit verify reads per request.
const eventsPageSize = 200

// auditHead is a verified position in a board's event log.
type auditHead struct {
	Seq  int64  `json:"seq"`
	Hash string `json:"hash"`
}

// pinnedHeads is the content of heads.json: server id → board id → head.
type pinnedHeads map[string]map[string]auditHead

// badEvent is the first event that failed verification.
type badEvent struct {
	Seq    int64  `json:"seq"`
	Reason string `json:"reason"`
}

type pinnedResult struct {
	Seq     int64  `json:"seq"`
	Hash    string `json:"hash"`
	Matches bool   `json:"matches"`
}

// pinCheck compares a board's events with the head this machine verified before.
type pinCheck struct {
	pinned  *auditHead
	seen    bool
	matches bool
}

// check looks for the pinned event in evs and returns its sequence number if its hash
// changed.
func (p *pinCheck) check(evs []events.Event) (int64, bool) {
	if p.pinned == nil {
		return 0, false
	}
	for i := range evs {
		if evs[i].Seq == p.pinned.Seq {
			p.seen, p.matches = true, evs[i].Hash == p.pinned.Hash
			return evs[i].Seq, !p.matches
		}
	}
	return 0, false
}

// runAudit runs "aboard audit verify", which checks a board's hash chain.
func runAudit(ctx context.Context, a *app, args []string) error {
	const use = "aboard audit verify [--as AGENT] [--board NAME] [--json]"
	fs := a.flags("audit")
	as := fs.String("as", "", "verify as this agent instead of as yourself")
	boardFlag := fs.String("board", "", "the board to verify")
	pos, err := a.parse(fs, args, use, 1, 1)
	if err != nil {
		return err
	}
	if pos[0] != "verify" {
		return usageError(fmt.Sprintf("%q is not an audit command.", pos[0]), use)
	}
	t, err := a.selectBoard(*boardFlag)
	if err != nil {
		return err
	}
	var c *client
	if *as != "" {
		cred, err := a.agentFor(*as, t)
		if err != nil {
			return err
		}
		c, err = a.client(t.server, cred.Token, requestTimeout)
		if err != nil {
			return err
		}
	} else if c, err = a.humanClient(t); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	info, err := c.info(ctx)
	if err != nil {
		return err
	}
	b, err := c.board(ctx, t.board)
	if err != nil {
		return err
	}

	p, err := a.paths()
	if err != nil {
		return err
	}
	heads := pinnedHeads{}
	if _, err := readJSONFile(p.heads(), &heads); err != nil {
		return err
	}
	pins := &pinCheck{}
	if h, ok := heads[info.ServerId][b.Id]; ok {
		pins.pinned = &h
	}

	v := events.NewVerifier()
	var firstBad *badEvent
	after := 0
	for {
		page, err := c.eventsPage(ctx, t.board, after)
		if err != nil {
			return err
		}
		if prob := v.Add(page.Events); prob != nil {
			firstBad = &badEvent{Seq: prob.Seq, Reason: prob.Reason}
		}
		if seq, changed := pins.check(page.Events); changed && (firstBad == nil || seq <= firstBad.Seq) {
			firstBad = &badEvent{Seq: seq, Reason: historyRewritten}
		}
		if firstBad != nil || page.NextAfter == nil || *page.NextAfter <= after {
			break
		}
		after = *page.NextAfter
	}
	if firstBad == nil && pins.pinned != nil && !pins.seen {
		firstBad = &badEvent{Seq: pins.pinned.Seq, Reason: historyRewritten}
	}

	var pinned *pinnedResult
	if pins.pinned != nil {
		pinned = &pinnedResult{Seq: pins.pinned.Seq, Hash: pins.pinned.Hash, Matches: pins.seen && pins.matches}
	}
	head := auditHead{Seq: v.LastSeq, Hash: v.LastHash}
	out := struct {
		Board         string        `json:"board"`
		OK            bool          `json:"ok"`
		EventsChecked int           `json:"events_checked"`
		DataWithheld  int           `json:"data_withheld"`
		Head          auditHead     `json:"head"`
		FirstBad      *badEvent     `json:"first_bad"`
		PinnedHead    *pinnedResult `json:"pinned_head"`
	}{b.Name, firstBad == nil, v.Checked, v.Withheld, head, firstBad, pinned}

	if firstBad != nil {
		a.emit(out, fmt.Sprintf("FAILED: event #%d on %s: %s\nHint: the board's history was changed after it was written.\n",
			firstBad.Seq, b.Name, firstBad.Reason))
		return errCheckFailed
	}
	latest := pinnedHeads{}
	if err := updateJSONFile(p.heads(), &latest, 0o600, func() error {
		if latest[info.ServerId] == nil {
			latest[info.ServerId] = map[string]auditHead{}
		}
		latest[info.ServerId][b.Id] = head
		return nil
	}); err != nil {
		return err
	}
	a.emit(out, fmt.Sprintf("OK: %d events on %s verified, head #%d %s\n", v.Checked, b.Name, head.Seq, shortHash(head.Hash)))
	return nil
}

// shortHash shortens "sha256:<hex>" to its first 8 hex characters and an ellipsis.
func shortHash(h string) string {
	hexPart, ok := strings.CutPrefix(h, "sha256:")
	if !ok || len(hexPart) <= 8 {
		return h
	}
	return "sha256:" + hexPart[:8] + "…"
}

// eventPage is one page of a board's event log, decoded for verification.
type eventPage struct {
	Events    []events.Event `json:"events"`
	NextAfter *int           `json:"next_after"`
}

func (c *client) eventsPage(ctx context.Context, board string, after int) (*eventPage, error) {
	limit := eventsPageSize
	params := &api.ListEventsParams{Limit: &limit}
	if after > 0 {
		params.After = &after
	}
	r, err := c.api.ListEventsWithResponse(ctx, board, params)
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.StatusCode() != http.StatusOK {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	var page eventPage
	if err := json.Unmarshal(r.Body, &page); err != nil {
		return nil, fmt.Errorf("decode events of board %s: %w", board, err)
	}
	return &page, nil
}
