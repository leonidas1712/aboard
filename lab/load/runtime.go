package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type (
	machine struct {
		home, state, dir, key, handle string
		env                           []string
		cmd                           *exec.Cmd
		log                           *os.File
		seats                         []*seat
		heads                         *headLog
		done                          chan struct{}
		exitCode                      int
	}
	board struct {
		ID, Name string
		Head     int64
	}
	seat struct {
		Server   string `json:"server"`
		MemberID string `json:"member_id"`
		Board    string `json:"board"`
		Name     string `json:"name"`
		Token    string `json:"token"`
		owner    *machine
		board    *board
		session  string
		ext      *extension
		observer *http.Client
	}
	fixture struct {
		root, binary, url string
		admin             *machine
		people            []*machine
		boards            []*board
		client            *http.Client
		ctx               context.Context
		workers           sync.WaitGroup
		throttles         atomic.Int64
	}
	headLog struct {
		mu      sync.Mutex
		heads   []observation
		changed chan struct{}
		err     error
	}
	frame struct {
		Event   string `json:"event"`
		ID      int64  `json:"id"`
		Handoff string `json:"handoff_id"`
		Bundle  string `json:"bundle"`
		Error   *struct {
			Code string `json:"code"`
		} `json:"error"`
		at time.Time
	}
	extension struct {
		mu       sync.Mutex
		observed []frame
		done     chan struct{}
		conn     net.Conn
		frames   chan frame
		err      chan error
	}
	sample struct {
		key     messageKey
		started time.Time
		marker  string
	}
)

func run(ctx context.Context, o options) (r report, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	r = report{People: o.People, Agents: o.Agents, Boards: o.Boards, Status: "incomplete", Stage: "topology"}
	if o.People < 1 || o.Agents < 1 || o.Boards < 1 || o.Rounds < 1 || o.Boards > o.People*o.Agents {
		return r, errors.New("positive topology and rounds required; each board needs a seat")
	}
	root, err := os.MkdirTemp("/tmp", "aboard-load-")
	if err != nil {
		return r, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	f := &fixture{root: root, binary: o.Binary, ctx: ctx, client: &http.Client{Timeout: 65 * time.Second, Transport: loadTransport((&net.Dialer{}).DialContext, 16)}}
	progress := runProgress{Stage: "build"}
	defer func() { f.finishReport(&r, progress, err); cancel(); f.close() }()
	if f.binary == "" {
		f.binary = filepath.Join(root, "aboard")
		// #nosec G204 -- builds a fixed repository command into its private scratch directory.
		build := exec.CommandContext(ctx, "go", "build", "-o", f.binary, "./server/cmd/aboard")
		build.Env = cleanEnv(root, "")
		if output, err := build.CombinedOutput(); err != nil {
			return r, fmt.Errorf("build aboard: %w (%d output bytes)", err, len(output))
		}
	}
	f.binary, err = filepath.Abs(f.binary)
	if err != nil {
		return r, err
	}
	start := time.Now()
	progress.SetupStart = start
	progress.Stage = "setup"
	if err := f.setup(o); err != nil {
		return r, err
	}
	r.Setup = time.Since(start).Seconds()
	r.Daemons = len(f.people)
	checks := deliveryCheck{Expected: map[string][]messageKey{}, Seen: map[string][]messageKey{}}
	start = time.Now()
	progress.MeasurementStart = start
	progress.Stage = "round"
	for round := range o.Rounds {
		if err := f.round(ctx, round, &checks, &progress.Stream, &progress.Poll, &progress.Handover, &progress.Write, &r); err != nil {
			return r, err
		}
	}
	r.Measurement = time.Since(start).Seconds()
	r.Throughput = float64(r.Posts) / r.Measurement
	r.Throttles = int(f.throttles.Load())
	progress.Stage = "confirmation"
	if err := f.finishExtensions(o.Rounds); err != nil {
		return r, err
	}
	if err := checks.validate(); err != nil {
		return r, err
	}
	for _, p := range f.people {
		for _, s := range p.seats {
			if err := f.awaitAck(s, checks.Expected[s.MemberID][len(checks.Expected[s.MemberID])-1].Seq); err != nil {
				return r, err
			}
		}
	}
	progress.Stage = "audit"
	for _, b := range f.boards {
		if err := f.cli(f.admin, "", "audit", "verify", "--board", b.Name, "--json"); err != nil {
			return r, fmt.Errorf("chain verification: %w", err)
		}
		r.VerifiedChains++
	}
	wantStream := 0
	for _, person := range f.people {
		boards := map[string]bool{}
		for _, seat := range person.seats {
			boards[seat.board.ID] = true
		}
		wantStream += len(boards) * o.Rounds
	}
	if r.Posts != o.Boards*o.Rounds || r.Deliveries != o.People*o.Agents*o.Rounds || len(progress.Poll) != r.Deliveries || len(progress.Handover) != r.Deliveries || len(progress.Stream) != wantStream {
		return r, errors.New("measurement sample counts do not match the topology")
	}
	if r.Stream, err = summarize(progress.Stream); err != nil {
		return r, err
	}
	if r.LongPoll, err = summarize(progress.Poll); err != nil {
		return r, err
	}
	r.Handover, err = summarize(progress.Handover)
	return r, err
}

func cleanEnv(home, addr string) []string {
	var env []string
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if key == "HOME" || strings.HasPrefix(key, "XDG_") || strings.HasPrefix(key, "ABOARD") || strings.HasPrefix(key, "CLAUDE") || strings.HasPrefix(key, "CODEX") || strings.HasPrefix(key, "OMP") || strings.HasPrefix(key, "PI_") || strings.HasPrefix(key, "TMUX") || strings.HasPrefix(key, "ORCA") || key == "TERM_PROGRAM" {
			continue
		}
		env = append(env, v)
	}
	return append(env, "HOME="+home, "ABOARD_HOME="+filepath.Join(home, "aboard"), "ABOARD_LOCAL_ADDR="+addr, "ABOARD_NO_UPDATE_CHECK=1", "USER=load-proof", "ABOARD_EXIT_WITH_PID="+strconv.Itoa(os.Getpid()))
}

