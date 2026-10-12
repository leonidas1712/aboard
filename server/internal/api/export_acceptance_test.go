package api_test

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func init() {
	openapi3filter.RegisterBodyDecoder("text/markdown", openapi3filter.FileBodyDecoder)
	openapi3filter.RegisterBodyDecoder("application/zip", openapi3filter.FileBodyDecoder)
}

func TestBoardExportKeepsVisibleThreadsAndUnchangedFiles(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	th := newThread(t, s)
	c := s.client(s.owner)
	ctx := context.Background()
	f, err := c.PutFileWithBodyWithResponse(ctx, th.board, &api.PutFileParams{Name: "notes/data.txt"}, "application/octet-stream", strings.NewReader("unchanged bytes\n"))
	mustStatus(t, f, err, 201)
	md := api.ExportBoardParamsFormat("md")
	doc, err := s.client(th.reviewer).ExportBoardWithResponse(ctx, th.board, &api.ExportBoardParams{Format: &md})
	mustStatus(t, doc, err, 200)
	text := string(doc.Body)
	for _, want := range []string{"Can you review the draft?", "Yes, after lunch.", "Thanks.", "notes/data.txt"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export misses %q: %s", want, text)
		}
	}
	if strings.Contains(text, "I'll look too.") {
		t.Fatal("export leaked an addressed reply")
	}
	if doc.HTTPResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("private export must not be cached")
	}
	format := api.ExportBoardParamsFormat("zip")
	archive, err := c.ExportBoardWithResponse(ctx, th.board, &api.ExportBoardParams{Format: &format})
	mustStatus(t, archive, err, 200)
	z, err := zip.NewReader(bytes.NewReader(archive.Body), int64(len(archive.Body)))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]string{}
	for _, entry := range z.File {
		r, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		_ = r.Close()
		if err != nil {
			t.Fatal(err)
		}
		found[entry.Name] = string(b)
	}
	for _, name := range []string{"board.md", "tasks.md", "timeline.md", "files/notes/data.txt"} {
		if _, ok := found[name]; !ok {
			t.Fatalf("zip missing %s", name)
		}
	}
	if found["files/notes/data.txt"] != "unchanged bytes\n" {
		t.Fatal("export changed uploaded bytes")
	}
}

func TestBoardExportUsesGuestConfinementAndNeverAcknowledges(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, writer, _ := s.pair("starter")
	code := s.guestCode(s.owner, name, "kim")
	mustStatus(t, code, nil, 201)
	joined := s.guestJoin(*code.JSON201.Code)
	mustStatus(t, joined, nil, 201)
	hidden := "private-export"
	visibility := api.BoardVisibilityPrivate
	created, err := s.client(s.owner).CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: &hidden, Visibility: &visibility})
	mustStatus(t, created, err, 201)
	for _, token := range []string{joined.JSON201.Token, joined.JSON201.Key.Token} {
		denied, err := s.client(token).ExportBoardWithResponse(ctx, hidden, nil)
		mustStatus(t, denied, err, 404)
		if denied.JSON404.Error.Code != "board_not_found" {
			t.Fatalf("hidden export: %s", denied.Body)
		}
		own, err := s.client(token).ExportBoardWithResponse(ctx, name, nil)
		mustStatus(t, own, err, 200)
	}
	before, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, before, err, 200)
	head := s.head(s.owner, name)
	for _, token := range []string{s.owner, s.browserToken(s.owner), writer} {
		doc, err := s.client(token).ExportBoardWithResponse(ctx, name, nil)
		mustStatus(t, doc, err, 200)
	}
	after, err := s.client(s.owner).GetBoardWithResponse(ctx, name)
	mustStatus(t, after, err, 200)
	if before.JSON200.ReadUpTo == nil || after.JSON200.ReadUpTo == nil || *before.JSON200.ReadUpTo != *after.JSON200.ReadUpTo {
		t.Fatal("export advanced read cursor")
	}
	assertBriefHead(t, s, name, head)
	removed, err := s.client(s.owner).RemoveAgentWithResponse(ctx, name, "writer", nil)
	mustStatus(t, removed, err, 200)
	denied, err := s.client(writer).ExportBoardWithResponse(ctx, name, nil)
	mustStatus(t, denied, err, 403)
	if denied.JSON403.Error.Code != "agent_removed" {
		t.Fatalf("removed export: %s", denied.Body)
	}
}
