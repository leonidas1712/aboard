//go:build live

package live

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// A person changes the server-held mode through HTTP, without telling the local
// daemon. The native session follows off, then all releases the message that waited.
func TestServerHeldModeStopsAndRestartsNativeDelivery(t *testing.T) {
	eachHarness(t, "ServerHeldModeStopsAndRestartsNativeDelivery", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		l := newLab(t)
		d.setUp(l)
		board := l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")
		writer.submit(`When @reviewer posts an Aboard message containing MODE-4217, run aboard say --to @reviewer "MODE-ACK". For now reply only OK and end your turn; wait for that message.`)
		writer.waitIdle(2 * time.Minute)

		off := l.setServerDeliveryMode(board, "writer", "off")
		l.waitFor(30*time.Second, "the daemon to report server-held off", func() bool {
			return l.reportedDeliveryMode(board, "writer") == "off"
		})
		note := l.say("reviewer", "--to", "@writer", "FYI: the release tag is MODE-4217.")
		l.neverWithin(quietWindow, "off mode handed the message to the native session", func() bool {
			return len(l.handedAfter(note.At)) != 0
		})
		if !slices.ContainsFunc(l.writerInbox(), func(m message) bool { return m.Seq == note.Seq }) {
			t.Fatalf("off mode lost unread message #%d", note.Seq)
		}

		all := l.setServerDeliveryMode(board, "writer", "all")
		if all <= off {
			t.Fatalf("mode revision did not advance: off %d, all %d", off, all)
		}
		ack := l.waitMessage("writer", note.At, "MODE-ACK", 3*time.Minute)
		t.Logf("measured: native session answered the held message %s after posting", ack.At.Sub(note.At))
		writer.waitIdle(2 * time.Minute)
		l.waitFor(30*time.Second, "the daemon to report server-held all", func() bool {
			return l.reportedDeliveryMode(board, "writer") == "all"
		})
	})
}

// ownerModeRequest uses only the public API and the lab's isolated person login.
func (l *lab) ownerModeRequest(method, path, body string, out any) {
	l.t.Helper()
	token, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		l.t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(l.t.Context(), method, "http://"+l.addr+path, strings.NewReader(body))
	if err != nil {
		l.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		l.t.Fatalf("%s %s: %s", method, path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		l.t.Fatal(err)
	}
}

func (l *lab) setServerDeliveryMode(board, agent, mode string) int {
	l.t.Helper()
	var out struct {
		Mode     string `json:"mode"`
		Revision int    `json:"revision"`
		Changed  bool   `json:"changed"`
	}
	l.ownerModeRequest(http.MethodPut, "/v1/boards/"+board+"/members/"+agent+"/delivery", `{"mode":"`+mode+`"}`, &out)
	if out.Mode != mode || !out.Changed || out.Revision <= 0 {
		l.t.Fatalf("server mode change: %+v; want changed %s with a revision", out, mode)
	}
	return out.Revision
}

func (l *lab) reportedDeliveryMode(board, agent string) string {
	l.t.Helper()
	var out struct {
		Members []struct {
			Name     string `json:"name"`
			Delivery string `json:"delivery"`
		} `json:"members"`
	}
	l.ownerModeRequest(http.MethodGet, "/v1/boards/"+board+"/members", "", &out)
	for _, m := range out.Members {
		if m.Name == agent {
			return m.Delivery
		}
	}
	return ""
}
