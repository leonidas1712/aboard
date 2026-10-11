//go:build live

package live

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCodexSessionHomeIgnoresTheDaemonsHome(t *testing.T) {
	only(t, "codex")
	setup := requireCodex(t)
	parallel(t)
	l := newLab(t)
	l.codexHome(setup)
	oldDaemon := l.daemonPID()
	if err := syscall.Kill(oldDaemon, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	l.waitFor(15*time.Second, "the original daemon to stop", func() bool { return syscall.Kill(oldDaemon, 0) != nil })
	log, err := os.OpenFile(filepath.Join(l.stateDir(), "daemon.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = log.Close() }()
	cmd := command(t.Context(), l.bin, "daemon")
	cmd.Env = slices.DeleteFunc(slices.Clone(l.vars), func(value string) bool { return strings.HasPrefix(value, "CODEX_HOME=") })
	cmd.Env = append(cmd.Env, "CODEX_HOME="+filepath.Join(l.dir, "bogus-codex-home"))
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	socket := filepath.Join(l.stateDir(), "daemon.sock")
	if len(socket) > 100 {
		sum := sha256.Sum256([]byte(l.stateDir()))
		socket = fmt.Sprintf("/tmp/aboard-%d/%x.sock", os.Getuid(), sum[:8])
	}
	l.waitFor(15*time.Second, "the bogus-home daemon socket", func() bool {
		conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "unix", socket)
		if err != nil {
			return false
		}
		_ = conn.Close()
		return l.daemonPID() == cmd.Process.Pid
	})
	board := l.pairCLI()
	p := l.startCodex("home-agent", l.project("project", "codex"))
	p.submit(fmt.Sprintf("Run `aboard join --board %s --name home-agent` now. After that, when an Aboard message arrives, do exactly what it asks and stop. Reply only OK after joining.", board))
	p.waitIdle(3 * time.Minute)
	p.d.afterBind(p)
	l.waitPresence("home-agent", "idle", 30*time.Second)
	ping := l.say("writer", "--to", "@home-agent", "--expect-reply", "Reply to this message with exactly HOME-JOIN-1.")
	l.waitMessage("home-agent", ping.At, "HOME-JOIN-1", 3*time.Minute)
	p.waitIdle(time.Minute)
	d := p.d
	l.provesReconnect(p, "codex", "home-agent", "writer", func() {
		p.quit()
		d.stopBackground(l)
	}, func(id string) {
		p.respawn(d.env(l), d.resumeArgv(l, id))
		d.ready(p)
	})
}
