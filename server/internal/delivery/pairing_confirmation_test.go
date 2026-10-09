package delivery_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/sqlitejournal"
)

func TestPairingConfirmationSurvivesAckBeforeProofRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "delivery.db")
	j, err := sqlitejournal.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	session := delivery.SessionKey{Harness: "codex", ID: "selected"}
	agent := delivery.AgentRef{Server: "https://team.example", Board: "work", Name: "reviewer", MemberID: "mem_own"}
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: session, Boot: "boot", Open: true, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	binding, err := j.BindGeneration(ctx, delivery.Binding{Agent: agent, Session: session, BoundAt: now}, false)
	if err != nil {
		t.Fatal(err)
	}
	manifest := delivery.HandoffManifest{ID: "hnd_0123456789abcdef0123456789abcdef", Session: session, Boot: "boot", Class: delivery.ClassMixed, PayloadHash: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", CreatedAt: now, Parts: []delivery.HandoffPart{{Agent: agent, Generation: binding.Generation, Seqs: []int{8}}}}
	saved, err := j.PrepareHandoff(ctx, manifest)
	if err != nil {
		t.Fatal(err)
	}
	confirmed, err := j.ConfirmHandoff(ctx, saved.ID, session, "boot", []delivery.AgentKey{agent.Key()}, now.Add(time.Second))
	if err != nil || len(confirmed) != 1 {
		t.Fatalf("confirmation: %v %v", confirmed, err)
	}
	done := confirmed[0]
	done.State = delivery.StateDone
	done.UpdatedAt = now.Add(2 * time.Second)
	if err := j.UpdateDelivery(ctx, done); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j, err = sqlitejournal.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := delivery.PairingConfirmedRows(ctx, j, session, agent)
	if err != nil || len(proof) != 1 || proof[0].HandoffID != saved.ID || len(proof[0].Seqs) != 1 || proof[0].Seqs[0] != 8 || proof[0].State != delivery.StateConfirmed {
		t.Fatalf("ack erased confirmed pairing evidence: %v %v", proof, err)
	}

	// Queue admission and somebody else's cursor acknowledgment are not confirmation.
	unconfirmed := manifest
	unconfirmed.ID = "hnd_abcdef0123456789abcdef0123456789"
	unconfirmed.Parts[0].Seqs = []int{9}
	queued, err := j.PrepareHandoff(ctx, unconfirmed)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := j.Deliveries(ctx, delivery.StateHanded)
	if err != nil || len(rows) != 1 {
		t.Fatalf("queue row: %v %v", rows, err)
	}
	skippedProof := rows[0]
	skippedProof.State = delivery.StateDone
	skippedProof.AcceptedAt = now.Add(time.Second)
	skippedProof.UpdatedAt = now.Add(2 * time.Second)
	if err := j.UpdateDelivery(ctx, skippedProof); err != nil {
		t.Fatal(err)
	}
	proof, err = delivery.PairingConfirmedRows(ctx, j, session, agent)
	if err != nil || len(proof) != 1 || proof[0].HandoffID == queued.ID {
		t.Fatalf("unconfirmed acknowledged admission became proof: %v %v", proof, err)
	}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: session, Boot: "new-boot", Open: true, UpdatedAt: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	proof, err = delivery.PairingConfirmedRows(ctx, j, session, agent)
	if err != nil || len(proof) != 0 {
		t.Fatalf("new harness boot reused old proof: %v %v", proof, err)
	}
	if err := j.SaveSession(ctx, delivery.SessionRecord{Key: session, Boot: "boot", Open: true, UpdatedAt: now.Add(3 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := j.BindGeneration(ctx, delivery.Binding{Agent: agent, Session: session, BoundAt: now.Add(3 * time.Second)}, true); err != nil {
		t.Fatal(err)
	}
	proof, err = delivery.PairingConfirmedRows(ctx, j, session, agent)
	if err != nil || len(proof) != 0 {
		t.Fatalf("replacement reused old proof: %v %v", proof, err)
	}
}
