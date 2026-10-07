package mention

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var taskReference = regexp.MustCompile(`(?i)[a-z][a-z0-9]{1,5}-[1-9][0-9]*`)

// Tasks returns task references written outside code and links.
func Tasks(body string) []string {
	skip := skipped(body)
	var out []string
	for _, loc := range taskReference.FindAllStringIndex(body, -1) {
		if skip[loc[0]] || !startsWord(body[:loc[0]]) {
			continue
		}
		if loc[1] < len(body) {
			r, _ := utf8.DecodeRuneInString(body[loc[1]:])
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' {
				continue
			}
		}
		out = append(out, strings.ToUpper(body[loc[0]:loc[1]]))
	}
	return out
}
