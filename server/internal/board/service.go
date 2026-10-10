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
	Blobs     Blobs
	ServerID  string
	Mode      string // "local" or "team"
	IssuerURL string // exact configured issuer URL used by credential-qualified handovers
	JoinHost  string // how join lines name this server: "localhost", "localhost:7411", a domain
}

// Service implements every board operation.
type Service struct {
	st                 Store
	notify             Notifier
	clk                clock.Clock
	gen                *ids.Generator
	key                []byte // keys the digests of tokens and join codes
	cfg                Config
	log                *slog.Logger
	adminAuthorization *AdminAuthorization
	adminActor         *events.Actor
	codes              *loginCodes
	uses               *keyUses
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
	if fields, ok := data.(map[string]any); ok {
		kind := ""
		switch typ {
		case events.PersonAdded:
			kind = "added"
			if fields["action_kind"] == "invited" {
				kind = "invited"
			}
		case events.PersonRoleChanged, events.PersonMadeOwner:
			kind = "role_changed"
		}
		if kind != "" {
			copied := make(map[string]any)
			for k, v := range fields {
				copied[k] = v
			}
			copied["action_kind"] = kind
			data = copied
		}
	}
	if s.adminAuthorization != nil {
		if original, ok := data.(map[string]any); ok {
			copied := make(map[string]any)
			for key, value := range original {
				copied[key] = value
			}
			copied["authorization"] = s.adminAuthorization.data()
			data = copied
		}
		if s.adminActor != nil && actor.Kind != "system" {
			actor = *s.adminActor
		}
	}
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

// see returns the board named name if p may see it, with p's membership and whether p
// is on it. A person sees the boards they are on and, unless they are a guest, every
// open board; an agent sees only its own board, while it and its person are on it. Any other board, whether or
// not it exists, is board_not_found with the same message, so a name never tells
// whether a board the caller can't see exists. It first checks, with the time read here,
// that p's credential still works. Callers run it inside the transaction
// that reads or writes the board, so a change of access can't let one more read in.
func (s *Service) see(tx ReadTx, p Principal, name string) (Board, Member, bool, error) {
	person, err := caller(tx, p, stamp(s.clk.Now()))
	if err != nil {
		return Board{}, Member{}, false, err
	}
	if p.Agent != nil {
		b, me, err := seatOf(tx, *p.Agent)
		if isBoardNotFound(err) || (err == nil && b.Name != name) {
			return Board{}, Member{}, false, apierr.BoardNotFound(name)
		}
		return b, me, err == nil, err
	}
	b, err := tx.BoardByName(name)
	if errors.Is(err, ErrNotFound) {
		return Board{}, Member{}, false, apierr.BoardNotFound(name)
	}
	if err != nil {
		return Board{}, Member{}, false, err
	}
	if lifecycleOf(b) == LifecycleDeleted {
		return Board{}, Member{}, false, apierr.BoardNotFound(name)
	}
	me, err := tx.HumanMember(b.ID, p.Human.ID)
	switch {
	case err == nil && me.Status == StatusActive:
		return b, me, true, nil
	case err != nil && !errors.Is(err, ErrNotFound):
		return Board{}, Member{}, false, err
	case b.Visibility == BoardOpen && person.Role != ServerGuest:
		return b, Member{}, false, nil
	}
	return Board{}, Member{}, false, apierr.BoardNotFound(name)
}

// access returns the board named name and the caller's membership of it, for anything
// that needs being on the board: reading its messages, members and events, and every
// write. A board the caller can't see is board_not_found; an open board a person isn't
// on is not_on_board.
func (s *Service) access(tx ReadTx, p Principal, name string) (Board, Member, error) {
	b, me, on, err := s.see(tx, p, name)
	if err != nil {
		return Board{}, Member{}, err
	}
	if !on {
		return Board{}, Member{}, notOnBoard(b.Name, p.Human.Name)
	}
	return b, me, nil
}

func notOnBoard(board, handle string) *apierr.Error {
	return apierr.New(http.StatusForbidden, "not_on_board",
		fmt.Sprintf("You aren't on board %s.", board),
		fmt.Sprintf("It is open, so you can join it: aboard board add @%s --board %s.", handle, board))
}

func isBoardNotFound(err error) bool {
	var e *apierr.Error
	return errors.As(err, &e) && e.Code == "board_not_found"
}

// seatOf returns an agent's board and its current membership. An agent acts only within
// its person's current access, so once the agent or its person is no longer on the board
// it fails with board_not_found, as if the board weren't there.
func seatOf(tx ReadTx, agent Member) (Board, Member, error) {
	b, err := tx.BoardByID(agent.BoardID)
	if err != nil {
		return Board{}, Member{}, err
	}
	if lifecycleOf(b) == LifecycleDeleted {
		return Board{}, Member{}, apierr.BoardNotFound(b.Name)
	}
	me, err := tx.MemberByName(b.ID, agent.Name)
	if errors.Is(err, ErrNotFound) || (err == nil && (me.ID != agent.ID || me.Status != StatusActive)) {
		return Board{}, Member{}, apierr.BoardNotFound(b.Name)
	}
	if err != nil {
		return Board{}, Member{}, err
	}
	person, err := tx.HumanMember(b.ID, me.HumanID)
	if errors.Is(err, ErrNotFound) || (err == nil && person.Status != StatusActive) {
		return Board{}, Member{}, apierr.BoardNotFound(b.Name)
	}
	if err != nil {
		return Board{}, Member{}, err
	}
	return b, me, nil
}

// present keeps the members who are on the board now: the people whose membership is
// active, and the active agents of those people.
func present(members []Member) []Member {
	people := map[string]bool{}
	for _, m := range members {
		if m.Kind == "human" && m.Status == StatusActive {
			people[m.HumanID] = true
		}
	}
	out := make([]Member, 0, len(members))
	for _, m := range members {
		if m.Status == StatusActive && people[m.HumanID] {
			out = append(out, m)
		}
	}
	return out
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
		if m.Rules().IsAdmin() && m.Status == StatusActive {
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
