package proctable

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// lookup reads /proc/<pid>/stat. The start time is in clock ticks since boot.
func lookup(pid int) (info, error) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if errors.Is(err, fs.ErrNotExist) {
		return info{}, errGone
	}
	if err != nil {
		return info{}, fmt.Errorf("read process %d: %w", pid, err)
	}
	// The name is in parentheses and may hold spaces or parentheses itself.
	s := string(raw)
	open, closing := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
	if open < 0 || closing < open {
		return info{}, fmt.Errorf("read process %d: unexpected stat %q", pid, s)
	}
	fields := strings.Fields(s[closing+1:])
	// fields[0] is the state, fields[1] the parent, fields[19] the start time.
	if len(fields) < 20 {
		return info{}, fmt.Errorf("read process %d: unexpected stat %q", pid, s)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return info{}, fmt.Errorf("read process %d: parent: %w", pid, err)
	}
	start, err := strconv.ParseInt(fields[19], 10, 64)
	if err != nil {
		return info{}, fmt.Errorf("read process %d: start time: %w", pid, err)
	}
	state := fields[0]
	return info{ppid: ppid, name: s[open+1 : closing], start: start, exited: state == "Z" || state == "X"}, nil
}
