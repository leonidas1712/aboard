package cli

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/rules"
	"github.com/leonidas1712/aboard/server/internal/server"
	"github.com/leonidas1712/aboard/server/internal/store/sqlite"
)

// startTimeout is how long a background local server may take to answer.
const startTimeout = 10 * time.Second

// localRunning reports whether a server answers at the local address.
func (a *app) localRunning(ctx context.Context) bool {
	return a.serverAnswers(ctx, a.localServer())
}

// serverAnswers reports whether a server answers at srv.
func (a *app) serverAnswers(ctx context.Context, srv serverRef) bool {
	c, err := a.newClient(srv, "", time.Second)
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	_, err = c.info(ctx)
	return err == nil
}

// ensureLocal starts the local server in the background unless it is already running.
// It reports whether it started it.
func (a *app) ensureLocal(ctx context.Context) (bool, error) {
	if err := a.replaceOutdatedLocal(ctx); err != nil {
		return false, err
	}
	if a.localRunning(ctx) {
		return false, nil
	}
	// A server started inside a sandbox that blocks the network couldn't listen, and one
	// may well be running outside it.
	if harness, ok := a.networkBlocked(); ok {
		return false, sandboxBlocksNetwork(harness, "the local Aboard server at "+a.localServer().URL)
	}
	return true, a.startLocalAndWait(ctx)
}

