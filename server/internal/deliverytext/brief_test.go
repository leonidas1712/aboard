package deliverytext

import (
	"testing"
	"time"
)

func TestBriefReminderNeedsAgeVisibleProgressAndNudges(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name            string
		age             time.Duration
		messages, tasks int
		enabled, want   bool
	}{
		{"messages boundary", time.Hour, 30, 0, true, true},
		{"tasks boundary", time.Hour, 0, 3, true, true},
		{"young", time.Hour - time.Second, 30, 3, true, false},
		{"quiet", time.Hour, 29, 2, true, false},
		{"off", time.Hour, 30, 3, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := TaskWork{Nudges: tc.enabled, Brief: &BriefContext{FileID: "fil_original", Name: "brief.md", Version: 2, At: now.Add(-tc.age), MessagesSince: tc.messages, TasksDoneSince: tc.tasks}}
			if got := BriefStale(w, now, "board-a", Context{BoardQualified: true}); (got != nil) != tc.want {
				t.Fatalf("nudge=%+v want=%v", got, tc.want)
			}
		})
	}
	if BriefStale(TaskWork{Nudges: true}, now, "board-a") != nil {
		t.Fatal("nonkeeper facts must give no reminder")
	}
}
