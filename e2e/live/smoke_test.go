//go:build live

package live

import (
	"strings"
	"testing"
	"time"
)

// smokeReply is what the smoke test asks each harness's model to answer with.
const smokeReply = "SMOKE-OK"

// Each harness starts with the model the suite runs it with, in a lab like every other
// test's (scratch home and config, the login from the environment), in a folder with no
// Aboard setup, and answers one prompt. It checks a model id and a login with one turn
// per harness, before make live spends many; make live-smoke runs only this test.
func TestModelSmoke(t *testing.T) {
	for _, d := range drivers(t) {
		if !selected(d.p.Harness) {
			continue
		}
		t.Run(d.p.Harness, func(t *testing.T) {
			d.require(t)
			t.Parallel()
			l := newLab(t)
			d.setUp(l)
			// The same project set-up as the scenarios: omp, started in a folder without
			// Aboard's extension, keeps a spinner in its title and never reads as ready.
			p := d.start(l, "smoke", l.project("smoke", d.p.Harness))
			p.submit("Reply with exactly " + smokeReply)
			// The prompt shows the word once; the model's answer shows it again.
			answered := waitQuietly(3*time.Minute, func() bool {
				return strings.Count(p.scrollback(), smokeReply) >= 2 && p.idle()
			})
			if !answered {
				t.Fatalf("%s with model %s: %s didn't come back within 3 minutes. The pane shows:\n%s",
					d.p.Harness, d.model(), smokeReply, p.screen())
			}
			t.Logf("%s with model %s: %s came back", d.p.Harness, d.model(), smokeReply)
		})
	}
}