func (f *fixture) machine(name, addr string) (*machine, error) {
	home := filepath.Join(f.root, name)
	dir := filepath.Join(home, "work")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &machine{home: home, state: filepath.Join(home, "aboard", "state"), dir: dir, handle: name, env: cleanEnv(home, addr)}, nil
}

func (f *fixture) cli(m *machine, input string, args ...string) error {
	// #nosec G204 -- the operator selects the binary; arguments are fixed public CLI operations.
	cmd := exec.CommandContext(f.ctx, f.binary, args...)
	cmd.Env = m.env
	cmd.Dir = m.dir
	cmd.Stdin = strings.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("aboard %s failed: %w (%d output bytes withheld)", args[0], err, len(output))
	}
	var data map[string]any
	if err := json.Unmarshal(output, &data); err != nil {
		return fmt.Errorf("aboard %s returned non-JSON output", args[0])
	}
	return nil
}

func (f *fixture) process(m *machine, args ...string) error {
	log, err := os.OpenFile(filepath.Join(m.home, "process.log"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	// #nosec G204 -- the operator selects the binary; arguments are fixed public CLI operations.
	cmd := exec.CommandContext(f.ctx, f.binary, args...)
	cmd.Env = m.env
	cmd.Dir = m.dir
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		_ = log.Close()
		return err
	}
	m.cmd, m.log = cmd, log
	trackProcess(m)
	return nil
}

func (f *fixture) close() {
	for _, p := range f.people {
		for _, s := range p.seats {
			if s.observer != nil {
				s.observer.CloseIdleConnections()
			}
			if s.ext != nil {
				_ = s.ext.conn.Close()
			}
		}
	}
	for _, p := range append(f.people, f.admin) {
		if p != nil && p.cmd != nil {
			_ = p.cmd.Process.Kill()
			<-p.done
			if p.log != nil {
				_ = p.log.Close()
			}
		}
	}
	f.client.CloseIdleConnections()
	f.workers.Wait()
}

func (f *fixture) api(ctx context.Context, method, path, token string, body any) (map[string]any, error) {
	return f.apiWithClient(ctx, f.client, method, path, token, body)
}

func (f *fixture) apiWithClient(ctx context.Context, client *http.Client, method, path, token string, body any) (map[string]any, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	for {
		req, err := http.NewRequestWithContext(ctx, method, f.url+path, bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if method != "GET" {
			req.Header.Set("Idempotency-Key", fmt.Sprintf("load-%x", sha256.Sum256(append([]byte(path+token), raw...))))
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		data := map[string]any{}
		err = json.NewDecoder(io.LimitReader(resp.Body, 16<<20)).Decode(&data)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<20))
		_ = resp.Body.Close()
		if resp.StatusCode == 429 {
			f.throttles.Add(1)
			seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
			if seconds < 1 {
				seconds = 60
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(seconds) * time.Second):
				continue
			}
		}
		if err != nil {
			return nil, errors.New("API returned invalid JSON")
		}
		if resp.StatusCode >= 400 {
			e, _ := data["error"].(map[string]any)
			return nil, fmt.Errorf("%s %s: HTTP %d %v", method, path, resp.StatusCode, e["code"])
		}
		return data, nil
	}
}
func str(m map[string]any, key string) string { value, _ := m[key].(string); return value }
func obj(m map[string]any, key string) map[string]any {
	value, _ := m[key].(map[string]any)
	return value
}
func seq(m map[string]any, key string) int64 { value, _ := m[key].(float64); return int64(value) }
func wait(ctx context.Context, try func() bool) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if try() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (f *fixture) setup(o options) error {
	listener, err := (&net.ListenConfig{}).Listen(f.ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	f.url = "http://" + addr
	f.admin, err = f.machine("admin", addr)
	if err != nil {
		return err
	}
	if err := f.process(f.admin, "serve"); err != nil {
		return err
	}
	if err := wait(f.ctx, func() bool {
		raw, e := os.ReadFile(filepath.Join(f.admin.home, "aboard", "config", "local-owner-token"))
		if e != nil {
			return false
		}
		key := strings.TrimSpace(string(raw))
		_, e = f.api(f.ctx, "GET", "/v1/me", key, nil)
		if e == nil {
			f.admin.key = key
		}
		return e == nil
	}); err != nil {
		return err
	}
	for i := range o.Boards {
		data, e := f.api(f.ctx, "POST", "/v1/boards", f.admin.key, map[string]any{"name": fmt.Sprintf("load-board-%d", i), "template": "general"})
		if e != nil {
			return e
		}
		f.boards = append(f.boards, &board{ID: str(data, "id"), Name: str(data, "name"), Head: seq(data, "head_seq")})
	}
	for i := range o.People {
		p, e := f.machine(fmt.Sprintf("person-%d", i), "127.0.0.1:1")
		if e != nil {
			return e
		}
		f.people = append(f.people, p)
		invite, e := f.api(f.ctx, "POST", "/v1/invites", f.admin.key, map[string]any{})
		if e != nil {
			return e
		}
		joined, e := f.api(f.ctx, "POST", "/v1/connect", "", map[string]any{"invite": str(invite, "invite"), "handle": p.handle, "key_name": "load"})
		if e != nil {
			return e
		}
		p.key = str(obj(joined, "key"), "token")
		if e := f.cli(p, p.key+"\n", "login", f.url, "--json"); e != nil {
			return e
		}
		membership := map[string]bool{}
		for j := range o.Agents {
			b := f.boards[(i*o.Agents+j)%o.Boards]
			if !membership[b.Name] {
				if _, e = f.api(f.ctx, "POST", "/v1/boards/"+b.Name+"/people", f.admin.key, map[string]any{"handle": p.handle}); e != nil {
					return e
				}
				membership[b.Name] = true
			}
			name := fmt.Sprintf("agent-%d-%d", i, j)
			session := name
			data, e := f.api(f.ctx, "POST", "/v1/join", p.key, map[string]any{"board": b.Name, "role": "member", "name": name, "harness": "omp", "session": "omp:" + session})
			if e != nil {
				return e
			}
			a := obj(data, "agent")
			s := &seat{Server: f.url, MemberID: str(a, "id"), Board: b.Name, Name: str(a, "name"), Token: str(data, "token"), owner: p, board: b, session: session}
			p.seats = append(p.seats, s)
		}
		config := filepath.Join(p.home, "aboard", "config")
		if e := os.MkdirAll(config, 0o700); e != nil {
			return e
		}
		creds, e := json.Marshal(map[string]any{"agents": p.seats})
		if e != nil {
			return e
		}
		if e := os.WriteFile(filepath.Join(config, "credentials.json"), creds, 0o600); e != nil {
			return e
		}
		if e := f.process(p, "daemon"); e != nil {
			return e
		}
		if e := wait(f.ctx, func() bool {
			c, e := (&net.Dialer{}).DialContext(f.ctx, "unix", socketPath(p.state))
			if e == nil {
				_ = c.Close()
			}
			return e == nil
		}); e != nil {
			return e
		}
		for _, s := range p.seats {
			if e := f.bind(s); e != nil {
				return e
			}
		}
		p.heads, e = f.stream(p)
		if e != nil {
			return e
		}
		if e := p.heads.await(f.ctx, func(heads []observation) bool {
			for name := range membership {
				found := false
				for _, h := range heads {
					for _, b := range f.boards {
						if b.Name == name && b.ID == h.Key.BoardID {
							found = true
						}
					}
				}
				if !found {
					return false
				}
			}
			return true
		}); e != nil {
			return e
		}
	}
	return f.primeObservers()
}

func socketPath(state string) string {
	p := filepath.Join(state, "daemon.sock")
	if len(p) <= 100 {
		return p
	}
	sum := sha256.Sum256([]byte(state))
	return filepath.Join(string(filepath.Separator), "tmp", "aboard-"+strconv.Itoa(os.Getuid()), hex.EncodeToString(sum[:8])+".sock")
}

func send(conn net.Conn, data any) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	_, err = conn.Write(append(raw, '\n'))
	return err
}

