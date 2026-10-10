package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestApprovalOutputUsesServerLabelAndPlainInviteLink(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	var text bytes.Buffer
	a := e.app(&text, &text)
	srv := serverRef{Name: "work", URL: "https://issuer.example"}
	result := &api.AdminActionResult{State: "executed", Approval: api.Approval{Id: "apr_own"}, Invite: &api.ServerInvite{Invite: "abi_secret", Link: optional("https://issuer.example/join#abi_secret"), Prompt: optional("Server-owned prompt")}}
	if err := emitAdmissionResult(a, srv, "qa", result); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "on work") || !strings.Contains(text.String(), "Invite: https://issuer.example/join#abi_secret") || strings.Contains(text.String(), "Invite: aboard connect") {
		t.Fatalf("output: %s", &text)
	}
	command := labelOnboardingCommand("aboard approvals allow apr_own --server 'https://issuer.example'", srv)
	if command != "aboard approvals allow apr_own --server work" {
		t.Fatal(command)
	}
}

func TestSetupPrintsSkillInstructionOnce(t *testing.T) {
	for _, complete := range []bool{false, true} {
		e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
		var text bytes.Buffer
		a := e.app(&text, &text)
		out := newSetupOutput(serverRef{Name: "work", URL: "https://issuer.example"})
		out.confirmSetupHarness("aboard setup --continue")
		if complete {
			out.Next = nil
		}
		if err := emitSetup(a, out); err != nil {
			t.Fatal(err)
		}
		if strings.Count(text.String(), "Run aboard skill now") != 1 {
			t.Fatalf("output: %s", &text)
		}
	}
}

func TestSetupWaitingTextKeepsDeliveryStatus(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	var text bytes.Buffer
	a := e.app(&text, &text)
	out := newSetupOutput(serverRef{Name: "work", URL: "https://issuer.example"})
	out.Steps[5].Message = "Waiting for a reply from @alex's agents."
	if err := emitSetup(a, out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text.String(), "delivery: pending · Waiting for a reply from @alex's agents.") {
		t.Fatalf("waiting delivery lost its status: %s", &text)
	}
}
