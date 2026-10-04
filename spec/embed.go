// Package spec holds the contracts that aboard reads at run time: the board file's
// schema, which aboard swarm up checks board files against.
package spec

import _ "embed"

// BoardFileSchema is aboard.schema.json, the JSON Schema of aboard.yaml.
//
//go:embed aboard.schema.json
var BoardFileSchema []byte
