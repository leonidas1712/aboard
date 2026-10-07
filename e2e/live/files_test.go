//go:build live

package live

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAttachedFileReachesTheHarness(t *testing.T) {
	eachHarness(t, "AttachedFileReachesTheHarness", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.run("up")
		const board = "file-proof"
		var created struct {
			Name string `json:"name"`
		}
		l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards", map[string]any{"name": board, "template": "general"}, &created)
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.submit("Run `aboard join --board file-proof --name writer`. Do not create another seat or post a message. When a message with an attached file arrives, read the attached version using its file-get hint and reply on Aboard with exactly the file's contents. Reply only OK now.")
		writer.waitIdle(3 * time.Minute)
		d.afterBind(writer)
		l.waitFor(30*time.Second, "writer to join", func() bool { names := l.agents(board); return len(names) == 1 && names[0] == "writer" })
		const contents = "FILE-CONTENT-4927"
		path := filepath.Join(l.dir, "proof.txt")
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
		var uploaded struct {
			File struct {
				ID     string `json:"id"`
				Latest struct {
					Version int `json:"version"`
				} `json:"latest"`
			} `json:"file"`
		}
		l.decode(l.human, &uploaded, "file", "put", path, "--board", board)
		if uploaded.File.ID == "" || uploaded.File.Latest.Version != 1 {
			t.Fatal("upload did not create version one")
		}
		var posted struct {
			message
			ID string `json:"id"`
		}
		l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards/"+board+"/messages", map[string]any{
			"to": []string{"@writer"}, "body": "Read the attached version and reply with its entire contents. The message body does not contain the file's contents.",
			"files": []map[string]any{{"file": uploaded.File.ID, "version": 1}},
		}, &posted)
		reply := l.waitMessage("writer", posted.At, contents, 3*time.Minute)
		if reply.Body != contents {
			t.Fatalf("reply body %q, want exact attachment contents", reply.Body)
		}
		var detail struct {
			PostedIn []struct {
				MessageID string `json:"message_id"`
				Version   int    `json:"version"`
			} `json:"posted_in"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, fmt.Sprintf("/v1/boards/%s/files/%s", board, uploaded.File.ID), nil, &detail)
		if len(detail.PostedIn) != 1 || detail.PostedIn[0].MessageID != posted.ID || detail.PostedIn[0].Version != 1 {
			t.Fatal("attachment did not retain its pinned version")
		}
	})
}
