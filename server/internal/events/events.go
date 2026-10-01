// Package events defines a board's append-only event log entry and its hash chain. Each
// event's hash covers the previous event's hash and a hash of its payload, so editing or
// removing any stored event is detectable, even by a reader who may not see every payload.
package events

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// GenesisHash is the prev_hash of a board's first event.
var GenesisHash = "sha256:" + strings.Repeat("0", 64)

// Event types written to the log.
const (
	BoardCreated       = "board.created"
	BoardPolicyChanged = "board.policy_changed"
	MemberJoined       = "member.joined"
	JoinCodeCreated    = "joincode.created"
	JoinCodeRevoked    = "joincode.revoked"
	MessagePosted      = "message.posted"
)

// Actor is who caused an event, taken from the authenticated token.
type Actor struct {
	Kind     string  `json:"kind"`
	MemberID *string `json:"member_id"`
	Name     *string `json:"name"`
	Owner    *string `json:"owner"`
}

// Event is one entry in a board's log, as stored and as served by the API.
type Event struct {
	ID           string          `json:"id"`
	BoardID      string          `json:"board_id"`
	Seq          int64           `json:"seq"`
	Type         string          `json:"type"`
	At           string          `json:"at"`
	Actor        Actor           `json:"actor"`
	Data         json.RawMessage `json:"data,omitempty"`
	DataWithheld bool            `json:"data_withheld,omitempty"`
	DataHash     string          `json:"data_hash"`
	PrevHash     string          `json:"prev_hash"`
	Hash         string          `json:"hash"`
}

// header is the part of an event its hash covers: everything except the payload itself
// (covered through data_hash) and the hash.
type header struct {
	ID       string `json:"id"`
	BoardID  string `json:"board_id"`
	Seq      int64  `json:"seq"`
	Type     string `json:"type"`
	At       string `json:"at"`
	Actor    Actor  `json:"actor"`
	DataHash string `json:"data_hash"`
	PrevHash string `json:"prev_hash"`
}

// HashBytes returns "sha256:" + the hex SHA-256 of b.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Seal canonicalizes data, then fills in the event's Data, DataHash and Hash. The caller
// sets every other field first, including PrevHash.
func (e *Event) Seal(data any) error {
	canon, err := Canonical(data)
	if err != nil {
		return fmt.Errorf("canonicalize %s data: %w", e.Type, err)
	}
	e.Data = canon
	e.DataHash = HashBytes(canon)
	h, err := e.computeHash()
	if err != nil {
		return err
	}
	e.Hash = h
	return nil
}

func (e *Event) computeHash() (string, error) {
	canon, err := Canonical(header{
		ID: e.ID, BoardID: e.BoardID, Seq: e.Seq, Type: e.Type, At: e.At,
		Actor: e.Actor, DataHash: e.DataHash, PrevHash: e.PrevHash,
	})
	if err != nil {
		return "", fmt.Errorf("canonicalize event header: %w", err)
	}
	return HashBytes(canon), nil
}

// Problem reasons reported by a Verifier.
const (
	SeqGap           = "seq_gap"
	PrevHashMismatch = "prev_hash_mismatch"
	HashMismatch     = "hash_mismatch"
	DataHashMismatch = "data_hash_mismatch"
)

// Problem is the first event that failed verification, and why.
type Problem struct {
	Seq    int64
	Reason string
}

// Verifier checks a board's events in order, one page at a time.
type Verifier struct {
	LastSeq  int64
	LastHash string
	Checked  int
	Withheld int
}

// NewVerifier starts verification from the beginning of a log.
func NewVerifier() *Verifier { return &Verifier{LastHash: GenesisHash} }

// Add checks the next events. It returns the first problem found, or nil.
func (v *Verifier) Add(evs []Event) *Problem {
	for i := range evs {
		e := &evs[i]
		switch {
		case e.Seq != v.LastSeq+1:
			return &Problem{Seq: v.LastSeq + 1, Reason: SeqGap}
		case e.PrevHash != v.LastHash:
			return &Problem{Seq: e.Seq, Reason: PrevHashMismatch}
		}
		if h, err := e.computeHash(); err != nil || h != e.Hash {
			return &Problem{Seq: e.Seq, Reason: HashMismatch}
		}
		if e.DataWithheld {
			v.Withheld++
		} else {
			canon, err := CanonicalizeJSON(e.Data)
			if err != nil || HashBytes(canon) != e.DataHash {
				return &Problem{Seq: e.Seq, Reason: DataHashMismatch}
			}
		}
		v.LastSeq, v.LastHash = e.Seq, e.Hash
		v.Checked++
	}
	return nil
}
