package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
	"github.com/leonidas1712/aboard/server/internal/delivery/deliverytest"
)

// The tests run the fake codex from e2e/fakecodex, which speaks the protocol of
// codex-cli 0.159.3, first on the PATH.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "aboard-codex-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	build := &exec.Cmd{
		Path:   goTool,
		Args:   []string{"go", "build", "-o", filepath.Join(dir, "codex"), "github.com/leonidas1712/aboard/e2e/fakecodex"},
		Stdout: os.Stderr, Stderr: os.Stderr,
	}
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build fake codex:", err)
		os.Exit(1)
	}
	_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	queueLog = filepath.Join(dir, "queue.jsonl")
	_ = os.Setenv("FAKE_CODEX_LOG", queueLog)
	_ = os.Setenv("FAKE_CODEX_THREADS", filepath.Join(dir, "threads.json"))
	threads := `{"019a0000-0000-7000-8000-00000000000b":{"parent":"019a0000-0000-7000-8000-00000000000a"},` +
		`"019a0000-0000-7000-8000-0000000000dd":{"missing":true}}`
	if err := os.WriteFile(filepath.Join(dir, "threads.json"), []byte(threads), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// queueLog is where the fake codex records what it queued.
var queueLog string

// queued returns the messages the fake codex queued for a thread.
func queued(t *testing.T, thread string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Clean(queueLog))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var c map[string]string
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if c["thread"] == thread {
			out = append(out, c["message"])
		}
	}
	return out
}

// threads counts the root threads the tests made, so each test queues to a thread of
// its own even when the tests run several times.
var threads int

func TestCodexAdapterContract(t *testing.T) {
	deliverytest.RunAdapter(t, deliverytest.AdapterFixture{
		New: func(*testing.T) delivery.Adapter { return &Adapter{ClientVersion: "test"} },
		Root: func(*testing.T) string {
			threads++
			return fmt.Sprintf("019a0000-0000-7000-8000-%012d", threads)
		},
		SubAgent: func(*testing.T) string { return "019a0000-0000-7000-8000-00000000000b" },
		Absent:   func(*testing.T) string { return "019a0000-0000-7000-8000-0000000000dd" },
		Received: queued,
	})
}

func TestQueueFailureIsAFailedAttempt(t *testing.T) {
	t.Setenv("FAKE_CODEX_QUEUE_FAIL", "1")
	_, err := (&Adapter{}).Hand(context.Background(), delivery.Handover{SessionID: "019a0000-0000-7000-8000-0000000000aa", Bundle: "x"})
	if err == nil || errors.Is(err, delivery.ErrBusy) || errors.Is(err, delivery.ErrTargetAbsent) {
		t.Fatalf("a failed queue run = %v, want an error that counts as an attempt", err)
	}
	if !strings.Contains(err.Error(), "thread is busy") {
		t.Fatalf("error doesn't carry what codex printed: %v", err)
	}
}

func TestVersionAndQueueCommand(t *testing.T) {
	a := &Adapter{}
	v, err := a.Version(context.Background())
	if err != nil || v != "codex-cli 0.159.3" {
		t.Fatalf("Version = %q, %v", v, err)
	}
	if !a.HasQueue(context.Background()) {
		t.Fatal("HasQueue = false")
	}
}

func TestQueueErrorsAreClassifiedByWhatCodexPrinted(t *testing.T) {
	cause := errors.New("exit status 1")
	tests := []struct {
		stderr string
		want   error
	}{
		{"Error: failed to queue session message: thread/queue/add failed: failed to read thread: invalid thread-store request: no rollout found for thread id 019a (code -32603)", delivery.ErrTargetAbsent},
		{"Error: failed to queue session message: thread not found: 019a", delivery.ErrTargetAbsent},
		{"Error: No active session found matching 'x'.", delivery.ErrTargetAbsent},
		{"Error: direct app-server input is not allowed for unloaded spawned sub-agents", delivery.ErrSubAgent},
		{"Error: the local app-server daemon does not support thread/queue/add; update or restart it", nil},
	}
	for _, tt := range tests {
		err := queueError(tt.stderr, cause)
		if tt.want == nil {
			if errors.Is(err, delivery.ErrTargetAbsent) || errors.Is(err, delivery.ErrSubAgent) {
				t.Errorf("%q classified as %v", tt.stderr, err)
			}
			continue
		}
		if !errors.Is(err, tt.want) {
			t.Errorf("%q = %v, want %v", tt.stderr, err, tt.want)
		}
	}
}
