package events

import (
	"encoding/json"
	"testing"
)

func TestCanonicalJSON(t *testing.T) {
	tests := []struct{ name, in, want string }{
		{"keys sorted, whitespace removed", `{ "b": 1, "a": [true, null] }`, `{"a":[true,null],"b":1}`},
		{"nested objects sorted", `{"z":{"y":1,"x":2},"a":0}`, `{"a":0,"z":{"x":2,"y":1}}`},
		{"non-ASCII kept as UTF-8", `{"s":"café ✓"}`, `{"s":"café ✓"}`},
		{"HTML characters not escaped", `{"s":"<a&b>"}`, `{"s":"<a&b>"}`},
		{"control characters escaped", `{"s":"a\u0001\n"}`, `{"s":"a\u0001\n"}`},
		{"line separator kept raw", "{\"s\":\"a\\u2028b\"}", "{\"s\":\"a b\"}"},
		{"integers", `[1, -0, 10.0, 1e3]`, `[1,0,10,1000]`},
		{"large and small numbers", `[1e21, 1.5e-7, 123456789012]`, `[1e+21,1.5e-7,123456789012]`},
		{"keys sorted by UTF-16 code units", `{"😀":1,"｡":2}`, "{\"\U0001F600\":1,\"｡\":2}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CanonicalizeJSON([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Fatalf("got  %s\nwant %s", got, tt.want)
			}
		})
	}
}

func chain(t *testing.T, bodies ...string) []Event {
	t.Helper()
	prev := GenesisHash
	var evs []Event
	for i, body := range bodies {
		name := "writer"
		e := Event{
			ID: "evt_" + body, BoardID: "brd_1", Seq: int64(i + 1), Type: MessagePosted,
			At: "2026-10-01T16:20:31.204Z", Actor: Actor{Kind: "agent", Name: &name}, PrevHash: prev,
		}
		if err := e.Seal(map[string]any{"body": body}); err != nil {
			t.Fatal(err)
		}
		prev = e.Hash
		evs = append(evs, e)
	}
	return evs
}

func TestVerifierAcceptsAnIntactChain(t *testing.T) {
	v := NewVerifier()
	evs := chain(t, "a", "b", "c")
	if p := v.Add(evs[:2]); p != nil {
		t.Fatalf("first page: %+v", p)
	}
	if p := v.Add(evs[2:]); p != nil {
		t.Fatalf("second page: %+v", p)
	}
	if v.Checked != 3 || v.LastSeq != 3 || v.LastHash != evs[2].Hash {
		t.Fatalf("verifier state %+v", v)
	}
}

func TestVerifierFindsTampering(t *testing.T) {
	tests := []struct {
		name   string
		tamper func([]Event) []Event
		want   Problem
	}{
		{"edited payload", func(e []Event) []Event { e[1].Data = json.RawMessage(`{"body":"x"}`); return e }, Problem{2, DataHashMismatch}},
		{"edited payload with recomputed data hash", func(e []Event) []Event {
			e[1].Data = json.RawMessage(`{"body":"x"}`)
			e[1].DataHash = HashBytes(e[1].Data)
			return e
		}, Problem{2, HashMismatch}},
		{"removed event", func(e []Event) []Event { return append(e[:1], e[2:]...) }, Problem{2, SeqGap}},
		{"rewritten link", func(e []Event) []Event { e[2].PrevHash = e[0].Hash; return e }, Problem{3, PrevHashMismatch}},
		{"edited actor", func(e []Event) []Event { n := "reviewer"; e[0].Actor.Name = &n; return e }, Problem{1, HashMismatch}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewVerifier().Add(tt.tamper(chain(t, "a", "b", "c")))
			if p == nil || *p != tt.want {
				t.Fatalf("got %+v, want %+v", p, tt.want)
			}
		})
	}
}

func TestVerifierChecksLinksOfWithheldPayloads(t *testing.T) {
	evs := chain(t, "a", "b", "c")
	evs[1].Data, evs[1].DataWithheld = nil, true
	v := NewVerifier()
	if p := v.Add(evs); p != nil {
		t.Fatalf("got %+v", p)
	}
	if v.Withheld != 1 || v.Checked != 3 {
		t.Fatalf("verifier state %+v", v)
	}
	evs[1].DataHash = HashBytes([]byte("{}"))
	if p := NewVerifier().Add(evs); p == nil || p.Reason != HashMismatch {
		t.Fatalf("a changed data hash on a withheld event must break the chain, got %+v", p)
	}
}
