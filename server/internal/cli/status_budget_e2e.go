//go:build aboard_e2e

package cli

import "time"

// Only the e2e executable can allow more time for parallel replacement tests.
func statusBudget(getenv func(string) string) time.Duration {
	if getenv("ABOARD_E2E_STATUS_BUDGET") == "30s" {
		return 30 * time.Second
	}
	return 5 * time.Second
}
