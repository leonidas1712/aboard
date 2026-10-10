package boardtest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
)

func agentLocationsReplaceAndRollBack(t *testing.T, st board.Store) {
	var seat board.Member
	first := board.AgentLocation{Machine: "laptop", Harness: "codex", SessionID: "codex:first", Folder: "/work/first", LastActive: at}
	write(t, st, func(tx board.Tx) error {
		b, _, err := newBoard(tx, "locations")
		if err != nil {
			return err
		}
		seat = agent(b, creatorID, "writer", "member")
		return tx.InsertMember(seat)
	})
	read(t, st, func(tx board.ReadTx) error {
		_, err := tx.AgentLocation(seat.ID)
		if !errors.Is(err, board.ErrNotFound) {
			t.Fatalf("unreported location: %v", err)
		}
		return nil
	})
	write(t, st, func(tx board.Tx) error { return tx.SetAgentLocation(seat.ID, first) })
	next := board.AgentLocation{Machine: "desktop", Harness: "claude-code", SessionID: "claude-code:second", Folder: "/work/second", LastActive: "2026-10-01T17:00:00Z"}
	rollback := errors.New("abort location update")
	err := st.Write(context.Background(), func(tx board.Tx) error {
		if err := tx.SetAgentLocation(seat.ID, next); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.AgentLocation(seat.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("uncommitted location became visible: %+v", got)
		}
		return nil
	})
	write(t, st, func(tx board.Tx) error { return tx.SetAgentLocation(seat.ID, next) })
	read(t, st, func(tx board.ReadTx) error {
		got, err := tx.AgentLocation(seat.ID)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(got, next) {
			t.Fatalf("location replacement lost fields: %+v", got)
		}
		return nil
	})
}
