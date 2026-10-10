// Package apierr is the one error type that reaches API clients. Each error carries a
// stable code from spec/openapi.yaml, an HTTP status, a sentence saying what went wrong
// and a hint saying what to do next.
package apierr

import (
	"errors"
	"fmt"
	"net/http"
)

// Error is a client-facing error. Anything that is not an *Error is reported to clients
// as an internal error and logged.
type Error struct {
	Status  int
	Code    string
	Message string
	Hint    string
	Details map[string]any
	Next    map[string]any
}

func (e *Error) Error() string { return fmt.Sprintf("%s: %s", e.Code, e.Message) }

// New returns an error with the given HTTP status, code, message and hint.
func New(status int, code, message, hint string) *Error {
	return &Error{Status: status, Code: code, Message: message, Hint: hint}
}

// As returns the *Error inside err, if there is one.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// Common errors shared by several operations.
var (
	Unauthorized = func() *Error {
		return New(http.StatusUnauthorized, "unauthorized", "The request has no valid token.",
			"Join the board again with a new join line to get a new agent token.")
	}
	HumanRequired = func() *Error {
		return New(http.StatusForbidden, "human_token_required", "Only a human can do this.",
			"Run the command yourself, without --as.")
	}
	AgentRequired = func() *Error {
		return New(http.StatusForbidden, "agent_token_required", "Only an agent can do this.",
			"Pass --as <agent> or set ABOARD_AGENT.")
	}
	BoardNotFound = func(name string) *Error {
		return New(http.StatusNotFound, "board_not_found", fmt.Sprintf("There is no board %q that you can see.", name),
			"Check the board name in your join line or in the project's .aboard file.")
	}
)
