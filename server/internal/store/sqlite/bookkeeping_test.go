package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/board"
	"github.com/leonidas1712/aboard/server/internal/clock"
)

func bookkeepingStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "board.db"), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := s.db.ExecContext(t.Context(), "CREATE TABLE bookkeeping_proof (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	return s
}

// Holding the real writer lets requests accumulate without a timing assumption.
func holdBookkeepingWriter(t *testing.T, s *Store) func() {
	t.Helper()
	tx, err := s.writer.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback() })
	return func() {
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestQueuedBookkeepingSharesOneDurableCommit(t *testing.T) {
	s := bookkeepingStore(t)
	releaseWriter := holdBookkeepingWriter(t, s)
	entered, release := make(chan struct{}), make(chan struct{})
	var first, second *sql.Tx
	a := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		first = tx.tx
		_, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (1)")
		return err
	})
	b := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		second = tx.tx
		close(entered)
		select {
		case <-release:
		case <-t.Context().Done():
			return t.Context().Err()
		}
		_, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (2)")
		return err
	})
	releaseWriter()
	<-entered
	select {
	case <-a.done:
		t.Error("first acknowledgement returned before the shared commit")
	default:
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM bookkeeping_proof").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("bookkeeping visible before shared commit: %d rows", n)
	}
	close(release)
	<-a.done
	<-b.done
	if a.err != nil || b.err != nil {
		t.Fatalf("acknowledgements: %v, %v", a.err, b.err)
	}
	if first != second {
		t.Fatal("queued bookkeeping used separate transactions")
	}
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM bookkeeping_proof").Scan(&n); err != nil || n != 2 {
		t.Fatalf("committed bookkeeping: %d rows, %v", n, err)
	}
}

func TestBookkeepingRefusalAndCancellationLeaveOtherRequestsIntact(t *testing.T) {
	s := bookkeepingStore(t)
	releaseWriter := holdBookkeepingWriter(t, s)
	refused := errors.New("membership refused")
	a := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		if _, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (1)"); err != nil {
			return err
		}
		return refused
	})
	ctx, cancel := context.WithCancel(t.Context())
	b := s.queueBookkeeping(ctx, func(board.Tx) error {
		t.Error("cancelled request ran")
		return nil
	})
	c := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		_, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (3)")
		return err
	})
	cancel()
	releaseWriter()
	<-a.done
	<-b.done
	<-c.done
	if !errors.Is(a.err, refused) || !errors.Is(b.err, context.Canceled) || c.err != nil {
		t.Fatalf("per-request outcomes: %v, %v, %v", a.err, b.err, c.err)
	}
	var id int
	if err := s.db.QueryRowContext(t.Context(), "SELECT id FROM bookkeeping_proof").Scan(&id); err != nil || id != 3 {
		t.Fatalf("surviving request: id %d, %v", id, err)
	}
}

func TestClosingStoreCancelsQueuedBookkeeping(t *testing.T) {
	s := bookkeepingStore(t)
	release := holdBookkeepingWriter(t, s)
	r := s.queueBookkeeping(t.Context(), func(board.Tx) error {
		t.Error("request waiting for the writer ran after closing")
		return nil
	})
	closed := make(chan error, 1)
	go func() { closed <- s.Close() }()
	<-r.done
	if r.err == nil {
		t.Fatal("pending acknowledgement reported success on close")
	}
	release()
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
}

func TestCancelledBookkeepingCompletesWhileAnUnrelatedWriterIsHeld(t *testing.T) {
	s := bookkeepingStore(t)
	release := holdBookkeepingWriter(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	r := s.queueBookkeeping(ctx, func(board.Tx) error {
		t.Error("cancelled queued callback ran")
		return nil
	})
	cancel()
	<-r.done
	if !errors.Is(r.err, context.Canceled) {
		t.Fatalf("queued cancellation: %v", r.err)
	}
	// Completion must not depend on releasing the other writer first.
	release()
}

func TestRunningBookkeepingCancellationWaitsForItsCallback(t *testing.T) {
	s := bookkeepingStore(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	var result int
	r := s.queueBookkeeping(ctx, func(board.Tx) error {
		close(entered)
		<-release
		result = 1
		return nil
	})
	<-entered
	cancel()
	select {
	case <-r.done:
		t.Error("running callback could mutate its caller's result after return")
	default:
	}
	close(release)
	<-r.done
	if result != 1 || !errors.Is(r.err, context.Canceled) {
		t.Fatalf("completed callback: result %d, error %v", result, r.err)
	}
}

func TestFailedBookkeepingCommitReportsNoSuccessfulRequest(t *testing.T) {
	s := bookkeepingStore(t)
	if _, err := s.db.ExecContext(t.Context(), `CREATE TABLE bookkeeping_invalid (
		human_id TEXT REFERENCES humans(id) DEFERRABLE INITIALLY DEFERRED)`); err != nil {
		t.Fatal(err)
	}
	release := holdBookkeepingWriter(t, s)
	a := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		_, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (1)")
		return err
	})
	b := s.queueBookkeeping(t.Context(), func(b board.Tx) error {
		tx, ok := b.(*tx)
		if !ok {
			return errors.New("bookkeeping did not use the SQLite transaction")
		}
		_, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_invalid VALUES ('missing_person')")
		return err
	})
	release()
	<-a.done
	<-b.done
	if a.err == nil || b.err == nil {
		t.Fatalf("failed shared commit returned success: %v, %v", a.err, b.err)
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM bookkeeping_proof").Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed commit persisted %d rows: %v", n, err)
	}
}

func TestBookkeepingGroupsAreBoundedAndRunningCancellationRollsBack(t *testing.T) {
	s := bookkeepingStore(t)
	release := holdBookkeepingWriter(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var transactions [bookkeepingGroupSize + 1]*sql.Tx
	requests := make([]*bookkeepingRequest, len(transactions))
	for i := range requests {
		requestCtx := t.Context()
		if i == 1 {
			requestCtx = ctx
		}
		requests[i] = s.queueBookkeeping(requestCtx, func(b board.Tx) error {
			tx, ok := b.(*tx)
			if !ok {
				return errors.New("bookkeeping did not use the SQLite transaction")
			}
			transactions[i] = tx.tx
			if _, err := tx.tx.ExecContext(tx.ctx, "INSERT INTO bookkeeping_proof VALUES (?)", i); err != nil {
				return err
			}
			if i == 1 {
				cancel()
			}
			return nil
		})
	}
	release()
	for i, r := range requests {
		<-r.done
		if i == 1 {
			if !errors.Is(r.err, context.Canceled) {
				t.Fatalf("running cancellation: %v", r.err)
			}
		} else if r.err != nil {
			t.Fatalf("request %d: %v", i, r.err)
		}
	}
	for i := 1; i < bookkeepingGroupSize; i++ {
		if transactions[0] != transactions[i] {
			t.Fatalf("request %d was not grouped", i)
		}
	}
	if transactions[0] == transactions[bookkeepingGroupSize] {
		t.Fatal("group exceeded its bound")
	}
	var n int
	if err := s.db.QueryRowContext(t.Context(), "SELECT count(*) FROM bookkeeping_proof").Scan(&n); err != nil || n != bookkeepingGroupSize {
		t.Fatalf("running cancellation persisted %d rows: %v", n, err)
	}
}
