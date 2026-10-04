package api_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

type readEvent struct {
	Board    string `json:"board"`
	BoardID  string `json:"board_id"`
	Agent    string `json:"agent"`
	ReadUpTo int    `json:"read_up_to"`
}

// readPosition returns the next block, failing unless it is a read event.
func (st *eventStream) readPosition() readEvent {
	st.t.Helper()
	b := st.next()
	data, ok := strings.CutPrefix(b, "event: read\ndata: ")
	if !ok || strings.Contains(data, "\n") {
		st.t.Fatalf("not a read event: %q", b)
	}
	var r readEvent
	if err := json.Unmarshal([]byte(data), &r); err != nil {
		st.t.Fatalf("read event data %q: %v", data, err)
	}
	return r
}

// The stream tells an agent's owner, and only its owner, when the agent's read position
// moves, whichever client acknowledged; a read position is bookkeeping and never moves
// the board's head.
func TestStreamSendsReadPositionsToTheOwnerOnly(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	boardName, writer, reviewer := s.pair("starter")
	code, err := s.client(s.owner).CreateJoinCodeWithResponse(ctx, boardName, nil, api.CreateJoinCodeRequest{Role: "reviewer"})
	mustStatus(t, code, err, 201)
	priya := s.addHuman("priya")
	j, err := s.client(priya).JoinWithResponse(ctx, nil, api.JoinRequest{Code: code.JSON201.Code})
	mustStatus(t, j, err, 201)
	priyasAgent := j.JSON201.Token

	post, err := s.client(reviewer).PostMessageWithResponse(ctx, boardName, nil, api.PostMessageRequest{Body: "for everyone"})
	mustStatus(t, post, err, 201)
	seq := post.JSON201.Seq
	alex, other := s.openStream(s.owner), s.openStream(priya)
	head := alex.head().Seq
	other.head()

	ack, err := s.client(writer).AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: seq})
	mustStatus(t, ack, err, 200)
	if r := alex.readPosition(); r.Board != boardName || r.Agent != "writer" || r.ReadUpTo != seq {
		t.Fatalf("read event for the owner: %+v", r)
	}
	// Acknowledging again moves nothing, so nothing is sent; priya hears only of her own
	// agent.
	ack, err = s.client(writer).AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: seq})
	mustStatus(t, ack, err, 200)
	ack, err = s.client(priyasAgent).AckInboxWithResponse(ctx, nil, api.AckInboxJSONRequestBody{UpTo: seq})
	mustStatus(t, ack, err, 200)
	if r := other.readPosition(); r.Agent == "writer" || r.ReadUpTo != seq {
		t.Fatalf("priya's stream: %+v", r)
	}
	b, err := s.client(s.owner).GetBoardWithResponse(ctx, boardName)
	mustStatus(t, b, err, 200)
	if int64(b.JSON200.HeadSeq) != int64(head) {
		t.Fatalf("acknowledging moved the head from %d to %d", head, b.JSON200.HeadSeq)
	}
}
