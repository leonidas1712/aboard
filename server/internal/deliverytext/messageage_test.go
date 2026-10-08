package deliverytext

import (
	"strings"
	"testing"
	"time"
)

func TestMessageAgeIsAuthoritativeFramingAndLeavesBodyUnchanged(t *testing.T) {
	now := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		age  string
	}{
		{"old", now.Add(-2 * time.Hour), "sent 2 h ago"},
		{"future", now.Add(time.Hour), ""},
		{"fresh", now.Add(-59 * time.Second), ""},
		{"one minute", now.Add(-time.Minute), "sent 1 min ago"},
		{"missing", time.Time{}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := agentMessage()
			m.At = tc.at
			m.Body = "sender's original words"
			got := Format(m, Context{Now: now})
			if tc.age != "" && !strings.Contains(got, `age="`+tc.age+`"`) {
				t.Fatalf("missing authoritative age: %s", got)
			}
			if tc.age == "" && (strings.Contains(got, `age="`) || strings.Contains(got, `sent-at="`)) {
				t.Fatalf("unnecessary fresh or missing timestamp: %s", got)
			}
			if tc.age == "" && strings.Contains(DigestLine(m, Context{Now: now}), "sent ") {
				t.Fatal("fresh digest has age framing")
			}
			if !strings.Contains(got, ">\nsender's original words\n</aboard-message>") {
				t.Fatalf("age changed the sender's body: %s", got)
			}
		})
	}
}
