package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestSandboxedSetupWriteFailureHandsOffWithoutRetry(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	e.env["ABOARD_SESSION"] = "codex:setup-current"
	e.env["CODEX_SANDBOX"] = "seatbelt"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	err := &os.PathError{Op: "mkdir", Path: "private-state", Err: syscall.EPERM}
	got := a.setupWriteHandover(err)
	cause := asError(got)
	if !errors.Is(got, syscall.EPERM) || cause.Next == nil || cause.Next.Command != "aboard init --harness codex --allow-commands" || !strings.Contains(cause.Next.Resume, "do not use sudo") {
		t.Fatalf("write failure lost handover: %+v", cause)
	}
}

func TestInheritedSandboxMarkersDoNotRefuseReachableNetwork(t *testing.T) {
	e := lifecycleMachine(t, "https://issuer.example", "unused", agentCredential{})
	e.env["CODEX_SANDBOX"] = "seatbelt"
	e.env["CODEX_SANDBOX_NETWORK_DISABLED"] = "1"
	a := e.app(&bytes.Buffer{}, &bytes.Buffer{})
	if err := a.sandboxDaemonError(context.Background()); err != nil {
		t.Fatalf("inherited markers refused accessible loopback: %v", err)
	}
}
