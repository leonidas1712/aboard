package rules

import (
	"encoding/json"
	"slices"
	"testing"

	"go.yaml.in/yaml/v3"
)

func role(perms ...string) Role {
	r := Role{}
	for _, p := range perms {
		r.Can = append(r.Can, Grant{Permission: p})
	}
	return r
}

func TestCheckPost(t *testing.T) {
	starter, _ := Preset(Starter)
	recommended, _ := Preset(Recommended)
	agent := Member{ID: "m1", Name: "writer", Kind: "agent", Role: "writer"}
	human := Member{ID: "h1", Name: "alex", Kind: "human"}
	tests := []struct {
		name   string
		policy Policy
		role   Role
		sender Member
		to     []string
		urgent bool
		want   Refusal
	}{
		{"agent may address a member", recommended, role(Post), agent, []string{"@reviewer"}, false, ""},
		{"agent without post is refused", starter, role(), agent, []string{"@reviewer"}, false, NeedsPost},
		{"starter lets anyone post to all", starter, role(Post), agent, []string{"all"}, false, ""},
		{"recommended needs broadcast for all", recommended, role(Post), agent, []string{"all"}, false, NeedsBroadcast},
		{"recommended allows all with broadcast", recommended, role(Post, Broadcast), agent, []string{"all"}, false, ""},
		{"starter lets anyone send urgent", starter, role(Post), agent, []string{"@reviewer"}, true, ""},
		{"recommended needs urgent permission", recommended, role(Post, Broadcast), agent, []string{"@reviewer"}, true, NeedsUrgent},
		{"humans may always post to all, urgently", recommended, role(), human, []string{"all"}, true, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CheckPost(tt.policy, tt.role, tt.sender, tt.to, tt.urgent); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCanRead(t *testing.T) {
	open, _ := Preset(Starter)
	addressed, _ := Preset(Recommended)
	reviewer := Member{ID: "r", Name: "reviewer", Kind: "agent", Role: "reviewer"}
	tester := Member{ID: "t", Name: "tester", Kind: "agent", Role: "tester"}
	human := Member{ID: "h", Name: "alex", Kind: "human"}
	tests := []struct {
		name   string
		policy Policy
		to     []string
		sender string
		reader Member
		want   bool
	}{
		{"open: anyone reads a direct message", open, []string{"@reviewer"}, "w", tester, true},
		{"addressed: recipient by name reads", addressed, []string{"@reviewer"}, "w", reviewer, true},
		{"addressed: recipient by role reads", addressed, []string{"role:reviewer"}, "w", reviewer, true},
		{"addressed: others don't", addressed, []string{"@reviewer"}, "w", tester, false},
		{"addressed: sender reads own", addressed, []string{"@reviewer"}, "t", tester, true},
		{"addressed: all reaches everyone", addressed, []string{"all"}, "w", tester, true},
		{"addressed: humans read everything", addressed, []string{"@reviewer"}, "w", human, true},
		{"one of several names", addressed, []string{"@a", "@tester"}, "w", tester, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CanRead(tt.policy, tt.to, tt.sender, tt.reader); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicyApply(t *testing.T) {
	starter, _ := Preset(Starter)
	tests := []struct {
		name          string
		change        PolicyChange
		wantVis       string
		wantOverrides []string
		wantErr       bool
	}{
		{"preset replaces everything", PolicyChange{Preset: Recommended}, VisibilityAddressed, []string{}, false},
		{"key on top of preset is an override", PolicyChange{Preset: Recommended, Visibility: VisibilityOpen}, VisibilityOpen, []string{"visibility"}, false},
		{"key alone keeps the preset", PolicyChange{Broadcast: Granted}, VisibilityOpen, []string{"broadcast"}, false},
		{"unknown value is rejected", PolicyChange{Visibility: "secret"}, "", nil, true},
		{"unknown preset is rejected", PolicyChange{Preset: "strict"}, "", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := starter.Apply(tt.change)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if err != nil {
				return
			}
			if got.Visibility != tt.wantVis || !slices.Equal(got.Overrides, tt.wantOverrides) {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestShowHarness(t *testing.T) {
	starter, _ := Preset(Starter)
	if !starter.ShowHarness {
		t.Fatal("the starter preset hides harnesses")
	}
	off := false
	hidden, err := starter.Apply(PolicyChange{ShowHarness: &off})
	if err != nil || hidden.ShowHarness || !slices.Equal(hidden.Overrides, []string{"show_harness"}) {
		t.Fatalf("got %+v, %v", hidden, err)
	}
	if kept, _ := hidden.Apply(PolicyChange{Broadcast: Granted}); kept.ShowHarness {
		t.Fatalf("changing another key showed harnesses again: %+v", kept)
	}
	if reset, _ := hidden.Apply(PolicyChange{Preset: Recommended}); !reset.ShowHarness {
		t.Fatalf("a preset didn't replace show_harness: %+v", reset)
	}
	var old Policy
	if err := json.Unmarshal([]byte(`{"preset":"starter","visibility":"open","broadcast":"everyone","urgent":"everyone","overrides":[]}`), &old); err != nil || !old.ShowHarness {
		t.Fatalf("a policy without show_harness should read as true: %+v, %v", old, err)
	}
}

func TestAgentNameBase(t *testing.T) {
	for _, tt := range []struct{ harness, role, want string }{
		{"claude-code", "writer", "claude"},
		{"codex", "reviewer", "codex"},
		{"OpenCode", "reviewer", "opencode"},
		{"", "reviewer", "reviewer"},
		{"!!!", "reviewer", "reviewer"},
	} {
		if got := AgentNameBase(tt.harness, tt.role); got != tt.want {
			t.Errorf("AgentNameBase(%q, %q) = %q, want %q", tt.harness, tt.role, got, tt.want)
		}
	}
}

func TestAllocateNumberedName(t *testing.T) {
	taken := map[string]bool{"agent-1": true}
	if got := AllocateNumberedName("agent", func(n string) bool { return taken[n] }); got != "agent-2" {
		t.Fatalf("got %s", got)
	}
}

func TestGrantReadsBothForms(t *testing.T) {
	var fromYAML Role
	if err := yaml.Unmarshal([]byte("can: [post, {claim_tasks: [experiment]}]"), &fromYAML); err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(fromYAML)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"can":["post",{"claim_tasks":["experiment"]}]}` {
		t.Fatalf("got %s", out)
	}
	var fromJSON Role
	if err := json.Unmarshal(out, &fromJSON); err != nil {
		t.Fatal(err)
	}
	if !fromJSON.Has(ClaimTasks) || fromJSON.Can[1].TaskTypes[0] != "experiment" {
		t.Fatalf("got %+v", fromJSON)
	}
	var bad Role
	if err := yaml.Unmarshal([]byte("can: [fly]"), &bad); err == nil {
		t.Fatal("unknown permission accepted")
	}
}

func TestAllocateName(t *testing.T) {
	taken := map[string]bool{"reviewer": true, "reviewer-2": true}
	if got := AllocateName("reviewer", func(n string) bool { return taken[n] }); got != "reviewer-3" {
		t.Fatalf("got %s", got)
	}
	if got := AllocateName("writer", func(n string) bool { return taken[n] }); got != "writer" {
		t.Fatalf("got %s", got)
	}
}

func TestNormalizeName(t *testing.T) {
	for in, want := range map[string]string{
		"alex": "alex", "Alex.Smith": "alex-smith", "__x__": "x", "": "human", "José": "jos",
	} {
		if got := NormalizeName(in); got != want {
			t.Errorf("NormalizeName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCanManage(t *testing.T) {
	admin := Member{ID: "m1", Kind: "human", HumanID: "alex", Access: AccessAdmin}
	sam := Member{ID: "m2", Kind: "human", HumanID: "sam", Access: AccessMember}
	priya := Member{ID: "m3", Kind: "human", HumanID: "priya", Access: AccessMember}
	samsWriter := Member{ID: "m4", Kind: "agent", HumanID: "sam", Role: "writer"}
	samsReviewer := Member{ID: "m5", Kind: "agent", HumanID: "sam", Role: "reviewer"}
	alexsAgent := Member{ID: "m6", Kind: "agent", HumanID: "alex", Role: "writer"}
	// An access level on an agent, set by mistake, doesn't make it an admin.
	marked := Member{ID: "m7", Kind: "agent", HumanID: "alex", Access: AccessAdmin}
	tests := []struct {
		name          string
		person, agent Member
		want          bool
	}{
		{"owner", sam, samsWriter, true},
		{"admin, someone else's agent", admin, samsWriter, true},
		{"member, someone else's agent", priya, samsWriter, false},
		{"member, the admin's agent", sam, alexsAgent, false},
		{"agent of the same owner", samsReviewer, samsWriter, false},
		{"agent marked admin", marked, samsWriter, false},
	}
	for _, tt := range tests {
		if got := CanManage(tt.person, tt.agent); got != tt.want {
			t.Errorf("%s: CanManage = %v, want %v", tt.name, got, tt.want)
		}
	}
}
