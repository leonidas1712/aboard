//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const serverSwarmFile = "board: remote-probe\nagents:\n  - {name: worker, harness: claude-code, launcher: headless}\n"

func TestNewSwarmUsesSelectedServerAndIgnoresLegacyFolder(t *testing.T) {
	t.Parallel()
	for _, choice := range []string{"default", "name", "url"} {
		t.Run(choice, func(t *testing.T) {
			t.Parallel()
			tm := newTeam(t)
			s := newSwarmEnv(t)
			s.run("up")
			s.run("connect", tm.invite(), "--handle", "sam")
			s.run("servers", "name", tm.url(), "remote")
			s.run("board", "new", "remote-probe", "--server", "remote")
			s.run("servers", "use", "local")
			if choice == "default" {
				s.run("servers", "use", "remote")
			}
			legacy := `{"server":{"url":"https://ignored.example"},"board":"elsewhere"}`
			if choice == "default" {
				legacy = `{"server":{"url":"` + tm.url() + `"},"board":"remote-probe"}`
			}
			s.writeFileIn(filepath.Join(s.dir, ".aboard"), legacy)
			s.writeBoardFile(serverSwarmFile)
			s.run("down")
			args := []string{"swarm", "up", "--file", "aboard.yaml", "--wait", "2m", "--json"}
			if choice == "name" {
				args = append(args, "--server", "remote")
			}
			if choice == "url" {
				args = append(args, "--server", tm.url())
			}
			up := s.run(args...).json(t)
			matchesCLISpec(t, "SwarmUpOutput", up)
			if field(t, up, "server.url") != tm.url() || field(t, up, "server.name") != "remote" || up["server_started"] != false || up["board_created"] != false || agentsByName(t, up)["worker"]["seated"] != true {
				t.Fatalf("swarm used the wrong server or board: %v", up)
			}
			raw, err := os.ReadFile(filepath.Join(s.dir, ".aboard"))
			if err != nil || string(raw) != legacy {
				t.Fatalf("legacy file changed: %v", err)
			}
			if _, err := os.Stat(filepath.Join(s.dataDir(), "server.pid")); !os.IsNotExist(err) {
				t.Fatalf("remote launch started local server: %v", err)
			}
			id := up["swarm"].(string)
			raw, err = os.ReadFile(filepath.Join(s.stateDir(), "swarms", id+".json"))
			if err != nil {
				t.Fatal(err)
			}
			var record map[string]any
			if err := json.Unmarshal(raw, &record); err != nil {
				t.Fatal(err)
			}
			if record["server"] != tm.url() {
				t.Fatalf("record has wrong server: %v", record["server"])
			}
			textArgs := []string{"swarm", "up"}
			if choice != "default" {
				textArgs = append(textArgs, "--server", "remote")
			}
			if text := s.run(textArgs...).stdout; !strings.Contains(text, tm.url()) {
				t.Fatalf("text hides selected server: %s", text)
			}
			s.run("swarm", "down", "--swarm", id)
			s.run("servers", "use", "local")
			resumed := s.run("swarm", "up", "--swarm", id, "--json").json(t)
			if field(t, resumed, "server.url") != tm.url() || resumed["swarm"] != id {
				t.Fatalf("recorded swarm followed new default: %v", resumed)
			}
			refused := s.runExit("swarm", "up", "--swarm", id, "--server", "local", "--json")
			if refused.code != 2 || errorCode(t, refused.json(t)) != "invalid_request" {
				t.Fatalf("recorded swarm allowed retarget: %s", refused)
			}
			s.run("up")
			boards := s.run("boards", "--server", "local", "--json").json(t)
			if len(field(t, boards, "boards").([]any)) != 0 {
				t.Fatalf("local duplicate board created: %v", boards)
			}
		})
	}
}

