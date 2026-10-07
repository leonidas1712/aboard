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
