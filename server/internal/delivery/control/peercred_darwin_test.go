//go:build darwin

package control

import (
	"os"
	"syscall"
	"testing"
)

// LOCAL_PEERCRED reports this process's user for one end of a socket pair. CI runs this
// on macOS; Linux runs its own SO_PEERCRED test.
func TestLocalPeerCredReportsThisUser(t *testing.T) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Close(fds[0]); _ = syscall.Close(fds[1]) }()
	uid, err := peerUID(fds[0])
	if err != nil {
		t.Fatalf("LOCAL_PEERCRED: %v", err)
	}
	if uid != os.Getuid() {
		t.Fatalf("peer uid %d, want %d", uid, os.Getuid())
	}
}
