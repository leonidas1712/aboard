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
		{"invite", "--server"},
		{"keys"},
		{"keys", "create", "phone"},
		{"logout", "--browsers"},
		{"watch"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			r := e.exec([]string{"ABOARD_AGENT=scout"}, "", append(args, "--json")...)
			if r.code != 1 || errorCode(t, r.json(t)) != "human_command_in_session" {
				t.Fatalf("%v with ABOARD_AGENT set:\n%s", args, r)
			}
		})
	}
	if n := hits.Load(); n != 0 {
		t.Fatalf("%d requests reached the server", n)
	}
}
