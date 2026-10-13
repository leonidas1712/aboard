package cli

import (
	"io"
	"unicode/utf8"
)

const stdinTextLimit = 1024 * 1024

// textFromStdin expands one exact dash in free-text fields, before a command writes.
func (a *app) textFromStdin(fields ...*string) error {
	var target *string
	for _, field := range fields {
		if field == nil || *field != "-" {
			continue
		}
		if target != nil {
			e := newError("stdin_text_ambiguous", "More than one text field reads from stdin.", "Use - for one field and pass the other text as arguments.")
			e.Exit = exitUsage
			return e
		}
		target = field
	}
	if target == nil {
		return nil
	}
	if a.env.Stdin == nil {
		return newError("stdin_text_read_failed", "Could not read text from stdin.", "Pipe the text into this command, or pass it as an argument.")
	}
	data, err := io.ReadAll(io.LimitReader(a.env.Stdin, stdinTextLimit+1))
	if err != nil {
		return &Error{Code: "stdin_text_read_failed", Message: "Could not read text from stdin.", Hint: "Pipe the text into this command, or pass it as an argument.", Err: err}
	}
	if len(data) > stdinTextLimit {
		return newError("stdin_text_too_large", "Text from stdin exceeds 1 MiB.", "Use shorter text, or upload it with aboard file put.")
	}
	if !utf8.Valid(data) {
		return newError("stdin_text_invalid_utf8", "Text from stdin is not valid UTF-8.", "Use UTF-8 text, or upload the original bytes with aboard file put.")
	}
	*target = string(data)
	return nil
}
