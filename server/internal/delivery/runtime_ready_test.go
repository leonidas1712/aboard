package delivery_test

import (
	"bufio"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestRuntimeReadyRequiresCurrentObservedHookAndNotRestoredCLIState(t *testing.T) {
	r := newRig(t)
	req := func(op, boot string, hook bool) delivery.Request {
		return delivery.Request{Op: op, Harness: "codex", Session: "runtime", Boot: boot, RuntimeHook: hook}
	}
	ready := func(want bool) {
		t.Helper()
		if got := r.ok(req(delivery.OpAgents, "", false)).RuntimeReady; got != want {
			t.Fatalf("runtime_ready=%v want%v", got, want)
		}
	}
	r.ok(req(delivery.OpRegister, "", false))
	r.bind("codex", "runtime", reviewer)
	ready(false) // Binding may create a synthetic boot; it proves no harness hooks.
	r.ok(req(delivery.OpPrompt, "", false))
	ready(false)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "other", Boot: "b2", RuntimeHook: true})
	ready(false)
	r.ok(req(delivery.OpRegister, "b1", true))
	ready(true)
	r.ok(req(delivery.OpRegister, "b2", false))
	ready(false)
	r.ok(req(delivery.OpPrompt, "b1", true))
	ready(false) // A delayed old-boot hook cannot establish readiness.
	r.ok(req(delivery.OpRegister, "b2", false))
	r.ok(req(delivery.OpPrompt, "b2", true))
	ready(true)
	r.stop()
	r.start()
	ready(false) // Journal session/seenTurns is not a fresh runtime observation.
	r.ok(req(delivery.OpPrompt, "b2", true))
	ready(true)
	r.ok(req(delivery.OpEnd, "b2", false))
	ready(false)
	r.ok(req(delivery.OpRegister, "b2", false))
	ready(false)
}

func TestRuntimeReadyRequiresLiveExtensionOfCurrentBoot(t *testing.T) {
	r := newRig(t)
	e := r.hello("extensionboot", false)
	query := func(want bool) {
		t.Helper()
		resp := r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "omp", Session: "o1"})
		if resp.RuntimeReady != want {
			t.Fatalf("extension runtime_ready=%v want%v", resp.RuntimeReady, want)
		}
	}
	query(true)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "omp", Session: "o1", Boot: "differentboot"})
	query(false)
	e.send(delivery.Request{Op: delivery.OpEnd})
	e.next()
	query(false)
}

func TestRuntimeReadyObservesOnlyHookOperationsWithTheCurrentBoot(t *testing.T) {
	for _, op := range []string{delivery.OpPrompt, delivery.OpTurnStart, delivery.OpBoundary, delivery.OpTurnEnd} {
		t.Run(op, func(t *testing.T) {
			r := newRig(t)
			request := delivery.Request{Harness: "codex", Session: "runtime", Boot: "b1"}
			request.Op = delivery.OpRegister
			r.ok(request)
			request.Op = op
			request.RuntimeHook = true
			r.ok(request)
			request.Op = delivery.OpAgents
			request.RuntimeHook = false
			if !r.ok(request).RuntimeReady {
				t.Fatal("actual current-boot hook did not confirm runtime")
			}
		})
	}
	r := newRig(t)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "codex", Session: "runtime", Boot: "b1"})
	r.ok(delivery.Request{Op: delivery.OpPrompt, Harness: "codex", Session: "runtime", RuntimeHook: true})
	if r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "codex", Session: "runtime"}).RuntimeReady {
		t.Fatal("missing hook boot confirmed runtime")
	}
}

func TestRuntimeReadyObservesActualCurrentBootWaitHook(t *testing.T) {
	r := newRig(t)
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "wait-runtime", Boot: "b1"})
	conn := r.dial()
	defer func() { _ = conn.Close() }()
	if err := delivery.WriteFrame(conn, delivery.Request{V: delivery.ProtocolVersion, Op: delivery.OpWait, Harness: "claude-code", Session: "wait-runtime", Boot: "b1", RuntimeHook: true}); err != nil {
		t.Fatal(err)
	}
	var waiting delivery.Response
	if err := delivery.ReadFrame(bufio.NewReader(conn), &waiting); err != nil {
		t.Fatal(err)
	}
	if waiting.Event != delivery.EventWaiting {
		t.Fatalf("wait hook not accepted:%+v", waiting)
	}
	if !r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "claude-code", Session: "wait-runtime"}).RuntimeReady {
		t.Fatal("actual wait hook not observed")
	}
}
