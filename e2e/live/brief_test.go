//go:build live

package live

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestBriefGetEditPutFromTheHarness(t *testing.T) {
	eachHarness(t, "BriefGetEditPutFromTheHarness", func(t *testing.T, d *driver, _ *recorder) {
		l := newLab(t)
		d.setUp(l)
		l.run("up")
		const board = "brief-proof"
		var created struct {
			ID string `json:"id"`
		}
		l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards", map[string]any{"name": board, "template": "general"}, &created)
		const original = "BRIEF-INITIAL-4927\n"
		const added = "BRIEF-UPDATED-9187"
		const edited = original + added + "\n"
		source := filepath.Join(l.dir, "initial.md")
		if err := os.WriteFile(source, []byte(original), 0o600); err != nil {
			t.Fatal(err)
		}
		var uploaded struct {
			Version int `json:"version"`
		}
		l.decode(l.human, &uploaded, "brief", "put", source, "--board", board)
		if uploaded.Version != 1 {
			t.Fatal("fixture did not create brief version one")
		}
		var before struct {
			Brief *struct {
				FileID string `json:"file_id"`
			} `json:"brief"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board, nil, &before)
		if before.Brief == nil || before.Brief.FileID == "" {
			t.Fatal("created brief has no immutable file identity")
		}
		project := l.project("project", d.p.Harness)
		writer := d.start(l, "writer", project)
		writer.submit("Run `aboard join --board brief-proof --name writer`. Do not create another seat, read the brief yet, or post any message. Reply only OK now.")
		writer.waitIdle(3 * time.Minute)
		d.afterBind(writer)
		l.waitFor(30*time.Second, "writer's seat", func() bool {
			names := l.agents(board)
			return len(names) == 1 && names[0] == "writer"
		})
		seats := l.multiSeatCredentials([]string{board})
		local := filepath.Join(project, "edited-brief.md")
		if _, err := os.Stat(local); !os.IsNotExist(err) {
			t.Fatalf("edit path was not initially absent: %v", err)
		}
		writer.submit(fmt.Sprintf("Update the Aboard brief on %s using exactly this workflow: run `aboard brief get %s --board %s`; append one line containing exactly %q plus a newline to that fetched local file, keeping all existing bytes; then run `aboard brief put %s --board %s`. Do not use --base, another local path, the file command, or edit Aboard's state files. Do not post a message or update another file or board. End your turn after the put succeeds.", board, local, board, added, local, board))
		var current struct {
			Brief *struct {
				FileID  string `json:"file_id"`
				Name    string `json:"name"`
				Version int    `json:"version"`
				By      struct {
					Name string `json:"name"`
					Kind string `json:"kind"`
				} `json:"by"`
			} `json:"brief"`
		}
		l.waitFor(3*time.Minute, "the harness to put the fetched brief back as version two", func() bool {
			l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board, nil, &current)
			return current.Brief != nil && current.Brief.Version >= 2
		})
		writer.waitIdle(2 * time.Minute)
		if current.Brief.FileID != before.Brief.FileID || current.Brief.Name != "brief.md" || current.Brief.Version != 2 || current.Brief.By.Name != "writer" || current.Brief.By.Kind != "agent" {
			t.Fatalf("wrong final brief identity, version or writer: %+v", current.Brief)
		}
		localBytes, err := os.ReadFile(filepath.Clean(local))
		if err != nil || string(localBytes) != edited {
			t.Fatalf("the model's local edit did not preserve the fetched bytes: %q, %v", localBytes, err)
		}
		var detail struct {
			ID         string `json:"id"`
			Maintained bool   `json:"maintained"`
			Versions   []struct {
				Version int    `json:"version"`
				Base    int    `json:"base"`
				Digest  string `json:"digest"`
			} `json:"versions"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board+"/files/"+before.Brief.FileID, nil, &detail)
		originalDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(original)))
		editedDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(edited)))
		if detail.ID != before.Brief.FileID || !detail.Maintained || len(detail.Versions) != 2 || detail.Versions[0].Version != 1 || detail.Versions[0].Digest != originalDigest || detail.Versions[1].Version != 2 || detail.Versions[1].Base != 1 || detail.Versions[1].Digest != editedDigest {
			t.Fatalf("brief history did not retain the exact original and edited bytes: %+v", detail)
		}
		var fetched []struct {
			Server, Actor, Board, ID, Name, Path, Digest string
			Version                                      int
		}
		raw, err := os.ReadFile(filepath.Join(l.stateDir(), "files.json"))
		if err != nil || json.Unmarshal(raw, &fetched) != nil {
			t.Fatal("the successful put has no readable local provenance record")
		}
		found := false
		for _, entry := range fetched {
			if entry.Path == local {
				found = entry.Server == "http://"+l.addr && entry.Actor == seats[0].MemberID && entry.Board == created.ID && entry.ID == before.Brief.FileID && entry.Name == "brief.md" && entry.Version == 2 && entry.Digest == editedDigest
			}
		}
		if !found {
			t.Fatal("local provenance is not scoped to the writer's server, immutable board, seat and file")
		}
		var events struct {
			Events []struct {
				Type  string `json:"type"`
				Actor struct {
					MemberID string `json:"member_id"`
				} `json:"actor"`
				Data struct {
					FileID  string `json:"file_id"`
					Version int    `json:"version"`
					Base    int    `json:"base_version"`
					Digest  string `json:"digest"`
				} `json:"data"`
			} `json:"events"`
		}
		l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board+"/events?limit=200", nil, &events)
		versions, updatedEvents := 0, 0
		for _, event := range events.Events {
			if event.Type != "file.version_added" || event.Data.FileID != before.Brief.FileID {
				continue
			}
			versions++
			if event.Data.Version == 2 {
				updatedEvents++
				if event.Actor.MemberID != seats[0].MemberID || event.Data.Base != 1 || event.Data.Digest != editedDigest {
					t.Fatal("version two was not recorded by the exact seat against version one")
				}
			}
		}
		if versions != 2 || updatedEvents != 1 {
			t.Fatalf("got %d version events and %d updates, want exactly two and one", versions, updatedEvents)
		}
		proof, err := json.MarshalIndent(map[string]any{"workflow": "native brief get, local edit, brief put", "board_id": created.ID, "seat_id": seats[0].MemberID, "file_id": detail.ID, "base": 1, "version": 2, "initial_digest": originalDigest, "updated_digest": editedDigest}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if l.extra == nil {
			l.extra = map[string][]byte{}
		}
		l.extra["brief-proof.json"] = proof
	})
}
