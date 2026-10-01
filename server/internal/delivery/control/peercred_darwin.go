//go:build darwin

package control

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// peerUID reads the peer's user with LOCAL_PEERCRED, macOS's way of reporting it.
func peerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, fmt.Errorf("LOCAL_PEERCRED: %w", err)
	}
	return int(cred.Uid), nil
}
