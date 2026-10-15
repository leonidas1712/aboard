package live

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type codexAppServerProcess struct {
	parent int
	line   string
}

func parseCodexAppServers(output, home string) map[int]codexAppServerProcess {
	found := map[int]codexAppServerProcess{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !codexAppServerCommand(fields[2:]) || !slices.Contains(fields, "CODEX_HOME="+home) {
			continue
		}
		pid, errPID := strconv.Atoi(fields[0])
		parent, errParent := strconv.Atoi(fields[1])
		if errPID == nil && errParent == nil {
			found[pid] = codexAppServerProcess{parent: parent, line: line}
		}
	}
	return found
}

// Daemon management commands and clients can inherit a PATH containing
// app-server-daemon; only a native Codex app-server invocation is a server.
func codexAppServerCommand(args []string) bool {
	if len(args) < 2 || filepath.Base(args[0]) != "codex" {
		return false
	}
	args = args[1:]
	for len(args) > 0 {
		switch {
		case args[0] == "-c" || args[0] == "--config":
			if len(args) < 2 {
				return false
			}
			args = args[2:]
		case strings.HasPrefix(args[0], "--config="):
			args = args[1:]
		default:
			return args[0] == "app-server" && (len(args) < 2 || args[1] != "daemon")
		}
	}
	return false
}

func TestCodexAppServerProcesses(t *testing.T) {
	const home = "/tmp/lab/codex-home"
	binary := filepath.Join(home, "packages", "app-server-daemon", "releases", "version", "bin", "codex")
	rows := []struct {
		name    string
		command string
		want    bool
	}{
		{"managed server", binary + " app-server --listen unix:// --managed-daemon", true},
		{"stdio server", "/opt/bin/codex app-server", true},
		{"server with config override", "/opt/bin/codex -c key=value app-server", true},
		{"pid update helper", binary + " app-server daemon pid-update-loop", false},
		{"daemon launcher", "/opt/bin/codex app-server daemon start", false},
		{"node launcher", "node /opt/bin/codex app-server", false},
		{"interactive client", "/opt/bin/codex --model test-model", false},
		{"aboard server", "/tmp/bin/aboard serve", false},
		{"tmux server", "/opt/bin/tmux -L swarm start-server", false},
		{"hook shell", "sh -c /tmp/bin/aboard hook codex prompt", false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			// Codex exports its managed-server path to commands it starts. The path
			// must not turn those commands into app-server processes.
			line := "100 1 " + row.command + " CODEX_HOME=" + home + " PATH=" + filepath.Dir(binary)
			got := parseCodexAppServers(line, home)
			if (len(got) == 1) != row.want {
				t.Fatalf("server=%t, want %t for %s", len(got) == 1, row.want, row.command)
			}
		})
	}
	// A second real server must remain visible to the live proof's no-extra-server
	// assertion, including when it is not a child of the prestarted daemon.
	output := "100 1 " + binary + " app-server --listen unix:// CODEX_HOME=" + home + "\n" +
		"200 1 /opt/bin/codex app-server CODEX_HOME=" + home + "\n" +
		"300 1 /opt/bin/codex app-server CODEX_HOME=" + home + "-other\n"
	got := parseCodexAppServers(output, home)
	if len(got) != 2 || got[100].parent != 1 || got[200].parent != 1 {
		t.Fatalf("real servers hidden or other home included: %v", got)
	}
}
