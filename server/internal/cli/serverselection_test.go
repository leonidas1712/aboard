package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestServerExplanationOnlyForAmbiguousUnqualifiedOutput(t *testing.T) {
	const first = "https://first.example"
	const second = "https://second.example"
	for _, tc := range []struct {
		name          string
		many          bool
		chosen, board string
		explain       bool
	}{
		{name: "one known", chosen: first},
		{name: "several known", many: true, chosen: first, explain: true},
		{name: "board already named", many: true, chosen: first, board: "payments-design"},
		{name: "folder overrides default", chosen: second, explain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := lifecycleMachine(t, first, "abh_person", agentCredential{Server: first, Board: "payments-design", Name: "claude", Token: "aba_agent"})
			var output bytes.Buffer
			a := e.app(&output, &bytes.Buffer{})
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			saved := serverLogins{Default: first, Servers: []serverLogin{{URL: first}}}
			if tc.many {
				saved.Servers = append(saved.Servers, serverLogin{URL: second})
			}
			if err := writeJSONFile(p.servers(), saved, 0o600); err != nil {
				t.Fatal(err)
			}
			srv := a.selectedServer(serverRef{URL: tc.chosen}, "project")
			value := map[string]any{"server": srv, "board": tc.board}
			a.emit(value, "Result.\n")
			if got := strings.Contains(output.String(), "Server:"); got != tc.explain {
				t.Fatalf("server explanation=%v, want %v: %s", got, tc.explain, output.String())
			}
			output.Reset()
			a.json = true
			a.emit(value, "Result.\n")
			var decoded map[string]json.RawMessage
			if err := json.Unmarshal(output.Bytes(), &decoded); err != nil {
				t.Fatal(err)
			}
			if len(decoded["server_selection"]) == 0 {
				t.Fatal("JSON lost selection metadata")
			}
		})
	}
}
