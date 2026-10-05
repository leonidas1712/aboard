// Package board is Aboard's domain logic: creating boards, joining them, posting and
// reading messages, and serving the event log. Every write follows the same path:
// authenticate, check membership and permissions, then in one transaction append the
// hash-chained event and update the read models, then wake anyone waiting. Storage and
// waking are ports (Store, Notifier) that adapters implement; this package never
// imports them.
package board

import (
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
	codes  *loginCodes
	uses   *keyUses
}

// New returns a Service that keeps its data in st and wakes waiting readers through
// notify. key must be the server's secret digest key.
func New(st Store, notify Notifier, clk clock.Clock, gen *ids.Generator, key []byte, cfg Config, log *slog.Logger) *Service {
	return &Service{st: st, notify: notify, clk: clk, gen: gen, key: key, cfg: cfg, log: log, codes: newLoginCodes(), uses: &keyUses{last: map[string]time.Time{}}}
}

// Config returns the server description the Service was created with.
func (s *Service) Config() Config { return s.cfg }

// stamp formats a time the way every timestamp in Aboard is written.
func stamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func ptr[T any](v T) *T { return &v }

// deref returns the string p points to, or "" for nil.
func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

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

// ownerOnBoard returns the person an agent acts for, as a member of b, or me itself for
// a person, or for an agent whose owner isn't on the board (so it has no access).
func ownerOnBoard(tx ReadTx, b Board, me Member) (Member, error) {
	if me.Rules().IsHuman() {
		return me, nil
	}
	members, err := tx.Members(b.ID)
	if err != nil {
		return Member{}, err
	}
	for _, m := range members {
		if m.Rules().IsHuman() && m.HumanID == me.HumanID {
			return m, nil
		}
	}
	return me, nil
}

// requireAdmin refuses unless me is an admin of b. The hint names the board's admins, so
// the person knows whom to ask; what says what they tried to do.
func requireAdmin(tx ReadTx, b Board, me Member, what string) error {
	if me.Rules().IsAdmin() {
		return nil
	}
	members, err := tx.Members(b.ID)
	if err != nil {
		return err
	}
	var admins []string
	for _, m := range members {
		if m.Rules().IsAdmin() {
			admins = append(admins, m.Name)
		}
	}
	return apierr.New(http.StatusForbidden, "admin_required",
		fmt.Sprintf("Only an admin of board %s can %s.", b.Name, what),
		fmt.Sprintf("Ask an admin to do it: %s.", strings.Join(admins, ", ")))
}

func invalid(message, hint string) *apierr.Error {
	return apierr.New(http.StatusUnprocessableEntity, "invalid_request", message, hint)
}
