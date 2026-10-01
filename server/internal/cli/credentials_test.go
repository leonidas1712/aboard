package cli

import (
	"fmt"
	"sync"
	"testing"
)

// Sessions on one machine often join at the same moment; none of their tokens may be
// lost.
func TestConcurrentJoinsKeepEveryCredential(t *testing.T) {
	home := t.TempDir()
	a := &app{env: Env{Getenv: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}}}
	const n = 20
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- a.saveCredential(agentCredential{Server: "http://127.0.0.1:7400", Board: "b", Name: fmt.Sprintf("agent-%02d", i), Token: "aba_x"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	c, err := a.readCredentials()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(c.names("http://127.0.0.1:7400", "b")); got != n {
		t.Fatalf("%d of %d credentials kept", got, n)
	}
}
