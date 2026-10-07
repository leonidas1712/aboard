package cli

import (
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

func TestBriefNudgeHistoryUsesIssuerSeatFileAndVersion(t *testing.T) {
	home := t.TempDir()
	a := &app{env: Env{Getenv: func(k string) string { return map[string]string{"HOME": home, "ABOARD_HOME": home}[k] }}}
	ref := delivery.AgentRef{Server: "https://a.example", MemberID: "mem_keeper"}
	brief := deliverytext.BriefContext{FileID: "fil_original", Version: 1, MessagesSince: 30}
	if !a.allowBriefNudge(ref, brief) || a.allowBriefNudge(ref, brief) {
		t.Fatal("threshold must be recorded once")
	}
	brief.MessagesSince = 29
	if a.allowBriefNudge(ref, brief) {
		t.Fatal("regressed counts erased recorded threshold")
	}
	brief.MessagesSince = 60
	if !a.allowBriefNudge(ref, brief) {
		t.Fatal("new threshold must allow reminder")
	}
	brief.Version = 2
	if !a.allowBriefNudge(ref, brief) {
		t.Fatal("new version inherited old history")
	}
	brief.FileID = "fil_recreated"
	if !a.allowBriefNudge(ref, brief) {
		t.Fatal("recreated brief inherited old history")
	}
	ref.Server = "https://b.example"
	if !a.allowBriefNudge(ref, brief) {
		t.Fatal("another issuer inherited old history")
	}
	ref.MemberID = "mem_other"
	if !a.allowBriefNudge(ref, brief) {
		t.Fatal("another seat inherited old history")
	}
}
