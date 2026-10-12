package proctable

import (
	"bytes"
	"encoding/binary"

	"golang.org/x/sys/unix"
)

// Args reads only argv; the environment following it is never returned.
func Args(pid int) ([]string, bool) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(raw) < 4 {
		return nil, false
	}
	return processArgs(raw)
}

func processArgs(raw []byte) ([]string, bool) {
	if len(raw) < 4 {
		return nil, false
	}
	count := int(binary.NativeEndian.Uint32(raw[:4]))
	if count < 1 || count > len(raw) {
		return nil, false
	}
	raw = raw[4:]
	end := bytes.IndexByte(raw, 0)
	if end < 0 {
		return nil, false
	}
	raw = bytes.TrimLeft(raw[end+1:], "\x00")
	args := make([]string, 0, count)
	for range count {
		end = bytes.IndexByte(raw, 0)
		if end < 0 {
			return nil, false
		}
		args = append(args, string(raw[:end]))
		raw = raw[end+1:]
	}
	return args, true
}
