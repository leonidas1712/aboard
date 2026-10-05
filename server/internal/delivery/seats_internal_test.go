package delivery

import "testing"

// A joined seat is bound by its member id, not only its name: a daemon that keys seats
// by id refuses a name-only selector for a seat whose credentials carry one.
func TestAJoinedSeatIsBoundByItsMemberID(t *testing.T) {
	g := SeatGrant{Seat: SeatRef{Server: "https://team.example.com", Board: "docs", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1W"}}
	want := AgentRef{Server: "https://team.example.com", Board: "docs", Name: "claude", MemberID: "mem_01JB8Z3K7Q4M2N5P6R8S9T0V1W"}
	if got := grantAgent(g); got != want {
		t.Fatalf("bound as %+v, want %+v", got, want)
	}
}
