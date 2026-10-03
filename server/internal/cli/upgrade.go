package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// upgradeLockTimeout is how long a command waits while another one replaces the
// delivery daemon or the local server.
const upgradeLockTimeout = 30 * time.Second

// replaceAttempts bounds how often a command replaces an older daemon that an older
// hook keeps starting again.
const replaceAttempts = 3

// replaceOutdatedDaemon replaces a running delivery daemon from an older build with
// this binary's, once per command. The journal is on disk, so the new daemon carries on
// where the old one stopped. Any failure leaves the old daemon in use: the command still
// works with it, and aboard doctor reports it.
func (a *app) replaceOutdatedDaemon(ctx context.Context, p paths) {
	if a.daemonChecked {
		return
	}
	a.daemonChecked = true
	if _, _, ok := a.sandboxed(); ok {
		return // a daemon started here would inherit the sandbox
	}
	own := currentBuild()
	outdated := func() int {
		st, _ := a.daemonStatus(ctx)
		if st == nil || compareBuilds(st.Build, own) >= 0 {
			return 0
		}
		return st.PID
	}
	if outdated() == 0 {
		// A daemon being stopped for a replacement no longer answers, so it looks like
		// no daemon at all; wait for the new one rather than talk to the old one.
		awaitUpgrade(ctx, p)
		return
	}
	unlock, err := lockUpgrade(ctx, p)
	if err != nil {
		return
	}
	defer unlock()
	for range replaceAttempts {
		pid := outdated() // another command may have replaced it while this one waited
		if pid == 0 {
			return
		}
		if stopAboard(ctx, pid, func() bool { return lockFree(p.daemonLock()) }) != nil {
			return
		}
		if a.startDaemon(p) != nil {
			return
		}
		c, err := a.waitDaemon(ctx, p)
		if err != nil {
			return
		}
		_ = c.Close()
	}
}

// replaceOutdatedLocal replaces a running local server from an older build with this
// binary's, once per command. The database is on disk, so nothing is lost. It returns
// an error only when it stopped the old server and couldn't start the new one.
func (a *app) replaceOutdatedLocal(ctx context.Context) error {
	if a.localChecked {
		return nil
	}
	a.localChecked = true
	p, err := a.paths()
	if err != nil {
		return nil //nolint:nilerr // the command reports a bad path itself
	}
	outdated := func() bool {
		info, err := a.localInfo(ctx)
		return err == nil && info.Mode == api.Local && compareBuilds(infoBuild(info), currentBuild()) < 0
	}
	if !outdated() {
		// A server being stopped for a replacement no longer answers; wait for the new
		// one rather than start a server of this command's own.
		awaitUpgrade(ctx, p)
		return nil
	}
	unlock, err := lockUpgrade(ctx, p)
	if err != nil {
		return nil //nolint:nilerr // the older server still answers; use it
	}
	defer unlock()
	if !outdated() {
		return nil
	}
	if stopAboard(ctx, localPID(p), func() bool { return !a.localRunning(ctx) }) != nil {
		return nil // not a server this machine started; use it as it is
	}
	return a.startLocalAndWait(ctx)
}

// dataNewer reports data written by a newer aboard than this one.
func dataNewer(path string, err error) *Error {
	return &Error{
		Code:    "data_newer",
		Message: "The data in " + path + " was written by a newer aboard than this one, aboard " + version + ".",
		Hint:    "Install the newer aboard again; this one refuses to run rather than misread the data.",
		Err:     err,
	}
}

// localInfo asks the local server for its identity and build.
func (a *app) localInfo(ctx context.Context) (*api.ServerInfo, error) {
	c, err := a.newClient(a.localServer(), "", time.Second)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return c.info(ctx)
}

// lockUpgrade takes the machine's upgrade lock, so of several commands that find an
// older daemon or server at once, only one replaces it. The returned function releases
// the lock.
func lockUpgrade(ctx context.Context, p paths) (func(), error) {
	if err := os.MkdirAll(p.state, 0o700); err != nil {
		return nil, fmt.Errorf("create state directory %s: %w", p.state, err)
	}
	path := upgradeLock(p)
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	deadline := time.Now().Add(upgradeLockTimeout)
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { _ = f.Close() }, nil // closing the file releases the lock
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) || time.Now().After(deadline) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, fmt.Errorf("wait for %s: %w", path, ctx.Err())
		case <-tick.C:
		}
	}
}

// awaitUpgrade waits until no other command is replacing the daemon or the local
// server.
func awaitUpgrade(ctx context.Context, p paths) {
	if lockFree(upgradeLock(p)) {
		return
	}
	if unlock, err := lockUpgrade(ctx, p); err == nil {
		unlock()
	}
}

func upgradeLock(p paths) string { return filepath.Join(p.state, "upgrade.lock") }

// lockFree reports whether nobody holds the lock file at path: the daemon that held it
// has exited.
func lockFree(path string) bool {
	f, err := os.OpenFile(filepath.Clean(path), os.O_RDWR, 0o600)
	if err != nil {
		return errors.Is(err, os.ErrNotExist)
	}
	defer func() { _ = f.Close() }()
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
}
