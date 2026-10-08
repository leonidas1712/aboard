//go:build live

package live

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

func TestSameOwnerUrgentPeerReachesOneToolBoundary(t *testing.T) {
	eachHarness(t, "SameOwnerUrgentPeer", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Has("midturn-peer") {
			rec.notApplicable("the profile does not declare verified peer tool-boundary delivery")
		}
		l := newLab(t)
		d.setUp(l)
		l.pairCLI()
		l.run("delivery", "midturn", "my-agents")
		project := l.project("peer-boundary", d.p.Harness)
		path := filepath.Join(project, "release.fifo")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Fatal(err)
		}
		fifo, err := os.OpenFile(filepath.Clean(path), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = fifo.Close() }()
		script := filepath.Join(project, "gate.sh")
		body := "#!/bin/sh\nif [ -e first-done ]; then : > second-ready; else : > first-ready; fi\nIFS= read -r release < release.fifo\n: > first-done\nprintf 'gate complete\\n'\n"
		if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(script, 0o700); err != nil {
			t.Fatal(err)
		} // #nosec G302 -- Owner-only scratch gate.
		writer := d.start(l, "writer", project)
		writer.bind("writer")
		writer.submit("Run ./gate.sh in the foreground and wait. Then run it a second time and wait. Then run aboard say \"PEER-DONE\". Do not run aboard inbox or read. If Aboard delivers a message between commands, follow its request before continuing.")
		ready := func(name string) {
			l.waitFor(2*time.Minute, name, func() bool { _, err := os.Stat(filepath.Join(project, name)); return err == nil })
		}
		ready("first-ready")
		first := l.say("reviewer", "--to", "@writer", "--urgent", "Run aboard say \"PEER-STEP\" before continuing your task.")
		excess := l.say("reviewer", "--to", "@writer", "--urgent", "When delivered, run aboard say \"PEER-EXCESS\".")
		ordinary := l.say("reviewer", "--to", "@writer", "When delivered, run aboard say \"PEER-ORDINARY\".")
		if _, err := fifo.WriteString("release\n"); err != nil {
			t.Fatal(err)
		}
		step := l.waitMessage("writer", first.At, "PEER-STEP", 3*time.Minute)
		ready("second-ready")
		for _, h := range l.logged("tool boundary") {
			if slices.Contains(h.Seqs, excess.Seq) || slices.Contains(h.Seqs, ordinary.Seq) {
				t.Fatal("excess or ordinary peer entered the busy turn")
			}
		}
		reached, how, ok := l.reached(first.Seq)
		if !ok || how != "tool boundary" {
			t.Fatalf("urgent peer reached by %q at %s", how, reached)
		}
		if _, err := fifo.WriteString("release\n"); err != nil {
			t.Fatal(err)
		}
		done := l.waitMessage("writer", first.At, "PEER-DONE", 3*time.Minute)
		extra := l.waitMessage("writer", excess.At, "PEER-EXCESS", 3*time.Minute)
		normal := l.waitMessage("writer", ordinary.At, "PEER-ORDINARY", 3*time.Minute)
		if !step.At.Before(done.At) || extra.At.Before(done.At) || normal.At.Before(done.At) {
			t.Fatal("peer timing did not preserve the sender cap and turn-end boundary")
		}
		writer.waitIdle(2 * time.Minute)
		l.waitFor(30*time.Second, "peer messages acknowledged", func() bool { return len(l.writerInbox()) == 0 })
	})
}
