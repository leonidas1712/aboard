package cli

import (
	"bytes"
	"context"
	"testing"
)

func TestAgentCannotChangeMidturnPolicyOrReadAPersonKey(t *testing.T) {
	for _, args := range [][]string{{"midturn", "owner-only"}, {"midturn", "my-agents", "--as", "scout"}, {"midturn", "--inherit", "--as", "scout"}} {
		t.Run(args[1], func(t *testing.T) {
			home := t.TempDir()
			var out bytes.Buffer
			a := &app{env: Env{Dir: home, Stdout: &out, Stderr: &out, Getenv: func(k string) string {
				return map[string]string{"HOME": home, "ABOARD_HOME": home, "ABOARD_AGENT": "scout"}[k]
			}}}
			if err := runDelivery(context.Background(), a, args); asError(err).Code != "human_command_in_session" {
				t.Fatalf("agent setting must refuse before reading person state: %v", err)
			}
		})
	}
}
