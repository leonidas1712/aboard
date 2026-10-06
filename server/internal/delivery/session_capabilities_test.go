package delivery_test

import (
	"slices"
	"testing"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

func TestAgentsReportsOnlyTheExtensionsCurrentLiveCapabilities(t *testing.T) {
	r := newRig(t)
	e, welcome := r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b1", Capabilities: []string{delivery.CapabilityHandoffV1}})
	if welcome.Error != nil {
		t.Fatal(welcome.Error)
	}
	query := func() delivery.Response {
		return r.ok(delivery.Request{Op: delivery.OpAgents, Harness: "omp", Session: "o1"})
	}
	if !slices.Contains(query().Capabilities, delivery.CapabilityHandoffV1) {
		t.Fatal("agents omitted the live extension's negotiated handoff capability")
	}
	_ = e.conn.Close()
	r.eventually("extension disconnected", 0, func() bool { return r.status().OpenSessions == 0 })
	if len(query().Capabilities) != 0 {
		t.Fatal("agents kept a capability after the extension disconnected")
	}
	_, welcome = r.connect(delivery.Request{Harness: "omp", Session: "o1", Boot: "b2"})
	if welcome.Error != nil {
		t.Fatal(welcome.Error)
	}
	if len(query().Capabilities) != 0 {
		t.Fatal("an old extension inherited an earlier connection's capability")
	}
}
