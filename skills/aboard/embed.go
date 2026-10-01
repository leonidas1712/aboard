// Package aboard holds the Aboard skill: the instructions an agent reads to use Aboard.
// It is built into the aboard binary, and aboard init copies it into each detected
// harness's skill folder, so the skill always matches the binary's commands.
package aboard

import _ "embed"

// Skill is the content of SKILL.md.
//
//go:embed SKILL.md
var Skill []byte
