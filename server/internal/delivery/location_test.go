package delivery_test

import (
	"context"
	"testing"
	"time"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestLocationReportsOnlyCurrentRootSessionBinding(t *testing.T) {
	reports := make(chan delivery.SessionLocation, 10)
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server { return &locationServer{Server: s, reports: reports} })
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "root", Boot: "current", Cwd: "/work/project"})
	r.bind("claude-code", "root", reviewer)
	select {
	case got := <-reports:
		if got.Harness != "claude-code" || got.SessionID != "root" || got.Folder != "/work/project" {
			t.Fatalf("location: %+v", got)
		}
	case <-time.After(within):
		t.Fatal("no location report")
	}
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "root", Boot: "old", Cwd: "/stale"})
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "root", Boot: "current", Subagent: "child", Cwd: "/child"})
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "root", Boot: "current", Cwd: "/work/new"})
	select {
	case got := <-reports:
		if got.Folder != "/work/new" {
			t.Fatalf("stale or subagent overwrote location: %+v", got)
		}
	case <-time.After(within):
		t.Fatal("no updated location report")
	}
}

type locationServer struct {
	delivery.Server
	reports chan delivery.SessionLocation
}

func (s *locationServer) ReportLocation(ctx context.Context, _ delivery.AgentRef, l delivery.SessionLocation) error {
	select {
	case s.reports <- l:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestLocationSurvivesDaemonRestartWithoutGuessingNewBootFolder(t *testing.T) {
	reports := make(chan delivery.SessionLocation, 20)
	r := newRigWithServer(t, func(s delivery.Server) delivery.Server { return &locationServer{Server: s, reports: reports} })
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "root", Boot: "boot", Cwd: "/work/known"})
	r.bind("claude-code", "root", reviewer)
	select {
	case <-reports:
	case <-time.After(within):
		t.Fatal("initial report missing")
	}
	r.stop()
	r.start()
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "root", Boot: "boot"})
	select {
	case got := <-reports:
		if got.Folder != "/work/known" {
			t.Fatalf("restored location: %+v", got)
		}
	case <-time.After(within):
		t.Fatal("restored report missing")
	}
	r.ok(delivery.Request{Op: delivery.OpRegister, Harness: "claude-code", Session: "root", Boot: "new-boot"})
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "root", Boot: "new-boot", Cwd: "relative"})
	r.ok(delivery.Request{Op: delivery.OpBoundary, Harness: "claude-code", Session: "root", Boot: "new-boot", Cwd: "/new/observed"})
	select {
	case got := <-reports:
		if got.Folder != "/new/observed" {
			t.Fatalf("new boot guessed old or relative location: %+v", got)
		}
	case <-time.After(within):
		t.Fatal("new observed report missing")
	}
}
