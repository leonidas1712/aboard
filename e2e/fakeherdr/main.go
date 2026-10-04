// Command fakeherdr plays herdr for the tests of Aboard's herdr launcher: "fakeherdr
// --session <name> server" serves the part of herdr's socket API the launcher uses
// (ping, workspace.list, workspace.create, layout.apply, pane.list, pane.get, pane.close,
// server.stop), on the socket herdr would use for that session, and runs each pane's
// command as a real process. As in herdr, a pane whose process exits is removed, and a
// workspace closes with its last pane. Shapes follow herdr's own API schema (herdr api
// schema --json).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"
)

func main() {
	args := os.Args[1:]
	if len(args) == 3 && args[0] == "--session" && args[2] == "server" {
		serve(args[1])
		return
	}
	if len(args) == 1 && args[0] == "--version" {
		fmt.Println("herdr 0.9.3 (fake)")
		return
	}
	fmt.Fprintln(os.Stderr, "fakeherdr: only --session <name> server is played")
	os.Exit(2)
}

type pane struct {
	id, label, workspace string
	cmd                  *exec.Cmd
	done                 chan struct{}
}

type server struct {
	mu         sync.Mutex
	next       int
	workspaces map[string]string // id -> label
	panes      map[string]*pane
	stop       chan struct{}
}

func serve(session string) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(os.Getenv("HOME"), ".config")
	}
	dir := filepath.Join(base, "herdr", "sessions", session)
	if err := os.MkdirAll(dir, 0o700); err != nil { //nolint:gosec // herdr's session folder under the test's config folder
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	sock := filepath.Join(dir, "herdr.sock")
	_ = os.Remove(sock) //nolint:gosec // the socket this fake serves
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", sock)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error: herdr server is already running:", err)
		os.Exit(1)
	}
	s := &server{workspaces: map[string]string{}, panes: map[string]*pane{}, stop: make(chan struct{})}
	go func() {
		<-s.stop
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			break
		}
		go s.handle(conn)
	}
	s.mu.Lock()
	all := make([]*pane, 0, len(s.panes))
	for _, p := range s.panes {
		all = append(all, p)
	}
	s.mu.Unlock()
	for _, p := range all {
		kill(p)
	}
	_ = os.Remove(sock) //nolint:gosec // the socket this fake served
}

type request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func (s *server) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return
	}
	var req request
	if json.Unmarshal(line, &req) != nil {
		return
	}
	result, code, msg := s.do(req)
	out := map[string]any{"id": req.ID}
	if code != "" {
		out["error"] = map[string]string{"code": code, "message": msg}
	} else {
		out["result"] = result
	}
	raw, _ := json.Marshal(out)
	_, _ = conn.Write(append(raw, '\n'))
	if req.Method == "server.stop" && code == "" {
		close(s.stop)
	}
}

func (s *server) paneInfo(p *pane) map[string]any {
	return map[string]any{"pane_id": p.id, "terminal_id": "t" + p.id, "workspace_id": p.workspace, "tab_id": p.workspace + ":t", "focused": false, "agent_status": "unknown", "revision": 0, "label": p.label}
}

