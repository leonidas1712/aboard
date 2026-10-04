//go:build live

package live

import (
	"os"
	"strings"
	"testing"
)

// A lab never reaches into the person's own home folder: after Aboard's setup for every
// harness on this machine, from the person's folder and from a project, aboard doctor
// names only the lab's folders. Every other doctor run in the suite is checked the same
// way (lab.exec). It needs no harness login and spends no model turn.
func TestLabStaysOutOfYourHome(t *testing.T) {
	parallel(t)
	l := newLab(t)
	l.run("init", "--yes", "--allow-commands")
	proj := l.project("proj", "codex")
	for _, dir := range []string{l.human, proj} {
		r := l.exec(t.Context(), dir, "doctor", "--json")
		if !strings.Contains(r.stdout, l.home()) {
			t.Errorf("doctor in %s names nothing in the lab's home folder %s, so it can't show where it looked:\n%s", dir, l.home(), r)
		}
	}
	if _, err := os.Stat(l.home()); err != nil {
		t.Fatalf("the lab's home folder: %v", err)
	}
}

// realHomePaths lists the paths in text under the person's real home folder, leaving out
// the lab's own directory (which can sit inside it when TMPDIR does) and the harnesses'
// own programs, which the lab runs from the person's PATH.
func (l *lab) realHomePaths(text string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	text = strings.ReplaceAll(text, l.dir, "<lab>")
	var found []string
	for i := strings.Index(text, home+"/"); i >= 0; i = strings.Index(text, home+"/") {
		end := strings.IndexAny(text[i:], " \"'\n\t,)")
		if end < 0 {
			end = len(text) - i
		}
		path := text[i : i+end]
		if !onPath(path) {
			found = append(found, path)
		}
		text = text[i+end:]
	}
	return found
}

// onPath reports whether path is a program in a folder on the PATH the suite runs with.
func onPath(path string) bool {
	for _, dir := range strings.Split(os.Getenv("PATH"), string(os.PathListSeparator)) {
		if dir != "" && strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/") && !strings.Contains(path[len(dir)+1:], "/") {
			return true
		}
	}
	return false
}
