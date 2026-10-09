package delivery_test

import (
	"errors"
	"io"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

func TestEmptyQueueClaimNeedsNoFollowupUpdate(t *testing.T) {
	for _, lost := range []bool{false, true} {
		name := "claim received"
		if lost {
			name = "claim response lost"
		}
		t.Run(name, func(t *testing.T) {
			j, err := sqlitejournal.Open(t.Context(), filepath.Join(t.TempDir(), "delivery.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = j.Close() })
			key := delivery.SessionKey{Harness: "codex", ID: "empty-reporter"}
			if err := j.SaveSession(t.Context(), delivery.SessionRecord{Key: key, Boot: "b1", Open: true}); err != nil {
				t.Fatal(err)
			}
			binding, err := j.BindGeneration(t.Context(), delivery.Binding{Agent: reviewer, Session: key}, false)
			if err != nil {
				t.Fatal(err)
			}
			remote := &reportingServer{reports: make(chan delivery.QueueReportIntent, 4), claimKeys: make(chan string, 4), loseFirstClaim: lost, claimLost: make(chan struct{})}
			state, err := j.QueueReporter(t.Context(), reviewer, key, "b1", binding.Generation)
			if err != nil {
				t.Fatal(err)
			}
			_, err = delivery.PublishQueueIntent(t.Context(), j, remote, state, nil)
			if lost {
				if !errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("lost claim: %v", err)
				}
				state, err = j.QueueReporter(t.Context(), reviewer, key, "b1", binding.Generation)
				if err != nil || state.Pending == nil {
					t.Fatalf("claim intent not retained: %+v, %v", state, err)
				}
				_, err = delivery.PublishQueueIntent(t.Context(), j, remote, state, nil)
				first, retry := <-remote.claimKeys, <-remote.claimKeys
				if first != retry {
					t.Fatal("lost claim retried with a new key")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			state, err = j.QueueReporter(t.Context(), reviewer, key, "b1", binding.Generation)
			if err != nil || state.Pending != nil || state.Epoch != 1 || state.Revision != 0 || len(remote.reports) != 0 {
				t.Fatalf("empty claim generated a redundant update: %+v, updates=%d, err=%v", state, len(remote.reports), err)
			}
			desired := []delivery.QueuedMessage{{MessageID: "msg_waiting", Seq: 9, BoardID: "brd_docs"}}
			if _, err := delivery.PublishQueueIntent(t.Context(), j, remote, state, desired); err != nil {
				t.Fatal(err)
			}
			update := <-remote.reports
			if update.ExpectedEpoch != nil || update.Epoch != 1 || update.Revision != 1 || len(update.Messages) != 1 || update.Messages[0] != desired[0] {
				t.Fatalf("first nonempty update lost fence or identity: %+v", update)
			}
		})
	}
}
