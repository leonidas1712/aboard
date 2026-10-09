package delivery

import (
	"context"
	"io"
	"log/slog"
	"testing"
)

func TestLocationDropsQueuedReplacedBinding(t *testing.T) {
	agent := AgentRef{Server: "https://team.example", Board: "work", Name: "reviewer", MemberID: "mem_reviewer"}
	remote := &locationSink{}
	owner := &session{}
	owner.locationEpoch.Store(2)
	d := &Daemon{owners: map[AgentKey]*session{agent.Key(): owner}, generations: map[AgentKey]uint64{agent.Key(): 2}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	c := &serverConn{d: d, srv: remote, gone: map[AgentKey]bool{}}
	old := SessionLocation{Harness: "codex", SessionID: "old", Folder: "/old"}
	fresh := SessionLocation{Harness: "codex", SessionID: "new", Folder: "/new"}
	c.reportLocations(t.Context(), []srvMsg{{locationAgent: &agent, location: &old, generation: 1, locationOwner: owner, locationEpoch: 2}})
	if len(remote.got) != 0 {
		t.Fatal("replaced binding sent its location")
	}
	c.reportLocations(t.Context(), []srvMsg{{locationAgent: &agent, location: &old, generation: 2, locationOwner: owner, locationEpoch: 1}})
	if len(remote.got) != 0 {
		t.Fatal("old session boot sent its queued location")
	}
	c.reportLocations(t.Context(), []srvMsg{{locationAgent: &agent, location: &fresh, generation: 2, locationOwner: owner, locationEpoch: 2}})
	if len(remote.got) != 1 || remote.got[0] != fresh {
		t.Fatalf("current location: %+v", remote.got)
	}
	owner.locationEpoch.Add(1)
	c.reportLocations(t.Context(), []srvMsg{{locationAgent: &agent, location: &fresh, generation: 2, locationOwner: owner, locationEpoch: 2}})
	if len(remote.got) != 1 {
		t.Fatal("closed session sent its queued report")
	}
}

type locationSink struct {
	Server
	got []SessionLocation
}

func (s *locationSink) ReportLocation(_ context.Context, _ AgentRef, l SessionLocation) error {
	s.got = append(s.got, l)
	return nil
}
