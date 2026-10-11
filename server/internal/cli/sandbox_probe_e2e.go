//go:build aboard_e2e

package cli

import (
	"context"
	"syscall"
)

// Only the e2e executable can simulate a denied loopback probe.
func sandboxNetworkProbe(ctx context.Context, getenv func(string) string) error {
	if getenv("ABOARD_E2E_DENY_LOOPBACK") == "1" {
		return syscall.EPERM
	}
	return loopbackProbe(ctx)
}
