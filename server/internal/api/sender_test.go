package api_test

import (
	"context"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// joinAs adds an agent for the human with this token, as role on boardName, and returns
// the agent's name and token.
func (s *testServer) joinAs(token, boardName, role, harness string) (name, agentToken string) {
	s.t.Helper()
	req := api.JoinRequest{Board: &boardName, Role: &role}
	if harness != "" {
		req.Harness = &harness
	}
	r, err := s.client(token).JoinWithResponse(context.Background(), nil, req)
	mustStatus(s.t, r, err, 201)
	return r.JSON201.Agent.Name, r.JSON201.Token
}

func (s *testServer) say(token, boardName, body string) api.Message {
	s.t.Helper()
	r, err := s.client(token).PostMessageWithResponse(context.Background(), boardName, nil, api.PostMessageRequest{Body: body})
	mustStatus(s.t, r, err, 201)
	return *r.JSON201
}

// timeline returns the board's messages as the holder of token reads them, by body.
func (s *testServer) timeline(token, boardName string) map[string]api.Message {
	s.t.Helper()
	r, err := s.client(token).ListMessagesWithResponse(context.Background(), boardName, nil)
	mustStatus(s.t, r, err, 200)
	out := map[string]api.Message{}
	for _, m := range r.JSON200.Messages {
		out[m.Body] = m
	}
	return out
}

// newBoard creates a writer-reviewer board with no agents yet.
func (s *testServer) newBoard() string {
	s.t.Helper()
	tpl := "writer-reviewer"
	b, err := s.client(s.owner).CreateBoardWithResponse(context.Background(), nil, api.CreateBoardRequest{Template: &tpl})
	mustStatus(s.t, b, err, 201)
	return b.JSON201.Name
}

func TestAgentsAreNamedAfterTheirHarness(t *testing.T) {
	s := newTestServer(t)
	boardName := s.newBoard()
	for _, tt := range []struct{ harness, want string }{
		{"claude-code", "claude"},
		{"claude-code", "claude-2"},
		{"codex", "codex"},
		{"", "reviewer"},
		{"", "reviewer-2"},
	} {
		if name, _ := s.joinAs(s.owner, boardName, "reviewer", tt.harness); name != tt.want {
			t.Errorf("an agent with harness %q is called %q, want %q", tt.harness, name, tt.want)
		}
	}
}

func TestSenderLabelSaysWhoseSideTheSenderIsOn(t *testing.T) {
	s := newTestServer(t)
	boardName, writer, reviewer := s.pair("starter")

	// One owner: owners aren't worth showing yet.
	if first := s.say(writer, boardName, "before priya"); first.ShowOwner || first.Sender != "self" {
		t.Fatalf("the writer's own new message: sender %q, show_owner %v", first.Sender, first.ShowOwner)
	}

	priya := s.addHuman("priya")
	harness := "codex"
	j := s.joinBoard(priya, boardName, "reviewer", &harness)
	codex := j.JSON201.Token

	s.say(s.owner, boardName, "from alex")
	s.say(writer, boardName, "from writer")
	s.say(codex, boardName, "from codex")
	s.say(priya, boardName, "from priya")

	readers := []struct {
		name, token string
		want        map[string]api.MessageSender
	}{
		{"alex's reviewer", reviewer, map[string]api.MessageSender{
			"from alex": "owner", "from writer": "owner_agent", "from codex": "other_agent", "from priya": "other_person",
		}},
		{"alex", s.owner, map[string]api.MessageSender{
			"from alex": "self", "from writer": "owner_agent", "from codex": "other_agent", "from priya": "other_person",
		}},
		{"priya's codex", codex, map[string]api.MessageSender{
			"from alex": "other_person", "from writer": "other_agent", "from codex": "self", "from priya": "owner",
		}},
	}
	for _, r := range readers {
		got := s.timeline(r.token, boardName)
		for body, want := range r.want {
			m := got[body]
			if m.Sender != want {
				t.Errorf("%s reads %q with sender %q, want %q", r.name, body, m.Sender, want)
			}
			if !m.ShowOwner {
				t.Errorf("%s reads %q without show_owner, though two people have agents here", r.name, body)
			}
		}
	}

	got := s.timeline(reviewer, boardName)
	if h := got["from codex"].From.Harness; h == nil || *h != "codex" {
		t.Errorf("from.harness of the codex agent's message: %v", h)
	}
	// The older trust field keeps its meaning for clients that still read it.
	if got["from writer"].Trust != "peer" || got["from priya"].Trust != "human" || got["from alex"].Trust != "owner" {
		t.Errorf("trust: writer %q, priya %q, alex %q", got["from writer"].Trust, got["from priya"].Trust, got["from alex"].Trust)
	}
}

func TestHidingHarnessesNamesAgentsNeutrally(t *testing.T) {
	s := newTestServer(t)
	ctx := context.Background()
	human := s.client(s.owner)
	boardName := s.newBoard()

	hide := false
	u, err := human.UpdateBoardWithResponse(ctx, boardName, nil, api.UpdateBoardRequest{Policy: &api.PolicyChange{ShowHarness: &hide}})
	mustStatus(t, u, err, 200)
	if p := u.JSON200.Policy; p.ShowHarness == nil || *p.ShowHarness || p.Overrides == nil || len(*p.Overrides) != 1 || (*p.Overrides)[0] != "show_harness" {
		t.Fatalf("policy after hiding harnesses: %+v", p)
	}

	first, claude := s.joinAs(s.owner, boardName, "writer", "claude-code")
	second, codex := s.joinAs(s.owner, boardName, "reviewer", "codex")
	if first != "agent-1" || second != "agent-2" {
		t.Fatalf("agents are called %q and %q, want agent-1 and agent-2", first, second)
	}
	if named, _ := s.joinAs(s.owner, boardName, "reviewer", ""); named != "agent-3" {
		t.Fatalf("an agent without a harness is called %q, want agent-3", named)
	}

	if posted := s.say(claude, boardName, "hello"); posted.From.Harness != nil {
		t.Errorf("an agent sees its own harness in its post: %q", *posted.From.Harness)
	}
	if h := s.timeline(codex, boardName)["hello"].From.Harness; h != nil {
		t.Errorf("an agent sees another agent's harness: %q", *h)
	}
	if h := s.timeline(s.owner, boardName)["hello"].From.Harness; h == nil || *h != "claude-code" {
		t.Errorf("a person doesn't see the harness: %v", h)
	}
	for _, c := range []struct {
		who, token string
		visible    bool
	}{{"an agent", codex, false}, {"a person", s.owner, true}} {
		ms, err := s.client(c.token).ListMembersWithResponse(ctx, boardName)
		mustStatus(t, ms, err, 200)
		for _, m := range ms.JSON200.Members {
			if m.Name == "agent-1" && (m.Harness != nil) != c.visible {
				t.Errorf("%s listing members sees agent-1's harness: %v, want visible %v", c.who, m.Harness, c.visible)
			}
		}
	}
}
