// Package adapters holds each harness's profile: how Aboard checks, starts, runs and
// delivers to it, and the code Aboard installs inside a harness that needs some, such
// as omp's extension. Both are built into the aboard binary.
package adapters

import "embed"

// Profiles holds <harness>/profile.yaml for every harness.
//
//go:embed */profile.yaml
var Profiles embed.FS

// Files holds the files aboard init installs inside harnesses, named by an install
// item's source: omp's extension.
//
//go:embed omp/aboard.ts
var Files embed.FS