// startLocalAndWait starts the local server in the background and waits until it
// answers.
func (a *app) startLocalAndWait(ctx context.Context) error {
	p, err := a.paths()
	if err != nil {
		return err
	}
	if err := a.startLocal(p); err != nil {
		return err
	}
	deadline := time.Now().Add(startTimeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for !a.localRunning(ctx) {
		if time.Now().After(deadline) {
			return newError("server_not_running",
				fmt.Sprintf("The local server didn't start within %s.", startTimeout),
				"Look at the server log at "+p.serverLog()+" for the reason.")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the local server: %w", ctx.Err())
		case <-tick.C:
		}
	}
	return nil
}

// startLocal runs "aboard serve" as a detached process that outlives this command,
// with its output appended to the server log.
func (a *app) startLocal(p paths) error {
	exe, err := a.env.Executable()
	if err != nil {
		return fmt.Errorf("find the aboard binary to start the local server: %w", err)
	}
	if err := os.MkdirAll(p.data, 0o700); err != nil {
		return fmt.Errorf("create data directory %s: %w", p.data, err)
	}
	logFile, err := os.OpenFile(p.serverLog(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open server log %s: %w", p.serverLog(), err)
	}
	defer func() { _ = logFile.Close() }()
	// The server must keep running after this command exits, so it is not tied to a
	// context and gets a session of its own.
	cmd := &exec.Cmd{
		Path:        exe,
		Args:        []string{exe, "serve"},
		Stdout:      logFile,
		Stderr:      logFile,
		SysProcAttr: &syscall.SysProcAttr{Setsid: true},
	}
	if err := cmd.Start(); err != nil {
		return &Error{
			Code: "server_not_running", Message: "Couldn't start the local server.",
			Hint: "Run aboard serve in a terminal to see why.", Err: err,
		}
	}
	if err := cmd.Process.Release(); err != nil {
		return fmt.Errorf("detach the local server process: %w", err)
	}
	return nil
}

// localPID reads the running local server's process id.
func localPID(p paths) int {
	data, err := os.ReadFile(filepath.Clean(p.pidFile()))
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	return pid
}

// runServe runs the local server in the foreground until interrupted.
func runServe(ctx context.Context, a *app, args []string) error {
	fs := a.flags("serve")
	team := fs.Bool("team", false, "run a team server")
	publicURL := fs.String("public-url", "", "the https address people use")
	data := fs.String("data", "", "the folder for the database, files and backups")
	listen := fs.String("listen", "", "the address to listen on")
	admin := fs.String("admin", "", "the first admin's handle")
	if _, err := a.parse(fs, args, usageOf("serve"), 0, 0); err != nil {
		return err
	}
	if *team {
		return a.serveTeam(ctx, teamFlags{publicURL: *publicURL, data: *data, listen: *listen, admin: *admin})
	}
	if *publicURL != "" || *data != "" || *listen != "" || *admin != "" {
		return usageError("--public-url, --data, --listen and --admin are for a team server: add --team.", usageOf("serve"))
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	owner := a.env.Getenv("USER")
	if owner == "" {
		owner = "human"
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	b := currentBuild()
	err = server.Run(ctx, server.Options{
		Addr:           a.localAddr(),
		DataDir:        p.data,
		OwnerName:      owner,
		MachineName:    machineName(),
		OwnerTokenPath: p.ownerToken(),
		Version:        b.Version,
		Commit:         b.Commit,
		CommitTime:     b.CommitTime,
		Log:            slog.New(slog.NewJSONHandler(a.env.Stderr, nil)),
	})
	if errors.Is(err, sqlite.ErrNewerSchema) {
		return dataNewer(p.data, err)
	}
	if err != nil {
		hint := "If another program uses the address, set ABOARD_LOCAL_ADDR to a free one."
		if p.home != "" && a.env.Getenv("ABOARD_LOCAL_ADDR") == "" {
			hint = "If another program uses the address, delete " + p.addrFile() +
				" so the next start picks a free port, or set ABOARD_LOCAL_ADDR to a free one."
		}
		return &Error{Code: "server_not_running", Message: "The local server stopped: " + err.Error(), Hint: hint, Err: err}
	}
	return nil
}

// teamFlags are aboard serve --team's flags as given; empty when not given.
type teamFlags struct{ publicURL, data, listen, admin string }

// defaultTeamListen is where a team server listens unless told otherwise: every
// address, since its proxy reaches it from outside its container or machine.
const defaultTeamListen = "0.0.0.0:7400"

// serveTeam runs a team server in the foreground until interrupted (D199). Each flag not
// given comes from its ABOARD_ variable, so a container can be configured by its
// environment.
func (a *app) serveTeam(ctx context.Context, f teamFlags) error {
	pick := func(flagValue, name, def string) string {
		if flagValue != "" {
			return flagValue
		}
		if v := strings.TrimSpace(a.env.Getenv(name)); v != "" {
			return v
		}
		return def
	}
	rawURL := pick(f.publicURL, "ABOARD_PUBLIC_URL", "")
	data := pick(f.data, "ABOARD_DATA", "")
	listen := pick(f.listen, "ABOARD_LISTEN", defaultTeamListen)
	admin := pick(f.admin, "ABOARD_ADMIN", "admin")
	use := usageOf("serve")
	if rawURL == "" {
		return usageError("A team server needs its public URL: give --public-url or set ABOARD_PUBLIC_URL.", use)
	}
	pub, err := server.ParsePublicURL(rawURL)
	if err != nil {
		return usageError("The public URL "+err.Error()+".", use)
	}
	if data == "" {
		return usageError("A team server needs a folder for its data: give --data or set ABOARD_DATA.", use)
	}
	if !filepath.IsAbs(data) {
		return usageError("The data folder "+data+" is not an absolute path.", use)
	}
	if _, _, err := net.SplitHostPort(listen); err != nil {
		return usageError("The listen address "+listen+" is not a host and port, such as 0.0.0.0:7400.", use)
	}
	if h := rules.NormalizeName(admin); h == "" || h != strings.ToLower(admin) {
		return usageError("The first admin's handle "+admin+" can't be a handle: use lowercase letters, digits and dashes.", use)
	}
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	b := currentBuild()
	err = server.Run(ctx, server.Options{
		Addr: listen, DataDir: data, Version: b.Version, Commit: b.Commit, CommitTime: b.CommitTime,
		Log:  slog.New(slog.NewJSONHandler(a.env.Stderr, nil)),
		Team: &server.Team{PublicURL: pub, AdminName: admin},
	})
	if errors.Is(err, sqlite.ErrNewerSchema) {
		return dataNewer(data, err)
	}
	if err != nil {
		return &Error{
			Code: "server_not_running", Message: "The team server stopped: " + err.Error(),
			Hint: "If another program uses " + listen + ", set --listen or ABOARD_LISTEN to a free address.", Err: err,
		}
	}
	return nil
}

// machineName is this machine's name without its domain, such as "maya-laptop" for
// maya-laptop.local, or "machine" when the system doesn't say. Access keys made here are
// named after it.
func machineName() string {
	host, err := os.Hostname()
	host, _, _ = strings.Cut(host, ".")
	if err != nil || host == "" {
		return "machine"
	}
	return rules.NormalizeName(host)
}

// runUp starts the local server in the background.
func runUp(ctx context.Context, a *app, args []string) error {
	use := usageOf("up")
	fs := a.flags("up")
	if _, err := a.parse(fs, args, use, 0, 0); err != nil {
		return err
	}
	started, err := a.ensureLocal(ctx)
	if err != nil {
		return err
	}
	p, err := a.paths()
	if err != nil {
		return err
	}
	srv := a.localServer()
	url := a.out().code(srv.URL)
	text := "Local Aboard is already running at " + url + "\n"
	switch r := a.localReplaced; {
	case started:
		text = "Started local Aboard at " + url + "\n"
	case r != nil:
		text = "Replaced local Aboard at " + url + ": it ran aboard " + buildLabel(r.From) + ", an older build\n"
	}
	a.emit(map[string]any{
		"server": srv, "started": started, "replaced": a.localReplaced, "pid": localPID(p),
		"ui_url": srv.URL + "/", "data_dir": p.data, "version": version,
	}, text)
	return nil
}
