// Package board is Aboard's domain logic: creating boards, joining them, posting and
// reading messages, and serving the event log. Every write follows the same path:
// authenticate, check membership and permissions, then in one transaction append the
// hash-chained event and update the read models, then wake anyone waiting. Storage and
// waking are ports (Store, Notifier) that adapters implement; this package never
// imports them.
package board

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/apierr"
	"github.com/leonidas1712/aboard/server/internal/clock"
	"github.com/leonidas1712/aboard/server/internal/events"
	"github.com/leonidas1712/aboard/server/internal/ids"
)

// Config describes the server a Service runs in.
type Config struct {
	ServerID string
	Mode     string // "local" or "team"
	JoinHost string // how join lines name this server: "localhost", "localhost:7411", a domain
}

// Service implements every board operation.
type Service struct {
	st     Store
	notify Notifier
	clk    clock.Clock
	gen    *ids.Generator
	key    []byte // keys the digests of tokens and join codes
	cfg    Config
	log    *slog.Logger
}

// New returns a Service that keeps its data in st and wakes waiting readers through
// notify. key must be the server's secret digest key.
func New(st Store, notify Notifier, clk clock.Clock, gen *ids.Generator, key []byte, cfg Config, log *slog.Logger) *Service {
	return &Service{st: st, notify: notify, clk: clk, gen: gen, key: key, cfg: cfg, log: log}
}

// Config returns the server description the Service was created with.
func (s *Service) Config() Config { return s.cfg }

// Principal is the authenticated caller: exactly one of Human and Agent is set.
type Principal struct {
	Human *Human
	Agent *Member
}

// Authenticate resolves a bearer token to a human or an agent.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	digest := ids.Digest(s.key, token)
	var p Principal
	err := s.st.Read(ctx, func(tx ReadTx) error {
		switch {
		case strings.HasPrefix(token, "abh_"):
			h, err := tx.HumanByTokenDigest(digest)
			if err != nil {
				return err
			}
			p.Human = &h
		case strings.HasPrefix(token, "aba_"):
			m, err := tx.MemberByTokenDigest(digest)
			if err != nil {
				return err
			}
			p.Agent = &m
		default:
			return ErrNotFound
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Principal{}, apierr.Unauthorized()
	}
	if err != nil {
		return Principal{}, fmt.Errorf("authenticate: %w", err)
	}
	return p, nil
}

// BootstrapOwner creates the local server's owner if no human exists yet, and returns
// the new token, or "" if a human already existed.
func (s *Service) BootstrapOwner(ctx context.Context, name string) (string, error) {
	var token string
	err := s.st.Write(ctx, func(tx Tx) error {
		n, err := tx.HumanCount()
		if err != nil || n > 0 {
			return err
		}
		now := s.clk.Now()
		id, err := s.gen.ID("hum", now)
		if err != nil {
			return err
		}
		if token, err = s.gen.Token("abh"); err != nil {
			return err
		}
		return tx.InsertHuman(Human{ID: id, Name: name, TokenDigest: ids.Digest(s.key, token), CreatedAt: stamp(now)})
	})
	if err != nil {
		return "", fmt.Errorf("create local owner: %w", err)
	}
	return token, nil
}

// stamp formats a time the way every timestamp in Aboard is written.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func ptr[T any](v T) *T { return &v }

func actorOf(m Member) events.Actor {
	return events.Actor{Kind: m.Kind, MemberID: ptr(m.ID), Name: ptr(m.Name), Owner: m.Owner}
}

// append seals and stores the next event on b, and advances b's head.
func (s *Service) append(tx Tx, b *Board, typ string, actor events.Actor, at time.Time, data any) (events.Event, error) {
	id, err := s.gen.ID("evt", at)
	if err != nil {
		return events.Event{}, err
	}
	e := events.Event{ID: id, BoardID: b.ID, Seq: b.HeadSeq + 1, Type: typ, At: stamp(at), Actor: actor, PrevHash: b.HeadHash}
	if err := e.Seal(data); err != nil {
		return events.Event{}, err
	}
	if err := tx.AppendEvent(e); err != nil {
		return events.Event{}, err
	}
	b.HeadSeq, b.HeadHash = e.Seq, e.Hash
	return e, nil
}

// access returns the board named name and the caller's membership of it. A board the
// caller can't see is reported as not found, so names don't leak.
func access(tx ReadTx, p Principal, name string) (Board, Member, error) {
	if p.Agent != nil {
		b, err := tx.BoardByID(p.Agent.BoardID)
		if err != nil {
			return Board{}, Member{}, err
		}
		if b.Name != name {
			return Board{}, Member{}, apierr.BoardNotFound(name)
		}
		me, err := tx.MemberByName(b.ID, p.Agent.Name)
		return b, me, err
	}
	b, err := tx.BoardByName(name)
	if errors.Is(err, ErrNotFound) {
		return Board{}, Member{}, apierr.BoardNotFound(name)
	}
	if err != nil {
		return Board{}, Member{}, err
	}
	me, err := tx.HumanMember(b.ID, p.Human.ID)
	if errors.Is(err, ErrNotFound) {
		return Board{}, Member{}, apierr.BoardNotFound(name)
	}
	return b, me, err
}

func requireHuman(p Principal) error {
	if p.Human == nil {
		return apierr.HumanRequired()
	}
	return nil
}

func invalid(message, hint string) *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, "invalid_request", message, hint)
}
