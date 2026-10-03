package cli

import (
	"slices"
	"testing"
)

func TestMapJoinServer(t *testing.T) {
	tests := []struct {
		host, localAddr string
		wantURL         string
		wantErr         bool
	}{
		{"localhost", "127.0.0.1:7400", "http://127.0.0.1:7400", false},
		{"localhost:7400", "127.0.0.1:7400", "http://127.0.0.1:7400", false},
		{"localhost:7411", "127.0.0.1:7411", "http://127.0.0.1:7411", false},
		{"127.0.0.1:7411", "127.0.0.1:7411", "http://127.0.0.1:7411", false},
		{"localhost", "127.0.0.1:7411", "", true},
		{"localhost:7412", "127.0.0.1:7411", "", true},
		{"a.example.com", "127.0.0.1:7400", "", true},
		{"a.example.com:7400", "127.0.0.1:7400", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.host+" on "+tt.localAddr, func(t *testing.T) {
			got, err := mapJoinServer(tt.host, tt.localAddr)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				if e := asError(err); e.Code != "server_unknown" {
					t.Errorf("code = %q, want server_unknown", e.Code)
				}
				return
			}
			if got.Name != "local" || got.URL != tt.wantURL {
				t.Errorf("got %+v, want local at %s", got, tt.wantURL)
			}
		})
	}
}

func TestResolveAgent(t *testing.T) {
	const srv = "http://127.0.0.1:7400"
	creds := credentials{Agents: []agentCredential{
		{Server: srv, Board: "docs", Name: "writer", Token: "t1"},
		{Server: srv, Board: "docs", Name: "reviewer", Token: "t2"},
		{Server: srv, Board: "other", Name: "planner", Token: "t3"},
		{Server: "http://127.0.0.1:9999", Board: "docs", Name: "ghost", Token: "t4"},
	}}
	docs := target{server: serverRef{Name: "local", URL: srv}, board: "docs"}
	tests := []struct {
		name        string
		as, env     string
		t           target
		wantToken   string
		wantChoices []string
	}{
		{"flag wins over env", "writer", "reviewer", docs, "t1", nil},
		{"env when no flag", "", "reviewer", docs, "t2", nil},
		{"leading @ is accepted", "@writer", "", docs, "t1", nil},
		{"nothing selected lists this board's agents", "", "", docs, "", []string{"reviewer", "writer"}},
		{"unknown name lists choices", "planner", "", docs, "", []string{"reviewer", "writer"}},
		{"board without agents has no choices", "", "", target{server: docs.server, board: "empty"}, "", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveAgent(tt.as, tt.env, creds, tt.t)
			if tt.wantChoices == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Token != tt.wantToken {
					t.Errorf("token = %q, want %q", got.Token, tt.wantToken)
				}
				return
			}
			e := asError(err)
			if e.Code != "agent_not_selected" || e.exitCode() != exitError {
				t.Fatalf("got code %q exit %d, want agent_not_selected exit 1", e.Code, e.exitCode())
			}
			choices, ok := e.Details["choices"].([]string)
			if !ok || !slices.Equal(choices, tt.wantChoices) {
				t.Errorf("choices = %v, want %v", e.Details["choices"], tt.wantChoices)
			}
		})
	}
}

func TestResolvePaths(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	tests := []struct {
		name    string
		vars    map[string]string
		want    paths
		wantErr bool
	}{
		{
			"home defaults",
			map[string]string{"HOME": "/h"},
			paths{config: "/h/.config/aboard", data: "/h/.local/share/aboard", state: "/h/.local/state/aboard"},
			false,
		},
		{
			"xdg variables win",
			map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/c", "XDG_DATA_HOME": "/d", "XDG_STATE_HOME": "/s"},
			paths{config: "/c/aboard", data: "/d/aboard", state: "/s/aboard"},
			false,
		},
		{
			"relative xdg variables are ignored",
			map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "rel"},
			paths{config: "/h/.config/aboard", data: "/h/.local/share/aboard", state: "/h/.local/state/aboard"},
			false,
		},
		{
			"ABOARD_HOME moves everything and wins over xdg",
			map[string]string{"HOME": "/h", "XDG_CONFIG_HOME": "/c", "ABOARD_HOME": "/sb/aboard"},
			paths{home: "/sb/aboard", config: "/sb/aboard/config", data: "/sb/aboard/data", state: "/sb/aboard/state"},
			false,
		},
		{
			"ABOARD_HOME needs no HOME",
			map[string]string{"ABOARD_HOME": "/sb/aboard/"},
			paths{home: "/sb/aboard", config: "/sb/aboard/config", data: "/sb/aboard/data", state: "/sb/aboard/state"},
			false,
		},
		{"relative ABOARD_HOME", map[string]string{"HOME": "/h", "ABOARD_HOME": "rel"}, paths{}, true},
		{"no home", map[string]string{}, paths{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolvePaths(env(tt.vars))
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