func (f *fixture) bind(s *seat) error {
	conn, err := (&net.Dialer{}).DialContext(f.ctx, "unix", socketPath(s.owner.state))
	if err != nil {
		return err
	}
	ext := &extension{conn: conn, frames: make(chan frame, 16), err: make(chan error, 1), done: make(chan struct{})}
	s.ext = ext
	if deadline, ok := f.ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return err
		}
	}
	f.background(func() {
		defer close(ext.done)
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			var v frame
			if e := json.Unmarshal(scanner.Bytes(), &v); e != nil {
				ext.err <- e
				return
			}
			v.at = time.Now()
			if v.Event == "deliver" {
				ext.mu.Lock()
				ext.observed = append(ext.observed, v)
				ext.mu.Unlock()
			}
			select {
			case ext.frames <- v:
			case <-f.ctx.Done():
				return
			}
		}
		e := scanner.Err()
		if e == nil {
			e = io.EOF
		}
		ext.err <- e
	})
	if err := send(conn, map[string]any{"v": 1, "op": "hello", "harness": "omp", "session": s.session, "boot": "load-boot", "source": "startup", "process": map[string]any{"pid": os.Getpid()}, "capabilities": []string{"handoff-v1"}}); err != nil {
		return err
	}
	welcome, err := ext.next(f.ctx)
	if err != nil {
		return err
	}
	if welcome.Event != "welcome" {
		return errors.New("extension hello refused")
	}
	control, err := (&net.Dialer{}).DialContext(f.ctx, "unix", socketPath(s.owner.state))
	if err != nil {
		return err
	}
	defer func() { _ = control.Close() }()
	if deadline, ok := f.ctx.Deadline(); ok {
		if err := control.SetDeadline(deadline); err != nil {
			return err
		}
	}
	if err := send(control, map[string]any{"v": 1, "op": "bind", "harness": "omp", "session": s.session, "boot": "load-boot", "agent": map[string]any{"server": s.Server, "member_id": s.MemberID, "board": s.Board, "name": s.Name}}); err != nil {
		return err
	}
	var answer map[string]any
	if err := json.NewDecoder(control).Decode(&answer); err != nil {
		return err
	}
	if answer["error"] != nil {
		return fmt.Errorf("bind refused: %v", obj(answer, "error")["code"])
	}
	return nil
}

