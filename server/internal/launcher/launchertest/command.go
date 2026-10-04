package launchertest

import (
	"bytes"
	"context"
	"encoding/json"
	"os/exec"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/launcher"
	"github.com/leonidas1712/aboard/server/internal/launcher/external"
)

// RunCommand checks an external launcher's command: the whole kit through the
// protocol, and that it refuses what it must, with the codes spec/launcher.md gives. env
// is the environment the command runs with (nil: this process's).
func RunCommand(t *testing.T, name, path string, env []string) {
	t.Helper()
	l := external.Launcher{Name: name, Path: path, Env: env}
	info, err := l.Info(context.Background())
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	if info.Name != name {
		t.Errorf("info names the launcher %q; its command is %s, so want %q", info.Name, path, name)
	}
	t.Run("UnknownOpIsRefused", func(t *testing.T) {
		if code := rawCall(t, path, env, `{"v":1,"op":"dance"}`); code != "invalid_request" {
			t.Errorf("an unknown op got the code %q, want invalid_request", code)
		}
	})
	t.Run("WrongVersionIsRefused", func(t *testing.T) {
		if code := rawCall(t, path, env, `{"v":99,"op":"info"}`); code != "launcher_protocol_mismatch" {
			t.Errorf("version 99 got the code %q, want launcher_protocol_mismatch", code)
		}
	})
	Run(t, l)
}

// rawCall sends one request as given and returns the error code of the answer, which
// must come with a nonzero exit status.
func rawCall(t *testing.T, path string, env []string, request string) string {
	t.Helper()
	cmd := exec.CommandContext(context.Background(), path) //nolint:gosec // the launcher under test
	cmd.Env = env
	cmd.Stdin = bytes.NewReader([]byte(request + "\n"))
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	runErr := cmd.Run()
	var resp launcher.Response
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		t.Fatalf("the answer to %s isn't one JSON object: %q", request, stdout.String())
	}
	if resp.Error == nil {
		t.Fatalf("the answer to %s is no error: %s", request, stdout.String())
	}
	if runErr == nil {
		t.Errorf("the launcher answered %s with an error but exited 0", request)
	}
	return resp.Error.Code
}
