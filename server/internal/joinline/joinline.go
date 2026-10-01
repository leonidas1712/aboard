// Package joinline writes and reads join lines: the plain-language sentence a person
// pastes into a session so its agent joins a board, such as
// "Join Aboard board docs on localhost as reviewer with code 7Q4-K2M".
package joinline

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/ids"
)

// Line is the information a join line carries.
type Line struct {
	Board  string
	Server string // host, with a port when it isn't the default
	Role   string
	Code   string
}

// String returns the sentence form.
func (l Line) String() string {
	return fmt.Sprintf("Join Aboard board %s on %s as %s with code %s", l.Board, l.Server, l.Role, l.Code)
}

var pattern = regexp.MustCompile(`(?i)join aboard board ([a-z0-9-]+) on ([a-z0-9.:\[\]-]+?) as ([a-z0-9-]+) with code ([0-9a-z]{3}-?[0-9a-z]{3})\b`)

// Parse finds a join line inside text, which may contain other words around it.
func Parse(text string) (Line, error) {
	m := pattern.FindStringSubmatch(text)
	if m == nil {
		return Line{}, fmt.Errorf("no join line found; expected text like %q", Line{"docs", "localhost", "reviewer", "7Q4-K2M"}.String())
	}
	code, ok := ids.NormalizeJoinCode(m[4])
	if !ok {
		return Line{}, fmt.Errorf("%q is not a valid join code", m[4])
	}
	return Line{
		Board:  strings.ToLower(m[1]),
		Server: strings.ToLower(strings.TrimRight(m[2], ".")),
		Role:   strings.ToLower(m[3]),
		Code:   code,
	}, nil
}