func (e *extension) next(ctx context.Context) (frame, error) {
	for {
		select {
		case <-ctx.Done():
			return frame{}, ctx.Err()
		case err := <-e.err:
			return frame{}, err
		case v := <-e.frames:
			if v.Error != nil {
				return v, fmt.Errorf("extension: %s", v.Error.Code)
			}
			if v.Event == "welcome" || v.Event == "deliver" {
				return v, nil
			}
			return v, fmt.Errorf("unexpected extension event %q", v.Event)
		}
	}
}

func (f *fixture) stream(p *machine) (*headLog, error) {
	req, err := http.NewRequestWithContext(f.ctx, "GET", f.url+"/v1/stream", http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+p.key)
	// A stream lasts the run; the ordinary request client has a response deadline.
	response, err := (&http.Client{Transport: f.client.Transport}).Do(req)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != 200 {
		_ = response.Body.Close()
		return nil, errors.New("stream refused")
	}
	log := &headLog{changed: make(chan struct{})}
	f.background(func() { f.readStream(response.Body, log) })
	return log, nil
}

func (f *fixture) readStream(body io.ReadCloser, log *headLog) {
	defer func() { _ = body.Close() }()
	scanner := bufio.NewScanner(body)
	kind := ""
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event:") {
			kind = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if kind == "head" && strings.HasPrefix(line, "data:") {
			var data map[string]any
			if e := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data); e != nil {
				log.finish(e)
				return
			}
			log.mu.Lock()
			log.heads = append(log.heads, observation{messageKey{str(data, "board_id"), seq(data, "seq")}, time.Now()})
			close(log.changed)
			log.changed = make(chan struct{})
			log.mu.Unlock()
		}
	}
	e := scanner.Err()
	if e == nil {
		e = io.EOF
	}
	log.finish(e)
}

