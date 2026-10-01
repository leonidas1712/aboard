//go:build linux

package control

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// peerUID reads the peer's user with SO_PEERCRED, Linux's way of reporting it.
func peerUID(fd int) (int, error) {
	cred, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, fmt.Errorf("SO_PEERCRED: %w", err)
	}
	return int(cred.Uid), nil
}
