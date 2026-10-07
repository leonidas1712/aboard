package api_test

import (
	"context"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestFilesKeepConditionalVersionsAndExactBytes(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	params := api.PutFileParams{Name: "notes/storage.txt"}
	first, err := c.PutFileWithBodyWithResponse(ctx, name, &params, "application/octet-stream", strings.NewReader("first version\n"))
	mustStatus(t, first, err, 201)
	if first.JSON201.Latest.Version != 1 {
		t.Fatal("first version must be 1")
	}
	blind, err := c.PutFileWithBodyWithResponse(ctx, name, &params, "application/octet-stream", strings.NewReader("blind overwrite"))
	mustStatus(t, blind, err, 409)
	if blind.JSON409.Error.Code != "file_exists" {
		t.Fatal("blind write must be file_exists")
	}
	base := 1
	params.Base = &base
	second, err := c.PutFileWithBodyWithResponse(ctx, name, &params, "application/octet-stream", strings.NewReader("second version\n"))
	mustStatus(t, second, err, 201)
	if second.JSON201.Id != first.JSON201.Id || second.JSON201.Latest.Version != 2 {
		t.Fatal("version lost file identity")
	}
	stale, err := c.PutFileWithBodyWithResponse(ctx, name, &params, "application/octet-stream", strings.NewReader("stale overwrite"))
	mustStatus(t, stale, err, 409)
	if stale.JSON409.Error.Code != "file_changed" {
		t.Fatal("stale write must be file_changed")
	}
	list, err := c.ListFilesWithResponse(ctx, name, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Files) != 1 || list.JSON200.Files[0].Latest.Version != 2 {
		t.Fatal("list must show latest version")
	}
	for version, want := range map[string]string{"1": "first version\n", "2": "second version\n", "latest": "second version\n"} {
		got, err := c.GetFileVersionWithResponse(ctx, name, first.JSON201.Id, version)
		mustStatus(t, got, err, 200)
		if string(got.Body) != want {
			t.Fatalf("version %s bytes changed", version)
		}
	}
	detail, err := c.GetFileWithResponse(ctx, name, first.JSON201.Id)
	mustStatus(t, detail, err, 200)
	if len(detail.JSON200.Versions) != 2 {
		t.Fatal("history must retain both versions")
	}
}

func TestFilesRejectSecretsTraversalAndForeignBoardReads(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	for _, prefix := range []string{"abh_", "abi_", "abc_"} {
		for _, media := range []string{"text/plain", "application/octet-stream", "application/json"} {
			secret, err := c.PutFileWithBodyWithResponse(ctx, name, &api.PutFileParams{Name: "secret.txt", MediaType: &media}, "application/octet-stream", strings.NewReader(prefix+strings.Repeat("X", 40)))
			mustStatus(t, secret, err, 422)
			if secret.JSON422.Error.Code != "file_has_secret" {
				t.Fatal("credential was not refused")
			}
		}
	}

	reserved, err := c.PutFileWithBodyWithResponse(ctx, name, &api.PutFileParams{Name: "brief.md"}, "application/octet-stream", strings.NewReader("brief"))
	mustStatus(t, reserved, err, 409)
	file, err := c.PutFileWithBodyWithResponse(ctx, name, &api.PutFileParams{Name: "safe.txt"}, "application/octet-stream", strings.NewReader("safe"))
	mustStatus(t, file, err, 201)
	other, err := c.CreateBoardWithResponse(ctx, nil, api.CreateBoardRequest{Name: func() *string { v := "other-files"; return &v }()})
	mustStatus(t, other, err, 201)
	foreign, err := c.GetFileWithResponse(ctx, "other-files", file.JSON201.Id)
	mustStatus(t, foreign, err, 404)
	bytes, err := c.GetFileVersionWithResponse(ctx, "other-files", file.JSON201.Id, "1")
	mustStatus(t, bytes, err, 404)
	list, err := c.ListFilesWithResponse(ctx, name, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Files) != 1 {
		t.Fatal("rejected uploads created files")
	}
}

func TestFileReplayBindsNameAndBase(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	key := "file-replay"
	p := api.PutFileParams{Name: "one.txt", IdempotencyKey: &key}
	first, err := c.PutFileWithBodyWithResponse(ctx, name, &p, "application/octet-stream", strings.NewReader("same bytes"))
	mustStatus(t, first, err, 201)
	replay, err := c.PutFileWithBodyWithResponse(ctx, name, &p, "application/octet-stream", strings.NewReader("same bytes"))
	mustStatus(t, replay, err, 201)
	if replay.JSON201.Id != first.JSON201.Id {
		t.Fatal("replay created another file")
	}
	p.Name = "two.txt"
	changed, err := c.PutFileWithBodyWithResponse(ctx, name, &p, "application/octet-stream", strings.NewReader("same bytes"))
	mustStatus(t, changed, err, 422)
}

func TestFileRenameAndRemovalKeepVersionsAndFreeThePath(t *testing.T) {
	t.Parallel()
	s := newTestServer(t)
	ctx := context.Background()
	name, _, _ := s.pair("starter")
	c := s.client(s.owner)
	f, err := c.PutFileWithBodyWithResponse(ctx, name, &api.PutFileParams{Name: "old.txt"}, "application/octet-stream", strings.NewReader("kept"))
	mustStatus(t, f, err, 201)
	to := "notes/new.txt"
	moved, err := c.UpdateFileWithResponse(ctx, name, f.JSON201.Id, nil, api.UpdateFileRequest{Name: &to})
	mustStatus(t, moved, err, 200)
	if moved.JSON200.Id != f.JSON201.Id || moved.JSON200.Latest.Version != 1 { t.Fatal("rename changed file identity or version") }
	removed, err := c.RemoveFileWithResponse(ctx, name, f.JSON201.Id, nil)
	mustStatus(t, removed, err, 200)
	list, err := c.ListFilesWithResponse(ctx, name, nil)
	mustStatus(t, list, err, 200)
	if len(list.JSON200.Files) != 0 { t.Fatal("removed file is still listed") }
	history, err := c.GetFileVersionWithResponse(ctx, name, f.JSON201.Id, "1")
	mustStatus(t, history, err, 200)
	if string(history.Body) != "kept" { t.Fatal("removal lost historical bytes") }
	again, err := c.PutFileWithBodyWithResponse(ctx, name, &api.PutFileParams{Name: to}, "application/octet-stream", strings.NewReader("new identity"))
	mustStatus(t, again, err, 201)
	if again.JSON201.Id == f.JSON201.Id { t.Fatal("reused path revived old identity") }
}