func (h *headLog) finish(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.err = err
	close(h.changed)
	h.changed = make(chan struct{})
}

func (h *headLog) await(ctx context.Context, test func([]observation) bool) error {
	for {
		h.mu.Lock()
		ok := test(h.heads)
		changed, err := h.changed, h.err
		h.mu.Unlock()
		if ok {
			return nil
		}
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
	}
}

var markerPattern = regexp.MustCompile(`load-r\d+-b\d+`)

type polled struct {
	seat *seat
	data map[string]any
	at   time.Time
	err  error
}

func (f *fixture) round(ctx context.Context, round int, checks *deliveryCheck, streams, polls, handover, writes *[]time.Duration, r *report) error {
	replies := make(chan polled, len(f.people)*len(f.people[0].seats))
	started := make(chan error, cap(replies))
	for _, p := range f.people {
		for _, s := range p.seats {
			f.background(func() {
				var ready sync.Once
				trace := &httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
					if !info.Reused {
						ready.Do(func() { started <- errors.New("observer connection was not established during setup") })
					}
				}, WroteRequest: func(info httptrace.WroteRequestInfo) {
					ready.Do(func() { started <- info.Err })
				}}
				ctx := httptrace.WithClientTrace(ctx, trace)
				data, err := f.apiWithClient(ctx, s.observer, "GET", fmt.Sprintf("/v1/me/inbox?wait=60&after=%d", s.board.Head), s.Token, nil)
				ready.Do(func() {
					if err == nil {
						err = errors.New("long poll returned without request transmission evidence")
					}
					started <- err
				})
				replies <- polled{s, data, time.Now(), err}
			})
		}
	}
	for range cap(replies) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-started:
			if err != nil {
				return fmt.Errorf("start observer long poll: %w", err)
			}
		}
	}
	byBoard := map[string]sample{}
	markers := map[string]sample{}
	for i, b := range f.boards {
		marker := fmt.Sprintf("load-r%d-b%d", round, i)
		start := time.Now()
		data, err := f.api(ctx, "POST", "/v1/boards/"+b.Name+"/messages", f.admin.key, map[string]any{"body": marker, "to": []string{"all"}})
		if err != nil {
			return err
		}
		v := sample{messageKey{b.ID, seq(data, "seq")}, start, marker}
		*writes = append(*writes, time.Since(start))
		b.Head = v.key.Seq
		byBoard[b.ID] = v
		markers[marker] = v
		r.Posts++
	}
	for _, p := range f.people {
		visited := map[string]bool{}
		for _, s := range p.seats {
			v := byBoard[s.board.ID]
			checks.Expected[s.MemberID] = append(checks.Expected[s.MemberID], v.key)
			if visited[s.board.ID] {
				continue
			}
			visited[s.board.ID] = true
			if err := p.heads.await(ctx, func(heads []observation) bool { _, err := firstHead(heads, v.key); return err == nil }); err != nil {
				return err
			}
			p.heads.mu.Lock()
			at, err := firstHead(p.heads.heads, v.key)
			p.heads.mu.Unlock()
			if err != nil {
				return err
			}
			*streams = append(*streams, at.Sub(v.started))
		}
	}
	for range cap(replies) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case reply := <-replies:
			if reply.err != nil {
				return reply.err
			}
			want := byBoard[reply.seat.board.ID]
			messages, _ := reply.data["messages"].([]any)
			if len(messages) != 1 {
				return errors.New("long poll lost or duplicated a round message")
			}
			message, _ := messages[0].(map[string]any)
			if seq(message, "seq") != want.key.Seq || str(message, "body") != want.marker {
				return errors.New("long poll returned the wrong message")
			}
			*polls = append(*polls, reply.at.Sub(want.started))
		}
	}
	for _, p := range f.people {
		for _, s := range p.seats {
			v, err := s.ext.next(ctx)
			if err != nil {
				return err
			}
			if v.Event != "deliver" {
				return errors.New("missing extension delivery")
			}
			found := markerPattern.FindAllString(v.Bundle, -1)
			if len(found) != 1 {
				return errors.New("delivery lost or duplicated its marker")
			}
			sample, ok := markers[found[0]]
			if !ok || sample.key.BoardID != s.board.ID {
				return errors.New("delivery crossed boards or repeated an older message")
			}
			actual, err := renderedMessage(v.Bundle, s.Board)
			if err != nil {
				return err
			}
			if actual != sample.key.Seq {
				return errors.New("rendered message sequence disagrees with posted message")
			}
			checks.Seen[s.MemberID] = append(checks.Seen[s.MemberID], messageKey{s.board.ID, actual})
			*handover = append(*handover, v.at.Sub(sample.started))
			r.Deliveries++
			answer := map[string]any{"v": 1, "op": "received", "id": v.ID}
			if v.Handoff != "" {
				delete(answer, "id")
				answer["handoff_id"] = v.Handoff
			}
			if err := send(s.ext.conn, answer); err != nil {
				return err
			}
			if err := send(s.ext.conn, map[string]any{"v": 1, "op": "prompt"}); err != nil {
				return err
			}
			if err := send(s.ext.conn, map[string]any{"v": 1, "op": "turn_end"}); err != nil {
				return err
			}
		}
	}
	// Confirm every fake harness promptly; server ack polling must not consume the
	// other harnesses' confirmation windows while they wait for this driver.
	for _, p := range f.people {
		for _, s := range p.seats {
			if err := f.awaitAck(s, byBoard[s.board.ID].key.Seq); err != nil {
				return err
			}
		}
	}
	return nil
}

