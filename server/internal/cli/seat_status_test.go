package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
)

func TestStatusUsesTheSelectedSeatIdForServerModeAndPresence(t *testing.T) {
	for _, id := range []string{"mem_old", "mem_replacement"} {
		t.Run(id, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/info":
					_ = json.NewEncoder(w).Encode(map[string]string{"version": version, "mode": "team", "name": "team", "server_id": "srv_test"})
				case "/v1/boards/docs":
					_ = json.NewEncoder(w).Encode(api.Board{Name: "docs"})
				case "/v1/boards/docs/members":
					presence, mode, applied := api.MemberPresenceWorking, api.MemberDeliveryModeAll, api.MemberDeliveryAll
					_ = json.NewEncoder(w).Encode(map[string]any{"members": []api.Member{{Id: id, Name: "writer", Kind: api.MemberKindAgent, Presence: &presence, DeliveryMode: &mode, Delivery: &applied}}})
				default:
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer srv.Close()
			a := seatApp(t)
			var out bytes.Buffer
			a.env.Stdout, a.env.Stderr = &out, &out
			a.env.Dir = t.TempDir()
			a.env.Executable = os.Executable
			if err := a.writeProject(projectFile{Server: serverRef{URL: srv.URL}, Board: "docs"}); err != nil {
				t.Fatal(err)
			}
			if err := a.saveCredential(agentCredential{Server: srv.URL, Board: "docs", Name: "writer", MemberID: "mem_old", Token: "aba_old"}); err != nil {
				t.Fatal(err)
			}
			p, err := a.paths()
			if err != nil {
				t.Fatal(err)
			}
			if err := writeJSONFile(p.servers(), serverLogins{Servers: []serverLogin{{URL: srv.URL, Handle: "owner", Key: "abh_owner"}}}, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := runStatus(t.Context(), a, []string{"--as", "writer", "--json"}); err != nil {
				t.Fatal(err)
			}
			var got struct {
				Agent    string  `json:"agent"`
				Delivery string  `json:"delivery"`
				Applied  *string `json:"delivery_applied"`
				Presence *string `json:"presence"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("status output: %q: %v", out.String(), err)
			}
			if got.Agent != "writer" {
				t.Fatalf("selected agent: %+v", got)
			}
			if id == "mem_old" {
				if got.Delivery != "all" || deref(got.Applied) != "all" || deref(got.Presence) != "working" {
					t.Fatalf("matching seat metadata missing: %+v", got)
				}
			} else if got.Delivery != "focused" || got.Applied != nil || got.Presence != nil {
				t.Fatalf("replacement seat metadata shown as the old seat: %+v", got)
			}
		})
	}
}
