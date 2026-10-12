//go:build !aboard_e2e

package cli

import "time"

func statusBudget(_ func(string) string) time.Duration {
	return 5 * time.Second
}