func (f *fixture) awaitAck(s *seat, want int64) error {
	var terminal error
	err := wait(f.ctx, func() bool {
		data, e := f.api(f.ctx, "GET", "/v1/me/inbox", s.Token, nil)
		if e != nil {
			terminal = e
			return true
		}
		cursor := seq(data, "cursor")
		messages, _ := data["messages"].([]any)
		if cursor > want {
			terminal = errors.New("acknowledgement crossed the expected cursor")
			return true
		}
		return cursor == want && len(messages) == 0
	})
	if terminal != nil {
		return terminal
	}
	return err
}

func renderedMessage(bundle, board string) (int64, error) {
	decoder := xml.NewDecoder(strings.NewReader("<root>" + bundle + "</root>"))
	var sequence int64
	count := 0
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return 0, errors.New("delivery is not valid message markup")
		}
		start, ok := token.(xml.StartElement)
		if !ok || start.Name.Local != "aboard-message" {
			continue
		}
		count++
		foundBoard := ""
		for _, attr := range start.Attr {
			switch attr.Name.Local {
			case "board":
				foundBoard = attr.Value
			case "seq":
				sequence, err = strconv.ParseInt(attr.Value, 10, 64)
				if err != nil {
					return 0, err
				}
			}
		}
		if foundBoard != board {
			return 0, errors.New("rendered message names the wrong board")
		}
	}
	if count != 1 || sequence < 1 {
		return 0, errors.New("delivery must contain exactly one sequenced message")
	}
	return sequence, nil
}

func (f *fixture) finishExtensions(rounds int) error {
	for _, p := range f.people {
		for _, s := range p.seats {
			if err := send(s.ext.conn, map[string]any{"v": 1, "op": "goodbye"}); err != nil {
				return err
			}
			select {
			case <-f.ctx.Done():
				return f.ctx.Err()
			case <-s.ext.done:
			}
			s.ext.mu.Lock()
			count := len(s.ext.observed)
			s.ext.mu.Unlock()
			if count != rounds {
				return errors.New("extension observed missing or duplicate deliveries")
			}
		}
	}
	return nil
}

func (f *fixture) background(work func()) {
	f.workers.Add(1)
	go func() { defer f.workers.Done(); work() }()
}
