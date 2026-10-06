package main

import (
	"testing"
	"time"
)

func TestDeliveryProofRejectsMissingDuplicateAndReorderedMessages(t *testing.T) {
	a := messageKey{BoardID: "board-a", Seq: 7}
	b := messageKey{BoardID: "board-a", Seq: 9}
	c := messageKey{BoardID: "board-b", Seq: 7}
	expected := map[string][]messageKey{"seat-a": {a, b}, "seat-b": {c}}
	for _, tc := range []struct {
		name string
		seen map[string][]messageKey
	}{
		{"missing message", map[string][]messageKey{"seat-a": {a}, "seat-b": {c}}},
		{"missing seat", map[string][]messageKey{"seat-a": {a, b}}},
		{"duplicate message", map[string][]messageKey{"seat-a": {a, a, b}, "seat-b": {c}}},
		{"reordered messages", map[string][]messageKey{"seat-a": {b, a}, "seat-b": {c}}},
		{"wrong board at same sequence", map[string][]messageKey{"seat-a": {c, b}, "seat-b": {c}}},
		{"unexpected seat", map[string][]messageKey{"seat-a": {a, b}, "seat-b": {c}, "seat-c": {a}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := (deliveryCheck{Expected: expected, Seen: tc.seen}).validate(); err == nil {
				t.Fatal("incomplete or incorrect delivery proof passed")
			}
		})
	}
	if err := (deliveryCheck{Expected: expected, Seen: expected}).validate(); err != nil {
		t.Fatalf("complete proof with equal sequences on different boards: %v", err)
	}
}

func TestStreamProofKeepsAHeadReceivedBeforeThePostAnswer(t *testing.T) {
	start := time.Unix(100, 0)
	observed := start.Add(10 * time.Millisecond)
	postAnswer := start.Add(30 * time.Millisecond)
	key := messageKey{BoardID: "board-a", Seq: 7}
	heads := []observation{
		{Key: messageKey{BoardID: "board-b", Seq: 99}, At: start.Add(time.Millisecond)},
		{Key: messageKey{BoardID: "board-a", Seq: 6}, At: start.Add(2 * time.Millisecond)},
		{Key: messageKey{BoardID: "board-a", Seq: 9}, At: observed},
		{Key: key, At: postAnswer.Add(time.Millisecond)},
	}
	got, err := firstHead(heads, key)
	if err != nil || !got.Equal(observed) {
		t.Fatalf("coalesced head observed before POST returned: got %v, error %v; want %v", got, err, observed)
	}
	if _, err := firstHead(heads, messageKey{BoardID: "board-a", Seq: 10}); err == nil {
		t.Fatal("missing stream observation passed")
	}
	if _, err := firstHead(heads, messageKey{BoardID: "board-c", Seq: 7}); err == nil {
		t.Fatal("another board's head satisfied a missing observation")
	}
}

func TestLatencyProofRejectsEmptySamplesAndIncludesTheTail(t *testing.T) {
	if _, err := summarize(nil); err == nil {
		t.Fatal("empty latency proof passed")
	}
	samples := make([]time.Duration, 100)
	for i := range samples {
		samples[i] = time.Duration(100-i) * time.Millisecond
	}
	got, err := summarize(samples)
	if err != nil {
		t.Fatal(err)
	}
	if got.Samples != 100 || got.P50 != 50 || got.P95 != 95 || got.P99 != 99 {
		t.Fatalf("latency proof omitted or miscomputed samples: %+v", got)
	}
	if samples[0] != 100*time.Millisecond {
		t.Fatal("summarizing changed the observations")
	}
}