func (s *server) do(req request) (result map[string]any, code, message string) {
	switch req.Method {
	case "ping":
		return map[string]any{"type": "pong"}, "", ""
	case "server.stop":
		return map[string]any{"type": "ok"}, "", ""
	case "workspace.list":
		s.mu.Lock()
		defer s.mu.Unlock()
		ids := make([]string, 0, len(s.workspaces))
		for id := range s.workspaces {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		var list []map[string]any
		for _, id := range ids {
			list = append(list, map[string]any{"workspace_id": id, "label": s.workspaces[id]})
		}
		return map[string]any{"type": "workspace_list", "workspaces": list}, "", ""
	case "workspace.create":
		var p struct {
			Cwd   string `json:"cwd"`
			Label string `json:"label"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		s.next++
		ws := fmt.Sprintf("w%d", s.next)
		s.workspaces[ws] = p.Label
		s.mu.Unlock()
		root, err := s.spawn(ws, "", []string{"/bin/sh", "-c", "while :; do sleep 1; done"}, p.Cwd, nil)
		if err != nil {
			return nil, "spawn_failed", err.Error()
		}
		return map[string]any{"type": "workspace_created", "workspace": map[string]any{"workspace_id": ws, "label": p.Label}, "tab": map[string]any{"tab_id": ws + ":t"}, "root_pane": s.paneInfo(root)}, "", ""
	case "layout.apply":
		var p struct {
			WorkspaceID string `json:"workspace_id"`
			Root        struct {
				Type    string            `json:"type"`
				Label   string            `json:"label"`
				Cwd     string            `json:"cwd"`
				Command []string          `json:"command"`
				Env     map[string]string `json:"env"`
			} `json:"root"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Root.Type != "pane" || len(p.Root.Command) == 0 {
			return nil, "invalid_params", "the fake plays one pane with a command"
		}
		s.mu.Lock()
		_, ok := s.workspaces[p.WorkspaceID]
		s.mu.Unlock()
		if !ok {
			return nil, "workspace_not_found", "no such workspace"
		}
		np, err := s.spawn(p.WorkspaceID, p.Root.Label, p.Root.Command, p.Root.Cwd, p.Root.Env)
		if err != nil {
			return nil, "spawn_failed", err.Error()
		}
		return map[string]any{"type": "layout_apply", "layout": map[string]any{"workspace_id": p.WorkspaceID, "tab_id": p.WorkspaceID + ":t", "zoomed": false, "focused_pane_id": np.id, "root": map[string]any{"type": "pane", "pane_id": np.id, "label": np.label}}}, "", ""
	case "pane.list":
		s.mu.Lock()
		defer s.mu.Unlock()
		var list []map[string]any
		for _, p := range s.panes {
			list = append(list, s.paneInfo(p))
		}
		return map[string]any{"type": "pane_list", "panes": list}, "", ""
	case "pane.get", "pane.close":
		var p struct {
			PaneID string `json:"pane_id"`
		}
		_ = json.Unmarshal(req.Params, &p)
		s.mu.Lock()
		found := s.panes[p.PaneID]
		s.mu.Unlock()
		if found == nil {
			return nil, "pane_not_found", "pane " + p.PaneID + " not found"
		}
		if req.Method == "pane.get" {
			return map[string]any{"type": "pane_info", "pane": s.paneInfo(found)}, "", ""
		}
		kill(found)
		return map[string]any{"type": "ok"}, "", ""
	}
	return nil, "unknown_method", "the fake doesn't play " + req.Method
}

// spawn runs a pane's command: herdr's environment, with the pane's own on top.
func (s *server) spawn(ws, label string, argv []string, cwd string, env map[string]string) (*pane, error) {
	cmd := exec.CommandContext(context.Background(), argv[0], argv[1:]...) //nolint:gosec // the command the test asked for
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.next++
	p := &pane{id: fmt.Sprintf("%s:p%d", ws, s.next), label: label, workspace: ws, cmd: cmd, done: make(chan struct{})}
	s.panes[p.id] = p
	s.mu.Unlock()
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		delete(s.panes, p.id)
		last := true
		for _, other := range s.panes {
			if other.workspace == ws {
				last = false
			}
		}
		if last {
			delete(s.workspaces, ws)
		}
		s.mu.Unlock()
		close(p.done)
	}()
	return p, nil
}

// kill ends a pane's processes as herdr does: hang up, terminate, then kill.
func kill(p *pane) {
	for _, sig := range []syscall.Signal{syscall.SIGHUP, syscall.SIGTERM, syscall.SIGKILL} {
		_ = syscall.Kill(-p.cmd.Process.Pid, sig)
		select {
		case <-p.done:
			return
		case <-time.After(250 * time.Millisecond):
		}
	}
	<-p.done
}
