//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// With PostToolUse deliberately absent, these messages can reach the model only through
// Stop continuation or the external queue. The queue probe distinguishes the two.
func TestCodexStopContinuesWithOneFreshBundle(t *testing.T) {
	only(t, "codex")
	setup := requireCodex(t)
	parallel(t)
	l := newLab(t)
	actualCodex, err := exec.LookPath("codex")
	if err != nil {
		t.Fatal(err)
	}
	queueLog := filepath.Join(l.dir, "queue-calls")
	if err := os.WriteFile(queueLog, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	probe := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = queue ]; then echo queued >> %s; fi\nexec %s \"$@\"\n", shellQuote(queueLog), shellQuote(actualCodex))
	probePath := filepath.Join(filepath.Dir(l.bin), "codex")
	if err := os.WriteFile(probePath, []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(probePath, 0o700); err != nil { // #nosec G302 -- Only the owner can execute the scratch queue probe.
		t.Fatal(err)
	}
	l.codexHome(setup)
	l.pairCLI()
	project := l.project("stop-continuation", "codex")
	hooksPath := filepath.Join(project, ".codex", "hooks.json")
	raw, err := os.ReadFile(filepath.Clean(hooksPath))
	if err != nil {
		t.Fatal(err)
	}
	var hooks map[string]json.RawMessage
	if err := json.Unmarshal(raw, &hooks); err != nil {
		t.Fatal(err)
	}
	var events map[string]json.RawMessage
	if err := json.Unmarshal(hooks["hooks"], &events); err != nil {
		t.Fatal(err)
	}
	delete(events, "PostToolUse")
	hooks["hooks"], err = json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(hooks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	release := filepath.Join(project, "release.fifo")
	if err := syscall.Mkfifo(release, 0o600); err != nil {
		t.Fatal(err)
	}
	fifo, err := os.OpenFile(filepath.Clean(release), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fifo.Close() }()
	gate := filepath.Join(project, "gate.sh")
	if err := os.WriteFile(gate, []byte("#!/bin/sh\n: > ready\nIFS= read -r release < release.fifo\nprintf 'gate complete\\n'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(gate, 0o700); err != nil { // #nosec G302 -- Only the owner can execute the scratch gate.
		t.Fatal(err)
	}
	p := l.startCodex("stop-writer", project)
	p.bind("writer")
	p.submit("Run ./gate.sh in the foreground and wait for completion. Then run aboard say \"STOP-DONE\". Do not run aboard inbox or read. If Aboard delivers messages afterward, follow their requests.")
	l.waitFor(2*time.Minute, "the owned gate to begin", func() bool { _, err := os.Stat(filepath.Join(project, "ready")); return err == nil })
	start := time.Now()
	old := l.say("reviewer", "--to", "@writer", "Already read before Stop; never report OLD-STOP.")
	l.run("inbox", "--as", "writer")
	first := l.say("reviewer", "--to", "@writer", "After receiving this message, run aboard say \"STOP-FIRST\".")
	second := l.say("reviewer", "--to", "@writer", "After receiving this message, run aboard say \"STOP-SECOND\".")
	if _, err := fifo.WriteString("release\n"); err != nil {
		t.Fatal(err)
	}
	done := l.waitMessage("writer", start, "STOP-DONE", 3*time.Minute)
	ack1 := l.waitMessage("writer", first.At, "STOP-FIRST", 3*time.Minute)
	ack2 := l.waitMessage("writer", second.At, "STOP-SECOND", 3*time.Minute)
	if ack1.At.Before(done.At) || ack2.At.Before(done.At) {
		t.Fatal("peer context entered before the original turn ended")
	}
	var bundles int
	for _, h := range l.handedAfter(start) {
		if slices.Contains(h.Seqs, old.Seq) {
			t.Fatal("Stop handed an already-read message")
		}
		if slices.Contains(h.Seqs, first.Seq) || slices.Contains(h.Seqs, second.Seq) {
			if h.Error != "" {
				t.Fatal("matched Stop handoff failed", h.Error)
			}
			bundles++
			if !slices.Contains(h.Seqs, first.Seq) || !slices.Contains(h.Seqs, second.Seq) {
				t.Fatal("Stop split waiting messages")
			}
		}
	}
	if bundles != 1 {
		t.Fatalf("Stop handed %d bundles; want one", bundles)
	}
	l.waitFor(30*time.Second, "both Stop messages to be acknowledged", func() bool { return len(l.writerInbox()) == 0 })
	p.waitIdle(time.Minute)
	if !l.codexHooksRan("stop-block", "stop-complete") {
		t.Fatal("the installed Stop hook did not block and then complete")
	}
	queued, err := os.ReadFile(filepath.Clean(queueLog))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(queued), "queued") {
		t.Fatal("Stop continuation used codex queue")
	}
	t.Log("Stop continued the model with both fresh messages, no already-read message and no queue call")
}
