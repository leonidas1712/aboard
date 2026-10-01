package cli

import (
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// starterNotice is printed wherever a board on the starter policy is created or joined.
const starterNotice = "Starter policy: every member reads everything. Before adding more agents or people, run: aboard board policy recommended"

// policyNotice is the PolicyNotice JSON shape.
type policyNotice struct {
	Preset         string `json:"preset"`
	Message        string `json:"message"`
	TightenCommand string `json:"tighten_command"`
}

// noticeFor returns the starter policy notice, or nil when the board isn't on starter.
func noticeFor(p api.Policy) *policyNotice {
	if p.Preset != "starter" {
		return nil
	}
	return &policyNotice{Preset: "starter", Message: starterNotice, TightenCommand: "aboard board policy recommended"}
}

// agentUse is the AgentUse JSON shape: how to act as an agent in later commands.
type agentUse struct {
	As           string  `json:"as"`
	Env          string  `json:"env"`
	BoundSession *string `json:"bound_session"`
}

func useFor(name string) agentUse {
	return agentUse{As: name, Env: "ABOARD_AGENT=" + name}
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// targetsText joins a message's targets for display, showing "all" when there are none.
func targetsText(to []string) string {
	if len(to) == 0 {
		return "all"
	}
	return strings.Join(to, ", ")
}
