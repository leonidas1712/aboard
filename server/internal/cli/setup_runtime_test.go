package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestSetupClearsHarnessActionOnlyForCurrentRuntimeConfirmation(t *testing.T) {
	for _, ready := range []string{"true", "false", "absent", "old-boot", "empty-boot", "read-failed"} {
		t.Run(ready, func(t *testing.T) {
			e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
			e.env["ABOARD_SESSION"] = "claude-code:setup-current"
			e.env["ABOARD_BOOT"] = "current-boot"
			if ready == "empty-boot" {
				e.env["ABOARD_BOOT"] = ""
			}
			if err := os.MkdirAll(filepath.Join(e.home, ".claude"), 0o700); err != nil {
				t.Fatal(err)
			}
			a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
			var reads atomic.Int32
			var currentBoot atomic.Pointer[string]
			initialBoot := "current-boot"
			currentBoot.Store(&initialBoot)
			var expectedBoot atomic.Pointer[string]
			initialCallerBoot := e.env["ABOARD_BOOT"]
			expectedBoot.Store(&initialCallerBoot)
			fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
				if req.Op != delivery.OpAgents || req.Harness != "claude-code" || req.Session != "setup-current" || req.Boot != *expectedBoot.Load() {
					t.Errorf("setup runtime read selected a different session: %+v", req)
				}
				reads.Add(1)
				raw := `{"v":1,"boot":"` + *currentBoot.Load() + `"}`
				switch ready {
				case "read-failed":
					return delivery.Response{V: delivery.ProtocolVersion, Error: &delivery.WireError{Code: "session_not_registered", Message: "No actual current runtime.", Hint: "Restart the harness."}}
				case "empty-boot":
					raw = `{"v":1,"boot":"` + *currentBoot.Load() + `","runtime_ready":true}`
				case "old-boot":
					raw = `{"v":1,"boot":"old-boot","runtime_ready":true}`
				case "absent":
				default:
					raw = `{"v":1,"boot":"` + *currentBoot.Load() + `","runtime_ready":` + ready + `}`
				}
				var resp delivery.Response
				if err := json.Unmarshal([]byte(raw), &resp); err != nil {
					t.Error(err)
				}
				return resp
			})
			exe := filepath.Join(e.home, "aboard")
			// Newly installed hooks still need trust, even if an older runtime reports ready.
			first, err := a.setupHarness(context.Background(), exe)
			if err != nil || first == nil || !strings.Contains(first.Resume, "/hooks") {
				t.Fatalf("new hooks lost their trust action: next=%v err=%v", first, err)
			}
			if ready == "true" || ready == "empty-boot" {
				restartedBoot := "restarted-boot"
				currentBoot.Store(&restartedBoot)
				if ready != "empty-boot" {
					e.env["ABOARD_BOOT"] = *currentBoot.Load()
					expectedBoot.Store(&restartedBoot)
				}
			}
			next, err := a.setupHarness(context.Background(), exe)
			if err != nil {
				t.Fatal(err)
			}
			if ready == "true" || ready == "empty-boot" {
				if next != nil {
					t.Fatalf("confirmed current runtime still asks for trust/restart: %+v", next)
				}
				out := newSetupOutput(serverRef{URL: "https://issuer.example"})
				out.confirmSetupHarness("aboard setup --continue")
				if out.Steps[3].State != "complete" || out.Steps[5].State != "pending" || out.State != "pending" || out.Next.Command != "aboard setup --continue" || !strings.Contains(out.Next.Resume, "Run aboard skill now") {
					t.Fatalf("runtime readiness lost the skill action or claimed delivery: %+v", out)
				}
				if reads.Load() == 0 {
					t.Fatal("setup did not read current runtime confirmation")
				}
			} else if next == nil || !strings.Contains(next.Resume, "restart Claude Code") {
				t.Fatalf("unconfirmed or old daemon runtime completed harness: next=%v", next)
			}
		})
	}
}

func TestSetupKeepsPendingHarnessAction(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	e.env["ABOARD_SESSION"] = "claude-code:setup-current"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	var accepts atomic.Int32
	fakeDaemonAnswering(t, a, func(req delivery.Request) delivery.Response {
		resp := delivery.Response{V: delivery.ProtocolVersion}
		if req.Op == delivery.OpPairing {
			accepts.Add(1)
			resp.Server = "https://issuer.example"
			resp.PairingBoard = "work"
			resp.Pairing = &delivery.PairingRequest{
				ID: "prq_original", ServerID: "srv_issuer", BoardID: "brd_work", State: "ready", Generation: 1, CreatedAt: "2026-10-10T00:00:00Z", ExpiresAt: "2026-10-11T00:00:00Z",
				Initiator: &delivery.PairingEndpoint{AgentID: "mem_first", Generation: 1, SessionBinding: "first-current"},
				Recipient: &delivery.PairingEndpoint{AgentID: "mem_second", Generation: 1, SessionBinding: "second-current"},
			}
		}
		return resp
	})
	out := newSetupOutput(serverRef{URL: "https://issuer.example"})
	out.continueCommand = "aboard setup --continue"
	out.Next = &api.NextStep{Command: "aboard init --harness claude-code", Resume: "Run /hooks, then restart Claude Code."}
	if err := a.continueSetupPairing(context.Background(), &out, "prq_original"); err != nil {
		t.Fatal(err)
	}
	if accepts.Load() != 0 || out.Steps[5].State != "pending" || out.Steps[3].State != "pending" || out.State != "pending" || !strings.Contains(out.Next.Resume, "/hooks") {
		t.Fatalf("setup selected an endpoint before the required restart: accepts=%d result=%+v", accepts.Load(), out)
	}
}
