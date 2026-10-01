// Command fakecodex stands in for the codex CLI in tests. It records every message queued
// for a thread and answers thread lookups from a file, so tests can check delivery into
// Codex without running Codex. It speaks the protocol of codex-cli 0.159.3:
//
//   - `codex queue --thread <THREAD> --message <TEXT>` prints "Queued message … for
//     thread …." and exits 0, or prints "Error: …" to standard error and exits 1.
//   - `codex app-server` reads one JSON message per line on standard input and writes one
//     per line on standard output, without a "jsonrpc" field. Requests other than
//     initialize fail with -32600 "Not initialized" until initialize succeeds, and the
//     server sends notifications of its own in between responses.
//   - `thread/read` answers {"thread": Thread}. A sub-agent thread read from disk has
//     "parentThreadId": null and "source": {"subAgent": {"thread_spawn": {...}}}. An id
//     that isn't a UUID fails with -32600 "invalid thread id: …"; a thread that doesn't
//     exist fails with -32600 "thread not loaded: <id>".
//
// Environment:
//
//	FAKE_CODEX_LOG         file that receives one JSON line {"thread","message"} per queue call
//	FAKE_CODEX_THREADS     JSON file mapping thread id to {"parent": "<id>"} for sub-agent
//	                       threads, or {"missing": true} for threads that don't exist; a
//	                       thread missing from it is a root thread
//	FAKE_CODEX_QUEUE_FAIL  when set, queue calls fail with exit status 1
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

const version = "codex-cli 0.159.3"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	switch {
	case len(args) == 1 && args[0] == "--version":
		fmt.Println(version)
		return 0
	case len(args) >= 1 && args[0] == "queue":
		return queue(args[1:])
	case len(args) >= 1 && args[0] == "app-server":
		return appServer()
	}
	fmt.Fprintln(os.Stderr, "fakecodex: unsupported arguments", args)
	return 2
}

// threadInfo is one entry of FAKE_CODEX_THREADS.
type threadInfo struct {
	Parent  string `json:"parent"`
	Missing bool   `json:"missing"`
}

func threads() map[string]threadInfo {
	m := map[string]threadInfo{}
	if raw, err := readInRoot(os.Getenv("FAKE_CODEX_THREADS")); err == nil {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

var uuidPattern = regexp.MustCompile(`^(urn:uuid:)?[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// readInRoot reads a file named by the environment, opened within its own directory.
func readInRoot(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.ReadFile(filepath.Base(path))
}

// appendInRoot opens a file named by the environment for appending, within its own
// directory.
func appendInRoot(path string) (*os.File, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }()
	return root.OpenFile(filepath.Base(path), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
}

func queue(args []string) int {
	if slices.Contains(args, "--help") {
		fmt.Println("Queue a message for an existing session\n\nUsage: codex queue [OPTIONS] --thread <THREAD> --message <TEXT>")
		return 0
	}
	var thread, message string
	var haveThread, haveMessage bool
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--thread", "--message":
			if i+1 >= len(args) {
				fmt.Fprintf(os.Stderr, "error: a value is required for '%s <VALUE>' but none was supplied\n", args[i])
				return 2
			}
			if args[i] == "--thread" {
				thread, haveThread = args[i+1], true
			} else {
				message, haveMessage = args[i+1], true
			}
			i++
		default:
			fmt.Fprintf(os.Stderr, "error: unexpected argument '%s' found\n", args[i])
			return 2
		}
	}
	if !haveThread || !haveMessage {
		fmt.Fprintln(os.Stderr, "error: the following required arguments were not provided:\n  --thread <THREAD>\n  --message <TEXT>")
		return 2
	}
	if message == "" {
		fmt.Fprintln(os.Stderr, "Error: message must not be empty")
		return 1
	}
	if os.Getenv("FAKE_CODEX_QUEUE_FAIL") != "" {
		fmt.Fprintln(os.Stderr, "Error: failed to queue session message: thread/queue/add failed: thread is busy writing its rollout (code -32603)")
		return 1
	}
	if !uuidPattern.MatchString(thread) {
		fmt.Fprintf(os.Stderr, "Error: No active session found matching '%s'.\n", thread)
		return 1
	}
	if threads()[thread].Missing {
		fmt.Fprintf(os.Stderr, "Error: failed to queue session message: thread/queue/add failed: failed to read thread: "+
			"invalid thread-store request: no rollout found for thread id %s (code -32603)\n", thread)
		return 1
	}
	line, _ := json.Marshal(map[string]string{"thread": thread, "message": message})
	f, err := appendInRoot(os.Getenv("FAKE_CODEX_LOG"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Write(append(line, '\n')); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		return 1
	}
	fmt.Printf("Queued message 019a0000-0000-7000-8000-0000000000ff for thread %s.\n", thread)
	return 0
}

// appServer answers requests, one JSON message per line, on standard input.
func appServer() int {
	in := bufio.NewScanner(os.Stdin)
	out := json.NewEncoder(os.Stdout)
	initialized := false
	reply := func(id, result any) { _ = out.Encode(map[string]any{"id": id, "result": result}) }
	fail := func(id any, code int, msg string) {
		_ = out.Encode(map[string]any{"error": map[string]any{"code": code, "message": msg}, "id": id})
	}
	for in.Scan() {
		var req struct {
			ID     any             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if json.Unmarshal(in.Bytes(), &req) != nil || req.ID == nil {
			continue // notifications such as "initialized"
		}
		if req.Method != "initialize" && !initialized {
			fail(req.ID, -32600, "Not initialized")
			continue
		}
		switch req.Method {
		case "initialize":
			var p struct {
				ClientInfo *struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"clientInfo"`
			}
			if json.Unmarshal(req.Params, &p) != nil || p.ClientInfo == nil {
				fail(req.ID, -32600, "Invalid request: missing field `clientInfo`")
				continue
			}
			if initialized {
				fail(req.ID, -32600, "Already initialized")
				continue
			}
			initialized = true
			reply(req.ID, map[string]any{
				"userAgent": p.ClientInfo.Name + "/0.159.3", "codexHome": os.Getenv("HOME") + "/.codex",
				"platformFamily": "unix", "platformOs": "macos",
			})
			_ = out.Encode(map[string]any{"method": "remoteControl/status/changed", "params": map[string]any{"status": "disabled"}})
		case "thread/read":
			var p struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &p)
			info := threads()[p.ThreadID]
			switch {
			case !uuidPattern.MatchString(p.ThreadID):
				fail(req.ID, -32600, "invalid thread id: invalid character: expected an optional prefix of `urn:uuid:` followed by [0-9a-fA-F-]")
				continue
			case info.Missing:
				fail(req.ID, -32600, "thread not loaded: "+p.ThreadID)
				continue
			}
			thread := map[string]any{
				"id": p.ThreadID, "sessionId": p.ThreadID, "parentThreadId": nil, "forkedFromId": nil,
				"source": "cli", "threadSource": nil, "agentNickname": nil, "agentRole": nil,
				"status": map[string]any{"type": "notLoaded"}, "cliVersion": "0.159.3", "turns": []any{},
			}
			if info.Parent != "" {
				thread["source"] = map[string]any{"subAgent": map[string]any{"thread_spawn": map[string]any{
					"parent_thread_id": info.Parent, "depth": 1, "agent_path": nil, "agent_nickname": "Aquinas", "agent_role": "explorer",
				}}}
				thread["agentNickname"], thread["agentRole"] = "Aquinas", "explorer"
			}
			reply(req.ID, map[string]any{"thread": thread})
		default:
			fail(req.ID, -32601, "method not found: "+req.Method)
		}
	}
	return 0
}
