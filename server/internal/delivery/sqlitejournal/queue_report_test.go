package sqlitejournal

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestQueueReporterRetainsIntentAndRejectsRotatedBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "journal.db")
	j := open(t, path)
	key := delivery.SessionKey{Harness: "codex", ID: "reporter"}
	if err := j.SaveSession(t.Context(), delivery.SessionRecord{Key: key, Boot: "b1", Open: true}); err != nil {
		t.Fatal(err)
	}
	ref := delivery.AgentRef{Server: "https://team.example", Board: "docs", Name: "writer", MemberID: "mem_writer"}
	b, err := j.BindGeneration(t.Context(), delivery.Binding{Agent: ref, Session: key}, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := j.QueueReporter(t.Context(), ref, key, "b1", b.Generation)
	if err != nil {
		t.Fatal(err)
	}
	expected := int64(7)
	state.Pending = &delivery.QueueReportIntent{Session: key.String(), Boot: "b1", IdempotencyKey: "immutable-key", ExpectedEpoch: &expected}
	if err := j.SaveQueueReporter(t.Context(), state); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = open(t, path)
	got, err := j.QueueReporter(t.Context(), ref, key, "b1", b.Generation)
	if err != nil || !reflect.DeepEqual(got, state) {
		t.Fatalf("lost pending intent: %+v %v", got, err)
	}
	next, err := j.BindGeneration(t.Context(), delivery.Binding{Agent: ref, Session: key}, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.SaveQueueReporter(t.Context(), state); err == nil {
		t.Fatal("old generation rewrote reporter")
	}
	fresh, err := j.QueueReporter(t.Context(), ref, key, "b1", next.Generation)
	if err != nil || fresh.Pending != nil || fresh.Epoch != 0 {
		t.Fatalf("new generation inherited lease: %+v %v", fresh, err)
	}
}
