// Package retry spreads read reconnections after a server outage.
package retry

import (
	"crypto/rand"
	"encoding/binary"
	"time"
)

// Delay randomizes a bounded backoff without allowing a zero-delay retry burst.
func Delay(base time.Duration) time.Duration {
	var bits [8]byte
	if _, err := rand.Read(bits[:]); err != nil {
		return base
	}
	return jitter(base, float64(binary.LittleEndian.Uint64(bits[:])>>11)/(1<<53))
}

func jitter(base time.Duration, fraction float64) time.Duration {
	return base/2 + time.Duration(float64(base-base/2)*fraction)
}
