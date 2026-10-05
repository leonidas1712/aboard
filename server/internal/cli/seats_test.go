package cli

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// SeatID names the exact seat an agent carries the member id of, never one found by
// its board and name: two seats can share a name over time.
func TestSeatIDIsTheExactSeat(t *testing.T) {
	const srv = "http://127.0.0.1:7400"
	home := t.TempDir()
	a := &app{env: Env{Dir: home, Getenv: func(k string) string {
		return map[string]string{"ABOARD_HOME": home, "ABOARD_LOCAL_ADDR": "127.0.0.1:7400"}[k]
	}}}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	old := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1A"
	now := "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1B"
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "docs", Name: "claude", MemberID: old, Token: "t1"},
		{Server: srv, Board: "docs", Name: "claude", MemberID: now, Token: "t2"},
	}}
	if err := writeJSONFile(p.credentials(), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	s := newDaemonSeats(a)
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude", MemberID: now}); !ok || id != now {
		t.Errorf("by member id: %q %v", id, ok)
	}
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude"}); ok {
		t.Errorf("by name: %q", id)
	}
	if id, ok := s.SeatID(delivery.AgentRef{Server: srv, Board: "docs", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1C"}); ok {
		t.Errorf("an unknown member id: %q", id)
	}
}
