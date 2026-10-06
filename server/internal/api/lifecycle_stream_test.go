package api

import (
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func TestBoardUnavailableStreamIsIDOnlyAndKeepsSiblingHeads(t *testing.T) {
	t.Parallel()
	member := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1W"
	got := string(streamEvents(board.Update{
		Unavailable: []board.Unavailable{{BoardID: "brd_gone"}, {BoardID: "brd_other", MemberID: &member}},
		Heads:       []board.Head{{BoardID: "brd_live", Board: "readable-sibling", Seq: 7}},
	}, false))
	want := "event: board_unavailable\ndata: {\"board_id\":\"brd_gone\"}\n\n" +
		"event: board_unavailable\ndata: {\"board_id\":\"brd_other\",\"member_id\":\"" + member + "\"}\n\n" +
		"event: head\ndata: {\"board\":\"readable-sibling\",\"board_id\":\"brd_live\",\"seq\":7}\n\n"
	if got != want {
		t.Fatalf("stream events: %q, want %q", got, want)
	}
	if strings.Contains(got, "reason") || strings.Contains(got, "deleted") {
		t.Fatal("unavailability disclosed a reason")
	}
}
