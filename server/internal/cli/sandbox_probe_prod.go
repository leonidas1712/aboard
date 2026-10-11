//go:build !aboard_e2e

package cli

import "context"

func sandboxNetworkProbe(ctx context.Context, _ func(string) string) error {
	return loopbackProbe(ctx)
}
