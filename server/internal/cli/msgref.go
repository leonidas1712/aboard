package cli

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// messageRef is a reference to a message given on the command line.
type messageRef struct {
	// ID is set for a message id such as msg_….
	ID string
	// Board is set for a sequence number on another board, as in board-name#6.
	Board string
	// Seq is the message's sequence number when ID is empty.
	Seq int
}

var boardNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,38}[a-z0-9]$`)

// parseMessageRef reads a message id (msg_…), a sequence number on the current board
// (6 or #6) or a sequence number on another board (board-name#6).
func parseMessageRef(s string) (messageRef, error) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "msg_") && len(s) > len("msg_") {
		return messageRef{ID: s}, nil
	}
	board, num, found := strings.Cut(s, "#")
	if !found {
		board, num = "", s
	}
	if board != "" && !boardNamePattern.MatchString(board) {
		return messageRef{}, messageRefError(s)
	}
	seq, err := strconv.Atoi(num)
	if err != nil || seq < 1 || strings.HasPrefix(num, "+") {
		return messageRef{}, messageRefError(s)
	}
	return messageRef{Board: board, Seq: seq}, nil
}

func messageRefError(s string) *Error {
	return newError("message_ref_invalid", fmt.Sprintf("%q is not a message reference.", s),
		"Use a message id (msg_…), a number on this board (6 or #6), or a number on another board (board-name#6).")
}

// resolveMessageRef returns the id of the message ref points at. board is the board the
// command acts on, cur is the agent acting on it.
func (a *app) resolveMessageRef(ctx context.Context, ref messageRef, t target, cur agentCredential) (string, error) {
	if ref.ID != "" {
		return ref.ID, nil
	}
	board, cred := t.board, cur
	if ref.Board != "" && ref.Board != t.board {
		creds, err := a.readCredentials()
		if err != nil {
			return "", err
		}
		names := creds.names(t.server.URL, ref.Board)
		if len(names) == 0 {
			return "", newError("message_ref_invalid",
				fmt.Sprintf("This machine has no agent on board %s, so it can't look up %s#%d.", ref.Board, ref.Board, ref.Seq),
				"Use the message id (msg_…) instead, or join that board first.")
		}
		board = ref.Board
		cred, _ = creds.find(t.server.URL, board, names[0])
	}
	c, err := a.client(t.server, cred.Token, requestTimeout)
	if err != nil {
		return "", err
	}
	page, err := c.messages(ctx, board, ref.Seq-1, 1)
	if err != nil {
		return "", err
	}
	if len(page.Messages) == 0 || page.Messages[0].Seq != ref.Seq {
		return "", newError("message_ref_invalid",
			fmt.Sprintf("There is no message #%d on board %s that %s can see.", ref.Seq, board, cred.Name),
			"Run aboard read to see the messages and their numbers.")
	}
	return page.Messages[0].Id, nil
}
