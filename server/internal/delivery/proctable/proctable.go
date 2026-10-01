// Package proctable reads the operating system's process table: it finds the harness
// process a hook or command runs under, and tells the delivery daemon whether a
// session's harness still runs.
package proctable

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Table is the delivery daemon's Processes port, backed by the process table.
type Table struct{}

var _ delivery.Processes = Table{}

// errGone means there is no running process with that id.
var errGone = errors.New("no such process")

// info is what the process table says about one process.
type info struct {
	ppid  int
	name  string
	start int64
	// exited is true for a process that has exited but not yet been reaped.
	exited bool
}

// Alive reports whether p still runs. A process that can't be read for another reason
// counts as alive, so a session is never closed on a guess.
func (Table) Alive(p delivery.Process) bool {
	in, err := lookup(p.PID)
	if errors.Is(err, errGone) {
		return false
	}
	if err != nil {
		return true
	}
	return !in.exited && in.start == p.Start
}

// passThrough are programs that sit between a harness and the commands it runs: shells,
// and wrappers that start one command. The harness is the first ancestor not among them.
var passThrough = map[string]bool{
	"sh": true, "bash": true, "zsh": true, "dash": true, "fish": true, "ksh": true, "mksh": true,
	"tcsh": true, "csh": true, "env": true, "nohup": true, "timeout": true, "time": true,
	"script": true, "xargs": true, "aboard": true,
}

// maxDepth bounds the walk up the process tree.
const maxDepth = 16

// Harness returns the process the current process runs under: its nearest ancestor that
// isn't a shell or a wrapper. ok is false when there is none, or the process table
// can't be read here.
func Harness() (p delivery.Process, ok bool) {
	pid := os.Getppid()
	for range maxDepth {
		if pid <= 1 {
			return delivery.Process{}, false
		}
		in, err := lookup(pid)
		if err != nil {
			return delivery.Process{}, false
		}
		if !passThrough[baseName(in.name)] {
			return delivery.Process{PID: pid, Start: in.start}, true
		}
		pid = in.ppid
	}
	return delivery.Process{}, false
}

// baseName is a program's name without its directory or a login shell's leading dash.
func baseName(name string) string {
	return strings.TrimPrefix(filepath.Base(name), "-")
}

// Name returns the program name of a running process, or ok false when there is no
// such process or it can't be read.
func Name(pid int) (name string, ok bool) {
	in, err := lookup(pid)
	if err != nil || in.exited {
		return "", false
	}
	return baseName(in.name), true
}
