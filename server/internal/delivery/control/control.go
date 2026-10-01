// Package control is the delivery daemon's control socket: a Unix socket in a directory
// only its owner can open, which accepts a connection only when the kernel reports that
// the peer runs as the same OS user. When the peer's user can't be read, the connection
// is refused. It implements the delivery package's Control port.
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// maxSocketPath is the longest socket path every supported system accepts; macOS allows
// 104 bytes including the terminating zero.
const maxSocketPath = 100

// SocketName is the socket's file name in the state directory.
const SocketName = "daemon.sock"

// Path returns where the daemon's socket lives for a state directory: in the directory
// itself, or, when that path is too long for a socket, in a directory of this user's
// own under /tmp named after a hash of the state directory.
func Path(stateDir string) string {
	p := filepath.Join(stateDir, SocketName)
	if len(p) <= maxSocketPath {
		return p
	}
	sum := sha256.Sum256([]byte(stateDir))
	dir := "/tmp/aboard-" + strconv.Itoa(os.Getuid())
	return filepath.Join(dir, hex.EncodeToString(sum[:8])+".sock")
}

// ErrUnsafe means the socket's directory can be opened by other users or belongs to
// someone else.
var ErrUnsafe = errors.New("socket directory is not private")

// PrepareDir creates the socket's directory with mode 0700 if needed and checks that it
// belongs to this user and that nobody else can open it.
func PrepareDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	return CheckDir(dir)
}

// CheckDir checks that dir belongs to this user and is closed to everyone else.
func CheckDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("check %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrUnsafe, dir)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %o", ErrUnsafe, dir, info.Mode().Perm())
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%w: %s belongs to user %d", ErrUnsafe, dir, st.Uid)
	}
	return nil
}

// Listener accepts control connections from this OS user only.
type Listener struct {
	l    *net.UnixListener
	path string
	uid  int
	// peer reads a connection's peer user; replaced in tests.
	peer func(*net.UnixConn) (int, error)
}

var _ delivery.Control = (*Listener)(nil)

// Listen creates the socket at path, replacing a stale one, readable only by its owner.
// The caller must hold the daemon lock, so no live daemon's socket is removed.
func Listen(path string) (*Listener, error) {
	if err := PrepareDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket %s: %w", path, err)
	}
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("set permissions on %s: %w", path, err)
	}
	return &Listener{l: l, path: path, uid: os.Getuid(), peer: PeerUID}, nil
}

// ErrPeerRefused means a connection came from another user, or its user couldn't be read.
var ErrPeerRefused = errors.New("control connection refused")

// Accept returns the next connection. A connection whose peer isn't this user is
// closed, and Accept returns ErrPeerRefused for it.
func (l *Listener) Accept() (net.Conn, error) {
	c, err := l.l.AcceptUnix()
	if err != nil {
		return nil, fmt.Errorf("accept control connection: %w", err)
	}
	uid, err := l.peer(c)
	if err := Allow(l.uid, uid, err); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// Allow decides whether a peer may talk to the daemon: only when its user was read and
// matches. It fails closed.
func Allow(want, got int, readErr error) error {
	if readErr != nil {
		return fmt.Errorf("%w: can't read the peer's user: %w", ErrPeerRefused, readErr)
	}
	if got != want {
		return fmt.Errorf("%w: peer runs as user %d, not %d", ErrPeerRefused, got, want)
	}
	return nil
}

// Close stops listening and removes the socket.
func (l *Listener) Close() error {
	err := l.l.Close()
	_ = os.Remove(l.path)
	if err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("close control socket: %w", err)
	}
	return nil
}

// Dial connects to the daemon's socket.
func Dial(ctx context.Context, path string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return nil, fmt.Errorf("connect to the delivery daemon at %s: %w", path, err)
	}
	return c, nil
}

// PeerUID reads the user id of the process on the other end of c from the kernel.
func PeerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("read socket: %w", err)
	}
	var uid int
	var credErr error
	if err := raw.Control(func(fd uintptr) { uid, credErr = peerUID(int(fd)) }); err != nil {
		return 0, fmt.Errorf("read socket: %w", err)
	}
	return uid, credErr
}

// SelfTest checks that the kernel reports a socket peer's user on this system, by
// reading it over a connected pair of sockets.
func SelfTest() error {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		return fmt.Errorf("create a socket pair: %w", err)
	}
	f0, f1 := os.NewFile(uintptr(fds[0]), "peer-test-0"), os.NewFile(uintptr(fds[1]), "peer-test-1")
	defer func() { _ = f0.Close(); _ = f1.Close() }()
	c, err := net.FileConn(f0)
	if err != nil {
		return fmt.Errorf("open socket: %w", err)
	}
	defer func() { _ = c.Close() }()
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return errors.New("not a unix socket")
	}
	uid, err := PeerUID(uc)
	return Allow(os.Getuid(), uid, err)
}
