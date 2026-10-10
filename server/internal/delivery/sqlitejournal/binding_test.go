package sqlitejournal

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestRetainedBindingsEnforceServerAndBoardIsolation(t *testing.T) {
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	session := delivery.SessionKey{Harness: "codex", ID: "session"}
	makeBinding := func(board, id string) delivery.Binding {
		return delivery.Binding{RetainSiblings: true, Agent: delivery.AgentRef{Server: "https://team.example", Board: board, Name: "writer", MemberID: id}, Session: session}
	}
	one, err := j.BindGeneration(ctx, makeBinding("one", "mem_one"), false)
	if err != nil {
		t.Fatal(err)
	}
	two, err := j.BindGeneration(ctx, makeBinding("two", "mem_two"), false)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := j.Bindings(ctx)
	if err != nil || len(bs) != 2 {
		t.Fatalf("second board evicted sibling %+v %v", bs, err)
	}
	for _, b := range bs {
		if b.RetainSiblings {
			t.Fatal("ephemeral activation was persisted")
		}
	}
	stranger := makeBinding("one", "mem_one")
	stranger.Agent.Server = "https://other.example"
	if _, err := j.BindGeneration(ctx, stranger, false); err != nil {
		t.Fatal(err)
	}
	bs, err = j.Bindings(ctx)
	if err != nil || len(bs) != 3 {
		t.Fatalf("another issuer displaced bindings %+v %v", bs, err)
	}
	replacement := makeBinding("one", "mem_replacement")
	if _, err := j.BindGeneration(ctx, replacement, false); err != nil {
		t.Fatal(err)
	}
	bs, err = j.Bindings(ctx)
	if err != nil || len(bs) != 3 {
		t.Fatalf("same-board replacement lost other board %+v %v", bs, err)
	}
	for _, b := range bs {
		if b.Agent.Server == one.Agent.Server && b.Agent.MemberID == one.Agent.MemberID {
			t.Fatal("same-board original remained bound")
		}
		if b.Agent.MemberID == two.Agent.MemberID && b.Generation != two.Generation {
			t.Fatal("other board generation changed")
		}
	}
	restored, err := j.BindGeneration(ctx, makeBinding("one", "mem_one"), false)
	if err != nil || restored.Generation <= one.Generation {
		t.Fatalf("evicted generation reused %+v %v", restored, err)
	}
}

func TestDefaultBindingStillReplacesAllSiblings(t *testing.T) {
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	s := delivery.SessionKey{Harness: "codex", ID: "session"}
	for _, board := range []string{"one", "two"} {
		if _, err := j.BindGeneration(ctx, delivery.Binding{RetainSiblings: true, Agent: delivery.AgentRef{Server: "https://team.example", Board: board, Name: "writer", MemberID: "mem_" + board}, Session: s}, false); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Bind(ctx, delivery.Binding{RetainSiblings: true, Agent: delivery.AgentRef{Server: "https://other.example", Board: "three", Name: "writer", MemberID: "mem_three"}, Session: s}); err != nil {
		t.Fatal(err)
	}
	bs, err := j.Bindings(ctx)
	if err != nil || len(bs) != 1 || bs[0].Agent.MemberID != "mem_three" {
		t.Fatalf("legacy bind retained siblings %+v %v", bs, err)
	}
	if _, err := j.BindGeneration(ctx, delivery.Binding{Agent: delivery.AgentRef{Server: "https://team.example", Board: "four", Name: "writer", MemberID: "mem_four"}, Session: s}, false); err != nil {
		t.Fatal(err)
	}
	bs, err = j.Bindings(ctx)
	if err != nil || len(bs) != 1 || bs[0].Agent.MemberID != "mem_four" {
		t.Fatalf("default generation bind retained siblings %+v %v", bs, err)
	}
}

func TestConcurrentRetainedBindingsKeepTwoServers(t *testing.T) {
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, server := range []string{"https://one.example", "https://two.example"} {
		go func() {
			<-start
			_, err := j.BindGeneration(ctx, delivery.Binding{RetainSiblings: true, Agent: delivery.AgentRef{Server: server, Board: "one", Name: "writer", MemberID: "mem_one"}, Session: delivery.SessionKey{Harness: "codex", ID: "session"}}, false)
			results <- err
		}()
	}
	close(start)
	successes := 0
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	bs, err := j.Bindings(ctx)
	if err != nil || successes != 2 || len(bs) != 2 {
		t.Fatalf("concurrent server selection successes=%d bindings=%+v error=%v", successes, bs, err)
	}
}

func TestLegacyBindingsNeverShareDurableGeneration(t *testing.T) {
	ctx := context.Background()
	j := open(t, filepath.Join(t.TempDir(), "journal.db"))
	for _, board := range []string{"one", "two"} {
		b, err := j.BindGeneration(ctx, delivery.Binding{Agent: delivery.AgentRef{Server: "https://team.example", Board: board, Name: "writer"}, Session: delivery.SessionKey{Harness: "codex", ID: board}}, true)
		if err != nil || b.Generation != 0 {
			t.Fatalf("unverified generation %+v %v", b, err)
		}
	}
	var count int
	if err := j.db.QueryRowContext(ctx, `SELECT count(*) FROM seat_generations`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("unverified seats shared durable counter %d %v", count, err)
	}
}
