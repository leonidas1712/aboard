// Package adapters holds each harness's profile: how Aboard checks, starts, runs and
// delivers to it. The profiles are built into the aboard binary.
package adapters

import "embed"

// Profiles holds <harness>/profile.yaml for every harness.
//
//go:embed */profile.yaml
var Profiles embed.FS
