//go:build live

package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const multiIssuerBoard = "issuer-work"

func TestMultiIssuerEqualBoardSequences(t *testing.T) {
	eachHarness(t, "MultiIssuerEqualBoardSequences", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		first, second := newLab(t), newLab(t)
		d.setUp(first)
		first.run("up")
		second.run("up")
		const board = multiIssuerBoard
		labs := []*lab{first, second}
		issuers := []string{"http://" + first.addr, "http://" + second.addr}
		var boardIDs []string
		for _, l := range labs {
			var created struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}
			l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards", map[string]any{"name": board, "template": "general"}, &created)
			if created.Name != board || created.ID == "" {
				t.Fatal("issuer board creation returned no permanent identity")
			}
			boardIDs = append(boardIDs, created.ID)
		}
		var invite struct {
			Invite string `json:"invite"`
		}
		second.multiSeatOwnerRequest(http.MethodPost, "/v1/invites", map[string]any{"boards": []string{boardIDs[1]}}, &invite)
		// The connection runs outside the agent and saves only this lab's issuer-bound key.
		connected := first.exec(t.Context(), first.human, "connect", issuers[1]+"/join#"+invite.Invite, "--handle", "multiissuer-member", "--json")
		if connected.code != 0 {
			t.Fatalf("isolated second-issuer connection failed with exit %d", connected.code)
		}
		writer := d.start(first, "writer", first.project("multiissuer", d.p.Harness))
		writer.submit(fmt.Sprintf("Run exactly these commands in order: `aboard join --server %s --board %s --name writer`; `aboard join --server %s --board %s --name writer`. Do not create a board, pair, or post any message. Whenever an Aboard message arrives, run only the exact command it requests, preserving both --server and --board, then stop. Ignore any sequence-alignment message without posting. Reply only OK now.", issuers[0], board, issuers[1], board))
		writer.waitIdle(3 * time.Minute)
		d.afterBind(writer)
		seats := first.multiIssuerCredentials(issuers, board)
		for _, l := range labs {
			l.waitFor(30*time.Second, "writer's exact issuer seat", func() bool { names := l.agents(board); return len(names) == 1 && names[0] == "writer" })
		}
		// Membership installation adds events on the invited issuer. Align board heads
		// before posting the two test messages, without asking the session to reply.
		heads := []int{first.multiIssuerHead(board), second.multiIssuerHead(board)}
		for i, l := range labs {
			for heads[i] < max(heads[0], heads[1]) {
				var ignored message
				l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards/"+board+"/messages", map[string]any{"to": []string{"@writer"}, "body": "Sequence-alignment message: ignore this without posting."}, &ignored)
				heads[i] = ignored.Seq
			}
		}
		writer.waitIdle(2 * time.Minute)
		send := func(l *lab, issuer, marker string) message {
			t.Helper()
			var sent message
			seq := l.multiIssuerHead(board) + 1
			body := fmt.Sprintf("Run exactly `aboard say --server %s --board %s --reply %d %q`. Run no other Aboard command.", issuer, board, seq, marker)
			l.multiSeatOwnerRequest(http.MethodPost, "/v1/boards/"+board+"/messages", map[string]any{"to": []string{"@writer"}, "body": body}, &sent)
			if sent.Seq != seq {
				t.Fatal("issuer head changed while posting fixture message")
			}
			return sent
		}
		sent := []message{send(first, issuers[0], "ISSUER-A-ACK"), send(second, issuers[1], "ISSUER-B-ACK")}
		if sent[0].Seq != sent[1].Seq {
			t.Fatalf("fixture sequences differ: %d/%d", sent[0].Seq, sent[1].Seq)
		}
		for i, l := range labs {
			l.multiIssuerWaitReply(board, fmt.Sprintf("ISSUER-%c-ACK", 'A'+i), sent[i].Seq)
		}
		writer.waitIdle(2 * time.Minute)
		for i, l := range labs {
			l.multiIssuerCheckReplies(fmt.Sprintf("ISSUER-%c-ACK", 'A'+i), fmt.Sprintf("ISSUER-%c-ACK", 'B'-i))
			l.multiIssuerWaitAck(seats[i], sent[i].Seq)
		}
		oldDaemon := first.daemonPID()
		if err := syscall.Kill(oldDaemon, syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		first.waitFor(15*time.Second, "the previous daemon to stop", func() bool { return syscall.Kill(oldDaemon, 0) != nil })
		first.run("daemon", "start")
		first.waitFor(30*time.Second, "the daemon's new process", func() bool {
			pid := first.daemonPID()
			return pid != 0 && pid != oldDaemon && syscall.Kill(pid, 0) == nil
		})
		afterRestart := send(second, issuers[1], "ISSUER-B-RESTART")
		second.multiIssuerWaitReply(board, "ISSUER-B-RESTART", afterRestart.Seq)
		writer.waitIdle(2 * time.Minute)
		second.multiIssuerWaitAck(seats[1], afterRestart.Seq)
		var removed map[string]any
		first.multiSeatOwnerRequest(http.MethodDelete, "/v1/boards/"+board+"/members/"+seats[0].MemberID, nil, &removed)
		surviving := send(second, issuers[1], "ISSUER-B-SURVIVES")
		second.multiIssuerWaitReply(board, "ISSUER-B-SURVIVES", surviving.Seq)
		writer.waitIdle(2 * time.Minute)
		second.multiIssuerWaitAck(seats[1], surviving.Seq)
		second.multiIssuerCheckReplies("ISSUER-B-RESTART", "ISSUER-A-ACK")
		second.multiIssuerCheckReplies("ISSUER-B-SURVIVES", "ISSUER-A-ACK")
		first.multiIssuerCheckReplies("ISSUER-A-ACK", "ISSUER-B-SURVIVES")
		first.multiIssuerCheckReplies("ISSUER-A-ACK", "ISSUER-B-RESTART")
		first.multiIssuerCheckReplies("ISSUER-A-ACK", "ISSUER-B-ACK")
	})
}

