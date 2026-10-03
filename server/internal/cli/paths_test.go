package cli

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeAddrIsPickedOnceAndKept(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{"ABOARD_HOME": home}
	first := (&app{env: Env{Getenv: func(k string) string { return env[k] }}}).localAddr()
	host, port, err := net.SplitHostPort(first)
	if err != nil || host != "127.0.0.1" || port == "0" || first == "127.0.0.1:7400" {
		t.Fatalf("picked address %q", first)
	}
	raw, err := os.ReadFile(filepath.Clean(filepath.Join(home, "data", "server.addr")))
	if err != nil || string(raw) != first+"\n" {
		t.Fatalf("recorded address %q, %v; want %q", raw, err, first)
	}
	if again := (&app{env: Env{Getenv: func(k string) string { return env[k] }}}).localAddr(); again != first {
		t.Fatalf("second command got %q, want the recorded %q", again, first)
	}
	env["ABOARD_LOCAL_ADDR"] = "127.0.0.1:9999"
	if got := (&app{env: Env{Getenv: func(k string) string { return env[k] }}}).localAddr(); got != "127.0.0.1:9999" {
		t.Fatalf("ABOARD_LOCAL_ADDR: got %q", got)
	}
}

func TestHooksNameAboardHome(t *testing.T) {
	env := map[string]string{"ABOARD_HOME": "/sb/my home"}
	a := &app{env: Env{Getenv: func(k string) string { return env[k] }}}
	for _, h := range a.withHome(hooksOf(t, "codex", "/bin/aboard")) {
		if !strings.HasPrefix(h.Handler.Command, "ABOARD_HOME='/sb/my home' /bin/aboard hook codex ") || !isAboardHook(h.Handler.Command, "codex", h.Arg) {
			t.Fatalf("hook command %q", h.Handler.Command)
		}
	}
	delete(env, "ABOARD_HOME")
	if got := a.withHome(hooksOf(t, "claude-code", "/bin/aboard"))[0].Handler.Command; got != "/bin/aboard hook claude-code session-start" {
		t.Fatalf("without ABOARD_HOME: %q", got)
	}
}
