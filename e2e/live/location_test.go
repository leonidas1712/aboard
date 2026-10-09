//go:build live

package live

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentLocationMatchesItsRealHarnessSession(t *testing.T) {
	eachHarness(t, "AgentLocationMatchesItsRealHarnessSession", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		folder := l.project("agent-location", d.p.Harness)
		writer := d.start(l, "writer", folder)
		writer.bind("writer")
		type location struct {
			Harness    string    `json:"harness"`
			Folder     string    `json:"folder"`
			Session    string    `json:"session_id"`
			LastActive time.Time `json:"last_active"`
		}
		var page struct {
			Agents []struct {
				Name     string    `json:"name"`
				Location *location `json:"location"`
			} `json:"agents"`
		}
		var found *location
		l.waitFor(30*time.Second, "the real session's private location report", func() bool {
			l.multiSeatOwnerRequest(http.MethodGet, "/v1/me/agents", nil, &page)
			for _, a := range page.Agents {
				if a.Name == "writer" && a.Location != nil {
					found = a.Location
					return true
				}
			}
			return false
		})
		actualFolder, err := filepath.EvalSymlinks(found.Folder)
		if err != nil {
			t.Fatal(err)
		}
		expectedFolder, err := filepath.EvalSymlinks(folder)
		if err != nil {
			t.Fatal(err)
		}
		if actualFolder != expectedFolder || found.Harness != d.p.Harness || found.LastActive.IsZero() {
			t.Fatal("reported location does not match the real harness and working folder")
		}
		matched := false
		for _, started := range l.starts() {
			if started.Session == d.p.Harness+":"+found.Session {
				matched = true
			}
		}
		if !matched || strings.TrimSpace(found.Session) == "" {
			t.Fatal("reported conversation differs from the daemon's real session")
		}
		writer.waitIdle(2 * time.Minute)
	})
}
