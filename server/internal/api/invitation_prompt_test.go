package api

import (
	"strings"
	"testing"
)

func TestInvitationPromptTellsOlderClientsToUpgradeBeforeSetup(t *testing.T) {
	t.Parallel()
	for _, pairing := range []bool{false, true} {
		prompt := invitationPrompt("https://team.example/join#invite", "maya", pairing)
		if !strings.Contains(prompt, "Run Aboard outside your agent's sandbox; approve it when your harness asks.") {
			t.Fatalf("prompt omitted sandbox approval guidance: %s", prompt)
		}
		upgrade := "If aboard version is older than 0.1.4 and is not a +dev build, run aboard upgrade first."
		if !strings.Contains(prompt, upgrade) || strings.Index(prompt, upgrade) > strings.Index(prompt, "run aboard setup") {
			t.Fatalf("upgrade must precede setup: %s", prompt)
		}
		if !strings.Contains(prompt, "--handle maya.") || strings.Contains(prompt, "Verify you can exchange messages") {
			t.Fatalf("prompt lost its recipient or retained a separate verification instruction: %s", prompt)
		}
	}
}
