package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func briefUpload(t *testing.T, c *api.ClientWithResponses, name string, p api.PutFileParams, body string, status int) *api.PutFileResponse {
	t.Helper()
	r, err := c.PutFileWithBodyWithResponse(context.Background(), name, &p, "application/octet-stream", strings.NewReader(body))
	mustStatus(t, r, err, status)
	return r
}

func briefParams(name string) api.PutFileParams {
	return api.PutFileParams{Name: name, Brief: ptr(true)}
}

func assertBriefHead(t *testing.T, s *testServer, name string, want int) {
	t.Helper()
	if got := s.head(s.owner, name); got != want {
		t.Fatalf("refusal changed board head: got %d, want %d", got, want)
	}
}

func TestBriefCreateAndReadReuseExactFileBytes(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	before, err := c.GetBoardWithResponse(context.Background(), name)
	mustStatus(t, before, err, 200)
	body := "# Work\nKeep these bytes.\n"
	f := briefUpload(t, c, name, briefParams("brief.md"), body, 201).JSON201
	if !f.Maintained {
		t.Fatal("brief must be maintained")
	}
	head := s.head(s.owner, name)
	b, err := c.GetBoardWithResponse(context.Background(), name)
	mustStatus(t, b, err, 200)
	if b.JSON200.Brief == nil || b.JSON200.Brief.FileId != f.Id || b.JSON200.Brief.Version != 1 {
		t.Fatal("board must identify its brief version")
	}
	got, err := c.GetFileVersionWithResponse(context.Background(), name, b.JSON200.Brief.FileId, fmt.Sprint(b.JSON200.Brief.Version))
	mustStatus(t, got, err, 200)
	if string(got.Body) != body {
		t.Fatal("brief download changed bytes")
	}
	after, err := c.GetBoardWithResponse(context.Background(), name)
	mustStatus(t, after, err, 200)
	if before.JSON200.ReadUpTo == nil || after.JSON200.ReadUpTo == nil || *before.JSON200.ReadUpTo != *after.JSON200.ReadUpTo {
		t.Fatal("brief reads advanced the cursor")
	}
	assertBriefHead(t, s, name, head)
}

func TestBriefUpdateRequiresFetchedIdentityAndBase(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	p := briefParams("brief.md")
	first := briefUpload(t, c, name, p, "one", 201).JSON201
	p.FileId, p.Base = &first.Id, ptr(1)
	second := briefUpload(t, c, name, p, "two", 201).JSON201
	if second.Id != first.Id || second.Latest.Version != 2 || second.Latest.Base != 1 {
		t.Fatal("update lost identity or conditional version")
	}
}

func TestBriefStaleAndMissingIdentityNeverWrite(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	first := briefUpload(t, c, name, briefParams("brief.md"), "one", 201).JSON201
	p := briefParams("brief.md")
	p.FileId, p.Base = &first.Id, ptr(1)
	briefUpload(t, c, name, p, "two", 201)
	head := s.head(s.owner, name)
	for _, tc := range []struct {
		name string
		id   *string
		base int
	}{
		{"missing identity", nil, 2}, {"stale base", &first.Id, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := briefParams("brief.md")
			q.FileId, q.Base = tc.id, &tc.base
			r := briefUpload(t, c, name, q, "refused", 409)
			if r.JSON409.Error.Code != "file_changed" {
				t.Fatal("conditional brief refusal must be file_changed")
			}
			assertBriefHead(t, s, name, head)
		})
	}
	removed, err := c.RemoveFileWithResponse(context.Background(), name, first.Id, nil)
	mustStatus(t, removed, err, 200)
	fresh := briefUpload(t, c, name, briefParams("brief.md"), "replacement", 201).JSON201
	if fresh.Id == first.Id {
		t.Fatal("recreation revived the old identity")
	}
	head = s.head(s.owner, name)
	p.Base = ptr(1)
	r := briefUpload(t, c, name, p, "old editor", 409)
	if r.JSON409.Error.Code != "file_changed" {
		t.Fatal("replaced identity must refuse")
	}
	assertBriefHead(t, s, name, head)
}

func TestBriefOtherFormatNeedsExplicitReplacement(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	briefUpload(t, c, name, briefParams("brief.md"), "markdown", 201)
	head := s.head(s.owner, name)
	r := briefUpload(t, c, name, briefParams("brief.html"), "<p>html</p>", 409)
	if r.JSON409.Error.Code != "brief_exists" {
		t.Fatal("second format must be brief_exists")
	}
	assertBriefHead(t, s, name, head)
}

