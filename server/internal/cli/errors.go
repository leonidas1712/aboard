package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Exit codes shared by every command.
const (
	exitOK      = 0
	exitError   = 1
	exitUsage   = 2
	exitChecked = 3
)

// Error is a failure reported to the person or agent running a command. It has the same
// fields as an API error, so errors from the server pass through unchanged.
type Error struct {
	Code    string
	Message string
	Hint    string
	Details map[string]any
	// Exit is the process exit code; zero means exitError.
	Exit int
	// Usage is printed to stderr after the error, for usage errors.
	Usage string
	// Err is the underlying cause, if any. It is never shown to the user as is.
	Err error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Err }

func (e *Error) exitCode() int {
	if e.Exit == 0 {
		return exitError
	}
	return e.Exit
}

// newError returns an error with a code, a message saying what happened and a hint
// saying what to do next.
func newError(code, message, hint string) *Error {
	return &Error{Code: code, Message: message, Hint: hint}
}

// usageError reports bad flags or arguments; usage is the command's usage text.
func usageError(message, usage string) *Error {
	return &Error{
		Code: "invalid_request", Message: message,
		Hint: "Run aboard help to see the commands and their flags.", Exit: exitUsage, Usage: usage,
	}
}

// asError turns any error into an *Error, keeping the code of one that has it.
func asError(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return &Error{
		Code: "internal", Message: "Something went wrong: " + err.Error(),
		Hint: "Try again; if it keeps failing, run the command with --json and report the output.", Err: err,
	}
}

// wireError is the JSON error shape shared by the API and the CLI.
type wireError struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Hint    string         `json:"hint"`
		Details map[string]any `json:"details,omitempty"`
	} `json:"error"`
}

func (e *Error) wire() wireError {
	var w wireError
	w.Error.Code, w.Error.Message, w.Error.Hint, w.Error.Details = e.Code, e.Message, e.Hint, e.Details
	return w
}

// apiError reads the error the server sent with an unexpected status.
func apiError(status int, body []byte) *Error {
	var w wireError
	if err := json.Unmarshal(body, &w); err == nil && w.Error.Code != "" {
		return &Error{Code: w.Error.Code, Message: w.Error.Message, Hint: w.Error.Hint, Details: w.Error.Details}
	}
	return newError("internal",
		fmt.Sprintf("The server answered %d %s without an error description.", status, http.StatusText(status)),
		"Check that the address points at an Aboard server, and look at the server log.")
}
