package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestArchivedBoardListsKeepCountsAndCapabilitiesWithinReadableScope(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	maya := s.addHuman("maya")
	name := s.privateBoard(maya)
	requireLifecycleCall(t, s.lifecycleCall(maya, "POST", "/v1/boards/"+name+"/archive", "", nil), 200, "")
	archived := api.ListBoardsParamsLifecycleArchived
	for _, test := range []struct {
		name, token string
		count       int
		visible     bool
	}{
		{name: "creator", token: maya, count: 1, visible: true},
		{name: "outside admin", token: s.owner, count: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			listed, err := s.client(test.token).ListBoardsWithResponse(context.Background(), &api.ListBoardsParams{All: ptr(true), Lifecycle: &archived})
			mustStatus(t, listed, err, 200)
			out := listed.JSON200
			if out.ArchivedCount == nil || *out.ArchivedCount != test.count {
				t.Fatalf("archived_count=%v, want %d readable archives", out.ArchivedCount, test.count)
			}
			if test.visible {
				if len(out.Boards) != 1 || out.Boards[0].Lifecycle == nil || string(*out.Boards[0].Lifecycle) != "archived" {
					t.Fatal("creator did not get the readable archive")
				}
				b := out.Boards[0]
				if b.CanArchive == nil || *b.CanArchive || b.CanRestore == nil || !*b.CanRestore || b.CanDelete == nil || !*b.CanDelete {
					t.Fatal("creator capabilities do not reflect the archived lifecycle")
				}
				return
			}
			if len(out.Boards) != 0 || out.HiddenBoards == nil || len(*out.HiddenBoards) != 1 {
				t.Fatal("outside admin's archive is not limited to hidden metadata")
			}
			hidden := (*out.HiddenBoards)[0]
			if hidden.Lifecycle == nil || string(*hidden.Lifecycle) != "archived" || hidden.CanRestore == nil || !*hidden.CanRestore || hidden.CanDelete == nil || !*hidden.CanDelete || hidden.CanArchive == nil || *hidden.CanArchive {
				t.Fatal("hidden archive capabilities do not reflect admin housekeeping authority")
			}
		})
	}
}
