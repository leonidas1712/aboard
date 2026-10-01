package proctable

import (
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// zombie is the state of a process that has exited but not yet been reaped.
const zombie = 5 // SZOMB

// lookup asks the kernel about the process. The start time is in microseconds since the
// Unix epoch.
func lookup(pid int) (info, error) {
	k, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	// The kernel answers with nothing, which reads as EIO, for a process that doesn't exist.
	if errors.Is(err, unix.EIO) || errors.Is(err, unix.ESRCH) || (err == nil && int(k.Proc.P_pid) != pid) {
		return info{}, errGone
	}
	if err != nil {
		return info{}, fmt.Errorf("read process %d: %w", pid, err)
	}
	name := k.Proc.P_comm[:]
	for i, b := range name {
		if b == 0 {
			name = name[:i]
			break
		}
	}
	start := k.Proc.P_starttime.Sec*1_000_000 + int64(k.Proc.P_starttime.Usec)
	return info{ppid: int(k.Eproc.Ppid), name: string(name), start: start, exited: k.Proc.P_stat == zombie}, nil
}
