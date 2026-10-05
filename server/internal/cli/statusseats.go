package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// seatRow is one of a session's seats as aboard status lists them (cli.yaml, Seat):
// keyed by member id, with its board and name for display, and what its server says of
// it, read with its own token.
type seatRow struct {
	Server   string  `json:"server"`
	Board    string  `json:"board"`
	Name     string  `json:"name"`
	MemberID string  `json:"member_id"`
	Role     *string `json:"role"`
	Delivery string  `json:"delivery"`
	Unread   *int    `json:"unread"`
	Presence *string `json:"presence,omitempty"`
}

// sessionSeats reads each of a session's seats from its server with the seat's own
// token. A seat the server can't be asked about keeps what this machine knows: its
// delivery mode as the daemon applies it, and no unread count.
func (a *app) sessionSeats(ctx context.Context, agents []delivery.AgentRef, creds credentials) []seatRow {
	rows := make([]seatRow, 0, len(agents))
	for _, ag := range agents {
		row := seatRow{Server: ag.Server, Board: ag.Board, Name: ag.Name, MemberID: ag.MemberID, Delivery: string(delivery.ModeFocused)}
		if m, err := a.deliveryMode(ctx, ag); err == nil {
			row.Delivery = string(m)
		}
		// The seat is found by its member id only, so a replacement seat with the same
		// name never fills its row.
		if cred, ok := seatCredential(creds, ag.Server, ag.MemberID); ok {
			a.readSeat(ctx, cred, &row)
		}
		rows = append(rows, row)
	}
	slices.SortFunc(rows, func(x, y seatRow) int { return strings.Compare(x.Board, y.Board) })
	return rows
}

// readSeat fills a seat's role, mode, presence and unread inbox from its server, for
// the member with the seat's id.
func (a *app) readSeat(ctx context.Context, cred agentCredential, row *seatRow) {
	srv := a.serverRefFor(cred.Server)
	c, err := a.client(ctx, srv, cred.Token, requestTimeout)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	if m, err := c.api.ListMembersWithResponse(ctx, cred.Board); err == nil && m.JSON200 != nil {
		for _, mem := range m.JSON200.Members {
			if mem.Id != cred.MemberID || mem.Kind != api.MemberKindAgent {
				continue
			}
			row.Role, row.Name = mem.Role, mem.Name
			if held := heldModeOf(mem); held != "" {
				row.Delivery = held
			}
			if mem.Presence != nil {
				p := string(*mem.Presence)
				row.Presence = &p
			}
		}
	}
	if b, err := c.api.ListBoardsWithResponse(ctx, &api.ListBoardsParams{}); err == nil && b.JSON200 != nil {
		for _, x := range b.JSON200.Boards {
			if x.Name == cred.Board {
				row.Unread = x.Unread
			}
		}
	}
}

// seatsText is aboard status's Seats lines for a session with several seats, and the
// two reminder lines (seats_usage).
func seatsText(server string, rows []seatRow) (text string, usage []string) {
	var b strings.Builder
	fmt.Fprintf(&b, "Seats:  %d in this session, on %s\n", len(rows), server)
	boardW, nameW, roleW, modeW := 0, 0, 0, 0
	for _, r := range rows {
		boardW, nameW = max(boardW, len(r.Board)), max(nameW, len(r.Name))
		roleW, modeW = max(roleW, len(deref(r.Role))), max(modeW, len(r.Delivery))
	}
	for _, r := range rows {
		cols := []string{
			fmt.Sprintf("%-*s", boardW, r.Board), fmt.Sprintf("%-*s", nameW, r.Name),
			fmt.Sprintf("%-*s", roleW, deref(r.Role)), fmt.Sprintf("%-*s", modeW, r.Delivery),
		}
		if r.Presence != nil {
			cols = append(cols, presenceText(*r.Presence))
		}
		if r.Unread != nil && *r.Unread > 0 {
			cols = append(cols, fmt.Sprintf("%d unread", *r.Unread))
		}
		b.WriteString("          " + strings.TrimRight(strings.Join(cols, "  "), " ") + "\n")
	}
	usage = []string{
		fmt.Sprintf("Commands that act on one board need --board, such as aboard say --board %s \"…\".", rows[0].Board),
		fmt.Sprintf("Reply with: aboard say --board %s --reply SEQ \"…\"", rows[len(rows)-1].Board),
	}
	for _, u := range usage {
		b.WriteString("        " + u + "\n")
	}
	return b.String(), usage
}
