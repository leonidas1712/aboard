package proctable

import "github.com/leonidas1712/aboard/server/internal/delivery"

// Lookup returns pid as a Process, for tests.
func Lookup(pid int) (delivery.Process, bool) {
	in, err := lookup(pid)
	if err != nil {
		return delivery.Process{}, false
	}
	return delivery.Process{PID: pid, Start: in.start}, true
}