func TestNewSwarmRequiresUsableServerSelectionBeforeLaunch(t *testing.T) {
	t.Parallel()
	for _, choice := range []string{"one-known", "ambiguous", "stale-default", "stale-empty", "unknown-flag"} {
		t.Run(choice, func(t *testing.T) {
			t.Parallel()
			s := newSwarmEnv(t)
			if choice != "unknown-flag" && choice != "stale-empty" {
				tm := newTeam(t)
				s.run("connect", tm.invite(), "--handle", "sam")
				if choice == "ambiguous" {
					s.run("connect", newTeam(t).invite(), "--handle", "sam")
				}
				path := filepath.Join(s.configDir(), "servers.json")
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var saved map[string]any
				if err := json.Unmarshal(raw, &saved); err != nil {
					t.Fatal(err)
				}
				delete(saved, "default")
				if choice == "stale-default" {
					saved["default"] = "https://forgotten.example"
				}
				raw, err = json.Marshal(saved)
				if err != nil {
					t.Fatal(err)
				}
				s.writeFileIn(path, string(raw))
			}
			if choice == "stale-empty" {
				if err := os.MkdirAll(s.configDir(), 0o700); err != nil {
					t.Fatal(err)
				}
				s.writeFileIn(filepath.Join(s.configDir(), "servers.json"), `{"default":"https://forgotten.example"}`)
			}
			s.writeBoardFile(serverSwarmFile)
			s.writeFileIn(filepath.Join(s.dir, ".aboard"), `{"server":{"url":"https://remote.example"},"board":"remote-probe"}`)
			args := []string{"swarm", "up", "--file", "aboard.yaml", "--wait", "2m", "--json"}
			want := "server_not_selected"
			if choice == "unknown-flag" {
				args = append(args, "--server", "missing")
				want = "server_unknown"
			}
			r := s.runExit(args...)
			if r.code != 1 || errorCode(t, r.json(t)) != want {
				t.Fatalf("unsafe server choice: %s", r)
			}
			hint := field(t, r.json(t), "error.hint").(string)
			if want == "server_not_selected" && (!strings.Contains(hint, "aboard servers use") || !strings.Contains(hint, "--server")) {
				t.Fatalf("no actionable choice: %s", r)
			}
			if len(s.starts("")) != 0 {
				t.Fatal("refused selection started an agent")
			}
			if _, err := os.Stat(filepath.Join(s.dataDir(), "server.pid")); !os.IsNotExist(err) {
				t.Fatalf("refused selection started local: %v", err)
			}
			swarms := s.run("swarm", "list", "--json").json(t)
			if len(field(t, swarms, "swarms").([]any)) != 0 {
				t.Fatalf("refused selection wrote swarm: %v", swarms)
			}
		})
	}
}

func TestNewSwarmBootstrapsOnlySelectedLocalServer(t *testing.T) {
	t.Parallel()
	for _, choice := range []string{"default", "flag", "fresh", "fresh-legacy", "only-local"} {
		t.Run(choice, func(t *testing.T) {
			t.Parallel()
			s := newSwarmEnv(t)
			if choice == "default" || choice == "only-local" {
				s.run("up")
				if choice == "default" {
					s.run("servers", "use", "local")
				}
				s.run("down")
			}
			if choice == "fresh-legacy" {
				s.writeFileIn(filepath.Join(s.dir, ".aboard"), `{"server":{"url":"https://ignored.example"},"board":"remote-probe"}`)
			}
			s.writeBoardFile(serverSwarmFile)
			args := []string{"swarm", "up", "--json"}
			if choice == "flag" {
				args = append(args, "--server", "local")
			}
			v := s.run(args...).json(t)
			if v["server_started"] != true || field(t, v, "server.url") != "http://"+s.addr || agentsByName(t, v)["worker"]["seated"] != true {
				t.Fatalf("local bootstrap: %v", v)
			}
		})
	}
}

func TestNewSwarmNeverFallsBackFromUnreachableDefault(t *testing.T) {
	t.Parallel()
	tm := newTeam(t)
	s := newSwarmEnv(t)
	s.run("up")
	s.run("connect", tm.invite(), "--handle", "sam")
	s.run("servers", "use", tm.url())
	s.run("down")
	tm.admin.run("down")
	s.writeBoardFile(serverSwarmFile)
	r := s.runExit("swarm", "up", "--json")
	if r.code != 1 || errorCode(t, r.json(t)) != "server_unreachable" {
		t.Fatalf("unreachable default: %s", r)
	}
	if len(s.starts("")) != 0 {
		t.Fatal("unreachable default started an agent")
	}
	if _, err := os.Stat(filepath.Join(s.dataDir(), "server.pid")); !os.IsNotExist(err) {
		t.Fatalf("unreachable default started local: %v", err)
	}
}
