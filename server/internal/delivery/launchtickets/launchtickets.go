// Package launchtickets keeps launch tickets as files in a folder of Aboard's state:
// aboard swarm up writes one for each session it starts, and the delivery daemon takes
// it when the session first reports in, binding the session to the ticket's agent. A
// ticket works once; the file holds no token, only which agent it names.
package launchtickets

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"

	"github.com/leonidas1712/aboard/server/internal/delivery"
)

// Env is the variable a launcher puts a ticket in.
const Env = "ABOARD_LAUNCH"

// pattern is what a ticket looks like. Anything else is never read as a file name.
var pattern = regexp.MustCompile(`^lch_[0-9a-f]{24}$`)

// Valid reports whether s has a ticket's shape.
func Valid(s string) bool { return pattern.MatchString(s) }

// Dir is the folder that holds the tickets.
type Dir string

var _ delivery.Tickets = Dir("")

func (d Dir) path(ticket string) string { return filepath.Join(string(d), ticket+".json") }

// Write makes a new ticket for agent and returns it.
func (d Dir) Write(rnd io.Reader, agent delivery.AgentRef) (string, error) {
	b := make([]byte, 12)
	if _, err := io.ReadFull(rnd, b); err != nil {
		return "", fmt.Errorf("read randomness for a launch ticket: %w", err)
	}
	ticket := "lch_" + hex.EncodeToString(b)
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return "", fmt.Errorf("make the launch tickets folder: %w", err)
	}
	raw, err := json.Marshal(agent)
	if err != nil {
		return "", fmt.Errorf("encode a launch ticket: %w", err)
	}
	if err := os.WriteFile(d.path(ticket), raw, 0o600); err != nil {
		return "", fmt.Errorf("write a launch ticket: %w", err)
	}
	return ticket, nil
}

// Exists reports whether a ticket is still waiting to be taken.
func (d Dir) Exists(ticket string) bool {
	if !Valid(ticket) {
		return false
	}
	_, err := os.Stat(d.path(ticket))
	return err == nil
}

// Remove deletes a ticket nobody took.
func (d Dir) Remove(ticket string) error {
	if !Valid(ticket) {
		return nil
	}
	if err := os.Remove(d.path(ticket)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove a launch ticket: %w", err)
	}
	return nil
}

// Take returns the agent a ticket names and removes the ticket. The ticket is first
// renamed, which only one taker can do, so two sessions handing in the same ticket at
// once can't both take it.
func (d Dir) Take(ticket string) (delivery.AgentRef, bool, error) {
	if !Valid(ticket) {
		return delivery.AgentRef{}, false, nil
	}
	taken := d.path(ticket) + ".taken"
	if err := os.Rename(d.path(ticket), taken); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return delivery.AgentRef{}, false, nil
		}
		return delivery.AgentRef{}, false, fmt.Errorf("take a launch ticket: %w", err)
	}
	raw, err := os.ReadFile(filepath.Clean(taken))
	_ = os.Remove(taken)
	if err != nil {
		return delivery.AgentRef{}, false, fmt.Errorf("read a launch ticket: %w", err)
	}
	var agent delivery.AgentRef
	if err := json.Unmarshal(raw, &agent); err != nil || agent.Name == "" || agent.Board == "" || agent.Server == "" {
		return delivery.AgentRef{}, false, fmt.Errorf("read a launch ticket: it names no agent")
	}
	return agent, true, nil
}
