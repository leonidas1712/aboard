//go:build !linux && !darwin

package proctable

import "errors"

// lookup can't read the process table on this system, so no harness is ever found and
// no session is closed for its process.
func lookup(int) (info, error) {
	return info{}, errors.New("reading the process table isn't supported on this system")
}