func TestBriefFormatReplacementIsOneChainedTransaction(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	old := briefUpload(t, c, name, briefParams("brief.md"), "old bytes", 201).JSON201
	head := s.head(s.owner, name)
	p := briefParams("brief.html")
	p.FileId, p.Base, p.ReplaceFormat = &old.Id, ptr(1), ptr(true)
	fresh := briefUpload(t, c, name, p, "<p>new bytes</p>", 201).JSON201
	if fresh.Id == old.Id || fresh.Latest.Version != 1 || fresh.Latest.Base != 0 {
		t.Fatal("format replacement must start a new identity at version 1")
	}
	es := s.eventsAfter(s.owner, name, head)
	if len(es) != 2 || es[0]["type"] != "file.removed" || es[1]["type"] != "file.version_added" {
		t.Fatalf("replacement event order: %v", types(es))
	}
	data, ok := es[1]["data"].(map[string]any)
	if !ok {
		t.Fatal("file event has no object data")
	}
	if data["base_version"] != float64(0) || data["version"] != float64(1) {
		t.Fatal("new file event inherited the old base")
	}
	s.eventsOf(s.owner, name)
	history, err := c.GetFileVersionWithResponse(context.Background(), name, old.Id, "1")
	mustStatus(t, history, err, 200)
	if string(history.Body) != "old bytes" {
		t.Fatal("replacement removed retained history")
	}
	list, err := c.ListFilesWithResponse(context.Background(), name, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Files) != 1 || list.JSON200.Files[0].Id != fresh.Id {
		t.Fatal("replacement left more than one active brief")
	}
}

func TestBriefFailedFormatCASNeverRemovesTheCurrentBrief(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	old := briefUpload(t, c, name, briefParams("brief.md"), "one", 201).JSON201
	update := briefParams("brief.md")
	update.FileId, update.Base = &old.Id, ptr(1)
	briefUpload(t, c, name, update, "newer", 201)
	head := s.head(s.owner, name)
	p := briefParams("brief.html")
	p.FileId, p.Base, p.ReplaceFormat = &old.Id, ptr(1), ptr(true)
	r := briefUpload(t, c, name, p, "<p>stale</p>", 409)
	if r.JSON409.Error.Code != "file_changed" {
		t.Fatal("stale replacement must be file_changed")
	}
	assertBriefHead(t, s, name, head)
	b, err := c.GetBoardWithResponse(context.Background(), name)
	mustStatus(t, b, err, 200)
	if b.JSON200.Brief == nil || b.JSON200.Brief.FileId != old.Id || b.JSON200.Brief.Version != 2 {
		t.Fatal("failed replacement removed or changed current brief")
	}
}

func TestConcurrentBriefCreationHasOneWinner(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	head := s.head(s.owner, name)
	start := make(chan struct{})
	type result struct {
		response *api.PutFileResponse
		err      error
	}
	results := make(chan result, 2)
	var wg sync.WaitGroup
	for _, format := range []string{"brief.md", "brief.html"} {
		wg.Add(1)
		go func(format string) {
			defer wg.Done()
			<-start
			p := briefParams(format)
			r, err := c.PutFileWithBodyWithResponse(context.Background(), name, &p, "application/octet-stream", strings.NewReader("concurrent"))
			results <- result{r, err}
		}(format)
	}
	close(start)
	wg.Wait()
	close(results)
	success, refused := 0, 0
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		switch r.response.StatusCode() {
		case 201:
			success++
		case 409:
			refused++
		default:
			t.Fatalf("unexpected status %d", r.response.StatusCode())
		}
	}
	if success != 1 || refused != 1 {
		t.Fatalf("winners=%d refusals=%d", success, refused)
	}
	es := s.eventsAfter(s.owner, name, head)
	if len(es) != 1 || es[0]["type"] != "file.version_added" {
		t.Fatal("concurrent creation recorded more than one file")
	}
	list, err := c.ListFilesWithResponse(context.Background(), name, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Files) != 1 {
		t.Fatal("concurrent creation left more than one brief")
	}
}

func TestBriefUploadReplayRechecksArchive(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	p := briefParams("brief.md")
	p.IdempotencyKey = ptr("brief-replay")
	first := briefUpload(t, c, name, p, "same bytes", 201).JSON201
	replay := briefUpload(t, c, name, p, "same bytes", 201)
	if replay.JSON201.Id != first.Id {
		t.Fatal("replay created another brief")
	}
	archived, err := c.ArchiveBoardWithResponse(context.Background(), name, nil)
	mustStatus(t, archived, err, 200)
	head := s.head(s.owner, name)
	refused := briefUpload(t, c, name, p, "same bytes", 409)
	if refused.JSON409.Error.Code != "board_archived" {
		t.Fatal("archived replay returned a successful write receipt")
	}
	assertBriefHead(t, s, name, head)
	var b struct {
		Brief *api.BriefSummary `json:"brief"`
	}
	view, err := c.GetBoardWithResponse(context.Background(), name)
	mustStatus(t, view, err, 200)
	if err := json.Unmarshal(view.Body, &b); err != nil {
		t.Fatal(err)
	}
	if b.Brief == nil || b.Brief.FileId != first.Id {
		t.Fatal("archive made the brief unreadable")
	}
}

