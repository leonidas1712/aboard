//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"testing"
	"time"
)

func TestCleanupDoesNotProbeAnotherHomesServer(t *testing.T) {
	t.Parallel()
	owner := newEnv(t)
	owner.run("up", "--json")
	unused := newEnv(t)
	unused.vars = append(unused.vars, "ABOARD_LOCAL_ADDR="+owner.addr)
	unused.stopServer()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+owner.addr+"/v1/info", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the other home's server stopped: HTTP %d", resp.StatusCode)
	}
}
