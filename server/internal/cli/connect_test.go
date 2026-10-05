package cli

import (
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// A key is read only for the exact server that issued it, and a redirect from that
// server never carries the key anywhere else.
func TestAKeyGoesOnlyToItsServer(t *testing.T) {
	var mu sync.Mutex
	var leaked []string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		leaked = append(leaked, r.Header.Get("Authorization"))
	}))
	defer elsewhere.Close()
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/boards/docs", http.StatusTemporaryRedirect)
	}))
	defer issuer.Close()

	home := t.TempDir()
	a := &app{env: Env{Rand: rand.Reader, Getenv: func(k string) string {
		switch k {
		case "ABOARD_HOME":
			return home
		case "ABOARD_LOCAL_ADDR":
			return "127.0.0.1:1"
		}
		return ""
	}}}
	p, err := a.paths()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONFile(p.servers(), serverLogins{Servers: []serverLogin{{URL: issuer.URL, Handle: "maya", Key: "abh_maya"}}}, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := a.readOwnerToken(serverRef{URL: elsewhere.URL}); asError(err).Code != "login_required" {
		t.Fatalf("the key for another server: %v", err)
	}
	if _, err := a.readOwnerToken(serverRef{URL: issuer.URL + "/"}); asError(err).Code != "login_required" {
		t.Fatalf("the key for a URL that isn't exactly the issuer's: %v", err)
	}
	key, err := a.readOwnerToken(serverRef{URL: issuer.URL})
	if err != nil || key != "abh_maya" {
		t.Fatalf("the issuer's key: %q %v", key, err)
	}
	c, err := a.newClient(serverRef{URL: issuer.URL}, key, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.board(t.Context(), "docs"); err == nil {
		t.Fatal("a redirect was followed as if it were an answer")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(leaked) != 0 {
		t.Fatalf("the redirect was followed, sending %q elsewhere", leaked)
	}
}

func TestParseInviteLink(t *testing.T) {
	tests := []struct {
		link, wantURL, wantCode string
	}{
		{"https://team.example.com/join#abi_K8s2", "https://team.example.com", ""},
		{"http://127.0.0.1:7411/join#abi_K8s2", "http://127.0.0.1:7411", ""},
		{"http://localhost:7411/join/#abi_K8s2", "http://localhost:7411", ""},
		{"http://team.example.com/join#abi_K8s2", "", "insecure_server"},
		{"https://team.example.com/join?x=1#abi_K8s2", "", "invite_link_invalid"},
		{"https://team.example.com/other#abi_K8s2", "", "invite_link_invalid"},
		{"https://maya@team.example.com/join#abi_K8s2", "", "invite_link_invalid"},
		{"https://team.example.com/join#abl_K8s2", "", "invite_link_invalid"},
		{"abi_K8s2", "", "invite_link_invalid"},
	}
	for _, tt := range tests {
		srv, invite, err := parseInviteLink(tt.link)
		if tt.wantCode != "" {
			if asError(err).Code != tt.wantCode {
				t.Errorf("%s: error %v, want %s", tt.link, err, tt.wantCode)
			}
			continue
		}
		if err != nil || srv.URL != tt.wantURL || invite != "abi_K8s2" {
			t.Errorf("%s: %+v %q %v", tt.link, srv, invite, err)
		}
	}
}

func TestJoinLinesNameConnectedServers(t *testing.T) {
	connected := []serverLogin{{URL: "http://127.0.0.1:7411"}, {URL: "https://team.example.com"}}
	for _, tt := range []struct{ host, want string }{
		{"localhost:7411", "http://127.0.0.1:7411"},
		{"127.0.0.1:7411", "http://127.0.0.1:7411"},
		{"team.example.com", "https://team.example.com"},
		{"team.example.com:443", "https://team.example.com"},
		{"localhost", "http://127.0.0.1:7400"},
		{"team.example.com:7411", ""},
		{"other.example.com", ""},
	} {
		got, err := mapJoinServer(tt.host, "127.0.0.1:7400", connected)
		if tt.want == "" {
			if asError(err).Code != "server_unknown" {
				t.Errorf("%s: %+v %v, want server_unknown", tt.host, got, err)
			}
			continue
		}
		if err != nil || got.URL != tt.want {
			t.Errorf("%s: %+v %v, want %s", tt.host, got, err, tt.want)
		}
	}
}
