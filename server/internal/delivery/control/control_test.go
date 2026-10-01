package control

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

func listen(t *testing.T) (l *Listener, path string) {
	t.Helper()
	path = Path(filepath.Join(t.TempDir(), "state"))
	l, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })
	return l, path
}

func dial(path string) (net.Conn, error) {
	return Dial(context.Background(), path)
}

func TestControlSocketContract(t *testing.T) {
	deliverytest.RunControl(t, deliverytest.ControlFixture{
		New: func(t *testing.T) (delivery.Control, func() (net.Conn, error)) {
			l, path := listen(t)
			return l, func() (net.Conn, error) { return dial(path) }
		},
	})
}

// The kernel reports this process as the peer of its own connection. This runs on
// every system CI covers, through that system's own call.
func TestPeerUIDIsThisUser(t *testing.T) {
	l, path := listen(t)
	client, err := dial(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	c, err := l.l.AcceptUnix()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	uid, err := PeerUID(c)
	if err != nil {
		t.Fatalf("PeerUID: %v", err)
	}
	if uid != os.Getuid() {
		t.Fatalf("peer uid %d, want %d", uid, os.Getuid())
	}
	if err := SelfTest(); err != nil {
		t.Fatalf("SelfTest: %v", err)
	}
}

func TestAllowFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		got     int
		readErr error
		allowed bool
	}{
		{"same user", 501, nil, true},
		{"another user", 0, nil, false},
		{"peer unreadable", 501, errors.New("getsockopt: operation not supported"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Allow(501, tt.got, tt.readErr)
			if (err == nil) != tt.allowed {
				t.Fatalf("Allow = %v, allowed %v", err, tt.allowed)
			}
			if err != nil && !errors.Is(err, ErrPeerRefused) {
				t.Fatalf("refusal %v isn't ErrPeerRefused", err)
			}
		})
	}
}

func TestListenerClosesConnectionsFromOtherUsers(t *testing.T) {
	for name, peer := range map[string]func(*net.UnixConn) (int, error){
		"another user":    func(*net.UnixConn) (int, error) { return os.Getuid() + 1, nil },
		"peer unreadable": func(*net.UnixConn) (int, error) { return 0, errors.New("not reported") },
	} {
		t.Run(name, func(t *testing.T) {
			l, path := listen(t)
			l.peer = peer
			client, err := dial(path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			if c, err := l.Accept(); !errors.Is(err, ErrPeerRefused) {
				t.Fatalf("Accept = %v, %v; want ErrPeerRefused", c, err)
			}
			if _, err := client.Read(make([]byte, 1)); err == nil {
				t.Fatal("the refused connection is still open")
			}
		})
	}
}

func TestSocketIsPrivate(t *testing.T) {
	_, path := listen(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("socket mode %o is open to others", perm)
	}
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("socket directory mode %o, want 700", perm)
	}
}

func TestCheckDirRefusesADirectoryOthersCanOpen(t *testing.T) {
	private := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(private, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(private); err != nil {
		t.Fatalf("private directory refused: %v", err)
	}
	// Readable by the group: enough to refuse it.
	open := filepath.Join(t.TempDir(), "open")
	if err := os.Mkdir(open, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := CheckDir(open); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("CheckDir(0750) = %v, want ErrUnsafe", err)
	}
	if _, err := Listen(filepath.Join(open, SocketName)); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("Listen in an open directory = %v, want ErrUnsafe", err)
	}
}

func TestPathFitsInASocketAddress(t *testing.T) {
	short := "/home/a/.local/state/aboard"
	if got := Path(short); got != short+"/"+SocketName {
		t.Fatalf("Path(%q) = %q", short, got)
	}
	long := "/" + strings.Repeat("d", 120) + "/.local/state/aboard"
	got := Path(long)
	if len(got) > maxSocketPath || !strings.HasPrefix(got, "/tmp/aboard-") {
		t.Fatalf("Path(long) = %q", got)
	}
	if Path(long) != got || Path(long+"x") == got {
		t.Fatal("the short path must be the same for one state directory and differ between two")
	}
}
