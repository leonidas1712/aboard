package proctable

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestProcessArgsExcludeEnvironmentAndKeepArgumentBoundaries(t *testing.T) {
	raw := make([]byte, 4)
	binary.NativeEndian.PutUint32(raw, 4)
	raw = append(raw, []byte("/bin/codex\x00\x00\x00codex\x00--no-daemon\x00resume\x00a prompt with spaces\x00SECRET=not-an-argument\x00")...)
	args, ok := processArgs(raw)
	if !ok || !slices.Equal(args, []string{"codex", "--no-daemon", "resume", "a prompt with spaces"}) {
		t.Fatalf("argv: %q, available: %v", args, ok)
	}
}
