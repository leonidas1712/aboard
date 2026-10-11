//go:build e2e

package e2e

import (
	"net"
	"testing"
)

func TestFreeAddressesNeverReuseAnIssuedPort(t *testing.T) {
	first := freeAddr(t)
	calls := 0
	second := freeAddrFrom(t, func() (net.Listener, error) {
		calls++
		if calls == 1 {
			return net.Listen("tcp", first)
		}
		return net.Listen("tcp", "127.0.0.1:0")
	})
	if second == first {
		t.Fatal("another fixture received the first fixture's not-yet-started server address")
	}
}
