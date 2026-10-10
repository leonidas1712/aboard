package sqlitejournal

import (
	"path/filepath"
	"sync"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestMultiIssuerConcurrentIdenticalBindingsRemainIndependent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	j := open(t, path)
	ctx := t.Context()
	key := delivery.SessionKey{Harness: "codex", ID: "session"}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, server := range []string{"https://one.example", "https://two.example"} {
		wg.Go(func() {
			_, err := j.BindGeneration(ctx, delivery.Binding{RetainSiblings: true, Session: key, Agent: delivery.AgentRef{Server: server, Board: "docs", Name: "reviewer", MemberID: "mem_same"}}, false)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	bindings, err := j.Bindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("bindings %+v %v", bindings, err)
	}
	old := bindings[1]
	replacement := bindings[0]
	replacement.RetainSiblings = true
	replacement.Agent.MemberID = "mem_new"
	if _, err := j.BindGeneration(ctx, replacement, false); err != nil {
		t.Fatal(err)
	}
	bindings, err = j.Bindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("replacement lost issuer %+v %v", bindings, err)
	}
	if bindings[1].Agent != old.Agent || bindings[1].Generation != old.Generation {
		t.Fatalf("sibling changed %+v", bindings)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := open(t, path)
	bindings, err = reopened.Bindings(ctx)
	if err != nil || len(bindings) != 2 {
		t.Fatalf("restart lost issuer %+v %v", bindings, err)
	}
}