func TestBriefListAndJoinExposeTheSameVersion(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	f := briefUpload(t, c, name, briefParams("brief.md"), "join context", 201).JSON201
	list, err := c.ListBoardsWithResponse(context.Background(), nil)
	mustStatus(t, list, err, 200)
	found := false
	for _, b := range list.JSON200.Boards {
		if b.Name == name {
			found = b.Brief != nil && b.Brief.FileId == f.Id && b.Brief.Version == 1
		}
	}
	if !found {
		t.Fatal("board listing omitted current brief")
	}
	role := "writer"
	joined, err := c.JoinWithResponse(context.Background(), nil, api.JoinRequest{Board: &name, Role: &role})
	mustStatus(t, joined, err, 201)
	if joined.JSON201.Board.Brief == nil || joined.JSON201.Board.Brief.FileId != f.Id || joined.JSON201.Board.Brief.Version != 1 {
		t.Fatal("join summary omitted current brief")
	}
}

func TestBriefJSONDistinguishesMemberNoneFromOffBoard(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	ctx := context.Background()
	c := s.client(s.owner)
	assertField := func(t *testing.T, body []byte, want bool) {
		t.Helper()
		var row map[string]json.RawMessage
		if err := json.Unmarshal(body, &row); err != nil {
			t.Fatal(err)
		}
		raw, present := row["brief"]
		if present != want || (want && string(raw) != "null") {
			t.Fatalf("brief present=%v value=%s, want member-null=%v", present, raw, want)
		}
	}
	t.Run("member detail", func(t *testing.T) {
		got, err := c.GetBoardWithResponse(ctx, name)
		mustStatus(t, got, err, 200)
		assertField(t, got.Body, true)
	})
	t.Run("member list", func(t *testing.T) {
		got, err := c.ListBoardsWithResponse(ctx, nil)
		mustStatus(t, got, err, 200)
		var page struct {
			Boards []json.RawMessage `json:"boards"`
		}
		if err := json.Unmarshal(got.Body, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Boards) != 1 {
			t.Fatal("expected one board")
		}
		assertField(t, page.Boards[0], true)
	})
	t.Run("join", func(t *testing.T) {
		role := "writer"
		got, err := c.JoinWithResponse(ctx, nil, api.JoinRequest{Board: &name, Role: &role})
		mustStatus(t, got, err, 201)
		var out struct {
			Board json.RawMessage `json:"board"`
		}
		if err := json.Unmarshal(got.Body, &out); err != nil {
			t.Fatal(err)
		}
		assertField(t, out.Board, true)
	})
	outsider := s.client(s.addHuman("pat"))
	t.Run("offboard detail", func(t *testing.T) {
		got, err := outsider.GetBoardWithResponse(ctx, name)
		mustStatus(t, got, err, 200)
		if got.JSON200.OnBoard {
			t.Fatal("outsider unexpectedly joined")
		}
		assertField(t, got.Body, false)
	})
	t.Run("offboard list", func(t *testing.T) {
		got, err := outsider.ListBoardsWithResponse(ctx, &api.ListBoardsParams{All: ptr(true)})
		mustStatus(t, got, err, 200)
		var page struct {
			Boards []json.RawMessage `json:"boards"`
		}
		if err := json.Unmarshal(got.Body, &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Boards) != 1 {
			t.Fatal("expected one accessible open board")
		}
		assertField(t, page.Boards[0], false)
	})
}

func TestBriefUpdateResponseKeepsTheCurrentProjection(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	title := "Updated title"
	before, err := c.UpdateBoardWithResponse(context.Background(), name, nil, api.UpdateBoardRequest{Title: &title})
	mustStatus(t, before, err, 200)
	var row map[string]json.RawMessage
	if err := json.Unmarshal(before.Body, &row); err != nil {
		t.Fatal(err)
	}
	if raw, present := row["brief"]; !present || string(raw) != "null" {
		t.Fatal("update without a brief must emit null")
	}
	f := briefUpload(t, c, name, briefParams("brief.md"), "current context", 201).JSON201
	title = "Another title"
	after, err := c.UpdateBoardWithResponse(context.Background(), name, nil, api.UpdateBoardRequest{Title: &title})
	mustStatus(t, after, err, 200)
	if after.JSON200.Brief == nil || after.JSON200.Brief.FileId != f.Id || after.JSON200.Brief.Version != 1 {
		t.Fatal("title update lost existing brief projection")
	}
}