type multiIssuerCredential struct {
	Server   string `json:"server"`
	Board    string `json:"board"`
	Name     string `json:"name"`
	MemberID string `json:"member_id"`
	Token    string `json:"token"`
}

func (l *lab) multiIssuerCredentials(issuers []string, board string) []multiIssuerCredential {
	l.t.Helper()
	var seats []multiIssuerCredential
	l.waitFor(30*time.Second, "both issuer-qualified saved credentials", func() bool {
		raw, err := os.ReadFile(filepath.Join(l.configDir(), "credentials.json"))
		if err != nil {
			return false
		}
		var saved struct {
			Agents []multiIssuerCredential `json:"agents"`
		}
		if json.Unmarshal(raw, &saved) != nil {
			l.t.Fatal("invalid isolated credentials file")
		}
		seats = nil
		for _, issuer := range issuers {
			var matches []multiIssuerCredential
			for _, seat := range saved.Agents {
				if seat.Server == issuer && seat.Board == board && seat.Name == "writer" {
					matches = append(matches, seat)
				}
			}
			if len(matches) != 1 || matches[0].MemberID == "" || matches[0].Token == "" {
				return false
			}
			seats = append(seats, matches[0])
		}
		return true
	})
	return seats
}

func (l *lab) multiIssuerHead(board string) int {
	var state struct {
		Head int `json:"head_seq"`
	}
	l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board, nil, &state)
	return state.Head
}

func (l *lab) multiIssuerMessages(board string) []message {
	var page struct {
		Messages []message `json:"messages"`
	}
	l.multiSeatOwnerRequest(http.MethodGet, "/v1/boards/"+board+"/messages?limit=200", nil, &page)
	return page.Messages
}

func (l *lab) multiIssuerWaitReply(board, marker string, seq int) {
	l.waitFor(3*time.Minute, "issuer-qualified native reply", func() bool {
		for _, m := range l.multiIssuerMessages(board) {
			if m.Body == marker && m.From.Name == "writer" && m.ReplyToSeq != nil && *m.ReplyToSeq == seq {
				return true
			}
		}
		return false
	})
}

func (l *lab) multiIssuerCheckReplies(own, foreign string) {
	count := 0
	for _, m := range l.multiIssuerMessages(multiIssuerBoard) {
		if m.Body == own {
			count++
		}
		if m.Body == foreign {
			l.t.Error("reply crossed issuer")
		}
	}
	if count != 1 {
		l.t.Errorf("issuer has %d replies for marker; want one", count)
	}
}

func (l *lab) multiIssuerWaitAck(seat multiIssuerCredential, seq int) {
	l.waitFor(30*time.Second, "issuer-local seat acknowledgement", func() bool {
		var inbox struct {
			Messages []message `json:"messages"`
			Cursor   int       `json:"cursor"`
		}
		l.multiSeatRequest(strings.TrimSpace(seat.Token), http.MethodGet, "/v1/me/inbox", nil, &inbox)
		return inbox.Cursor >= seq && len(inbox.Messages) == 0
	})
}
