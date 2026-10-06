package cli

import (
	"context"
	"sync"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/apiserver"
)

// daemonSeats is how the delivery daemon reaches each server through this machine's
// delegation, and saves the seats it is given in credentials.json. The delegation is
// named after the machine and held only in the daemon's memory.
type daemonSeats struct {
	a    *app
	name string

	mu        sync.Mutex
	delegated map[string]*apiserver.Delegated
}

var _ delivery.Seats = (*daemonSeats)(nil)

func newDaemonSeats(a *app) *daemonSeats {
	return &daemonSeats{a: a, name: machineName(), delegated: map[string]*apiserver.Delegated{}}
}

// server returns the delegation's connection to url, made on first use.
func (s *daemonSeats) server(url string) *apiserver.Delegated {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.delegated[url]
	if !ok {
		d = apiserver.NewDelegated(url, s.name, daemonTokens{a: s.a})
		s.delegated[url] = d
	}
	return d
}

func (s *daemonSeats) Boards(ctx context.Context, server, lifecycle string) (delivery.SeatBoards, error) {
	return s.server(server).Boards(ctx, lifecycle)
}

func (s *daemonSeats) Join(ctx context.Context, server string, req delivery.SeatRequest) (delivery.SeatGrant, error) {
	g, err := s.server(server).Join(ctx, req)
	g.Seat.Server = server
	return g, err
}

// Save writes the seat's token to credentials.json, replacing its earlier one. The file
// is written whole and renamed into place.
func (s *daemonSeats) Save(_ context.Context, seat delivery.SeatRef, token string) error {
	return s.a.saveCredential(agentCredential{Server: seat.Server, MemberID: seat.MemberID, Board: seat.Board, Name: seat.Name, Token: token})
}

// SeatID returns the agent's member id when credentials.json keeps that exact seat on
// its server. It never finds a seat by board and name: two seats can share a name over
// time.
func (s *daemonSeats) SeatID(agent delivery.AgentRef) (string, bool) {
	if agent.MemberID == "" {
		return "", false
	}
	creds, err := s.a.readCredentials()
	if err != nil {
		return "", false
	}
	for _, c := range creds.Agents {
		if c.Server == agent.Server && c.MemberID == agent.MemberID {
			return c.MemberID, true
		}
	}
	return "", false
}

// seatCredential finds the credential of the exact seat with memberID on server, never
// one with the same board and name: a replacement seat can take an old seat's name.
func seatCredential(creds credentials, server, memberID string) (agentCredential, bool) {
	if memberID == "" {
		return agentCredential{}, false
	}
	for _, c := range creds.Agents {
		if c.Server == server && c.MemberID == memberID {
			return c, true
		}
	}
	return agentCredential{}, false
}

// Create asks for a board and seat through the same server-bound delegation.
func (s *daemonSeats) Create(ctx context.Context, server string, req delivery.SeatCreateRequest) (delivery.SeatGrant, error) {
	return s.server(server).Create(ctx, req)
}
