//go:build e2e

package e2e

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// With only ABOARD_AGENT set, outside any harness session, a command that is up to a
// person refuses with human_command_in_session before it reads the person's key or
// sends anything: ABOARD_AGENT is how an agent's environment says who it is.
// Board add now selects that agent; without its saved seat it refuses locally too.
func TestPersonCommandsRefuseUnderABOARDAGENT(t *testing.T) {
	t.Parallel()
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		http.Error(w, "no request should arrive", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	e := newEnv(t)
	project := `{"server":{"name":"` + srv.URL + `","url":"` + srv.URL + `"},"board":"general"}`
	if err := os.WriteFile(filepath.Join(e.dir, ".aboard"), []byte(project), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"board", "policy", "recommended"},
		{"board", "add", "@maya"},
		{"board", "remove", "@maya"},
		{"board", "leave"},
		{"board", "owner", "@maya"},
		{"board", "visibility", "open", "--yes"},
		{"board", "visibility", "private"},
		{"invite"},
		{"invite", "--guest", "sam"},
		{"invite", "--server"},
		{"people"},
		{"people", "role", "@maya", "admin"},
		{"people", "remove", "@maya", "--yes"},
		{"keys"},
		{"keys", "create", "phone"},
		{"logout", "--browsers"},
		{"watch"},
		{"delivery", "humans"},
		{"delivery", "off", "--as", "scout"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := e.exec([]string{"ABOARD_AGENT=scout"}, "", append(args, "--json")...)
			want := "human_command_in_session"
			if args[0] == "board" && args[1] == "add" {
				want = "agent_not_selected"
			}
			if r.code != 1 || errorCode(t, r.json(t)) != want {
				t.Fatalf("%v with ABOARD_AGENT set:\n%s", args, r)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}

// With only ABOARD_AGENT set, changing that agent's delivery mode is refused and leaves
// the mode as it was.
func TestDeliveryModeUnderABOARDAGENTIsRefused(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	e.run("pair", "writer-reviewer", "--name", "writer")
	r := e.exec([]string{"ABOARD_AGENT=writer"}, "", "delivery", "humans", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" ||
		!strings.Contains(field(t, r.json(t), "error.hint").(string), "aboard delivery humans --as writer") {
		t.Fatalf("delivery humans with ABOARD_AGENT set:\n%s", r)
	}
	if got := field(t, e.run("delivery", "--as", "writer", "--json").json(t), "mode"); got != "focused" {
		t.Fatalf("mode after the refused change = %v, want focused", got)
	}
}
