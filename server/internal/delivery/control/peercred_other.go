//go:build !darwin && !linux

package control

import "errors"

// peerUID refuses every peer: on this system Aboard has no way to read a socket peer's
// user, and a check that can't run must not pass.
func peerUID(int) (int, error) {
	return 0, errors.New("this system doesn't report a socket peer's user to Aboard")
}
