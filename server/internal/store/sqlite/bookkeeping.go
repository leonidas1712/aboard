package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/leonidas1712/aboard/server/internal/board"
)

type bookkeepingRequest struct {
	ctx   context.Context
	fn    func(board.Tx) error
	done  chan struct{}
	err   error
	state atomic.Uint32
	stop  func() bool
}

const (
	bookkeepingQueued uint32 = iota
	bookkeepingRunning
	bookkeepingCancelled
	bookkeepingFinished
)

func (r *bookkeepingRequest) start() bool {
	if !r.state.CompareAndSwap(bookkeepingQueued, bookkeepingRunning) {
		return false
	}
	r.stop()
	return true
}

func (r *bookkeepingRequest) finish(err error) {
	r.err = err
	r.state.Store(bookkeepingFinished)
	close(r.done)
}

type bookkeepingWrites struct {
	mu      sync.Mutex
	pending []*bookkeepingRequest
	done    chan struct{}
	cancel  context.CancelFunc
	closed  bool
}

const bookkeepingGroupSize = 16

// WriteBookkeeping waits for the transaction containing this request to commit.
func (s *Store) WriteBookkeeping(ctx context.Context, fn func(board.Tx) error) error {
	r := s.queueBookkeeping(ctx, fn)
	<-r.done
	return r.err
}

func (s *Store) queueBookkeeping(ctx context.Context, fn func(board.Tx) error) *bookkeepingRequest {
	r := &bookkeepingRequest{ctx: ctx, fn: fn, done: make(chan struct{})}
	r.stop = context.AfterFunc(ctx, func() {
		if r.state.CompareAndSwap(bookkeepingQueued, bookkeepingCancelled) {
			r.fn = nil
			r.err = ctx.Err()
			close(r.done)
		}
	})
	q := &s.bookkeeping
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		if r.start() {
			r.finish(sql.ErrConnDone)
		}
		return r
	}
	q.pending = append(q.pending, r)
	if q.done == nil {
		workerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
		q.cancel, q.done = cancel, make(chan struct{})
		go s.flushBookkeeping(workerCtx)
	}
	return r
}

func (s *Store) flushBookkeeping(ctx context.Context) {
	q := &s.bookkeeping
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.cancel()
			close(q.done)
			q.cancel, q.done = nil, nil
			q.mu.Unlock()
			return
		}
		r := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		s.commitBookkeeping(ctx, r)
	}
}

func (s *Store) commitBookkeeping(ctx context.Context, first *bookkeepingRequest) {
	db := s.writer
	if db == nil {
		db = s.db
	}
	// The store context owns the shared transaction. One disconnected client cannot
	// roll back other clients' acknowledgements; each query still uses its caller's context.
	sqlTx, err := db.BeginTx(ctx, nil)
	if err != nil {
		if first.start() {
			first.finish(fmt.Errorf("begin bookkeeping: %w", err))
		}
		return
	}
	defer func() { _ = sqlTx.Rollback() }()
	group := []*bookkeepingRequest{first}
	q := &s.bookkeeping
	q.mu.Lock()
	n := min(len(q.pending), bookkeepingGroupSize-1)
	group = append(group, q.pending[:n]...)
	q.pending = q.pending[n:]
	q.mu.Unlock()
	err = runBookkeeping(ctx, sqlTx, group)
	if err == nil {
		err = sqlTx.Commit()
	}
	if err != nil {
		_ = sqlTx.Rollback()
	}
	for _, r := range group {
		if r.state.Load() == bookkeepingQueued && !r.start() {
			continue
		}
		if r.state.Load() != bookkeepingRunning {
			continue
		}
		if r.err == nil && err != nil {
			r.err = fmt.Errorf("commit bookkeeping: %w", err)
		}
		r.finish(r.err)
	}
}

func runBookkeeping(ctx context.Context, sqlTx *sql.Tx, group []*bookkeepingRequest) error {
	for _, r := range group {
		// Cancellation can complete a queued request while the shared writer is busy.
		// Once started, only this worker may finish it, after rollback or commit.
		if !r.start() {
			continue
		}
		if r.err = r.ctx.Err(); r.err != nil {
			continue
		}
		if _, err := sqlTx.ExecContext(ctx, "SAVEPOINT bookkeeping_request"); err != nil {
			return err
		}
		r.err = r.fn(&tx{tx: sqlTx, ctx: r.ctx})
		if r.err == nil {
			r.err = r.ctx.Err()
		}
		if r.err != nil {
			if _, err := sqlTx.ExecContext(ctx, "ROLLBACK TO bookkeeping_request"); err != nil {
				return err
			}
		}
		if _, err := sqlTx.ExecContext(ctx, "RELEASE bookkeeping_request"); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) closeBookkeeping() {
	q := &s.bookkeeping
	q.mu.Lock()
	q.closed = true
	done := q.done
	if q.cancel != nil {
		q.cancel()
	}
	q.mu.Unlock()
	if done != nil {
		<-done
	}
}
