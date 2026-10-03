package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/boardfile"
)

// outdatedServer is the CLI's HTTP transport. When the server says it lacks something
// this aboard knows (an operation in the spec, or a built-in template), it asks the
// server for its build, and if that build is older than this aboard's or can't be
// read, it turns the answer into server_outdated with the way to fix it. Every command
// gets this through its client, so no command handles it on its own.
type outdatedServer struct {
	base  http.RoundTripper
	srv   serverRef
	local bool // srv is this machine's local server
}

func (t *outdatedServer) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil || req.URL.Path == "/v1/info" || !mayBeMissing(resp.StatusCode) {
		return resp, err
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		return nil, err
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	if !t.lacksKnownThing(req, body) || !t.olderOrUnknown(req) {
		return resp, nil
	}
	out, err := json.Marshal(t.outdated().wire())
	if err != nil {
		return resp, nil //nolint:nilerr // keep the server's own answer
	}
	resp.Body = io.NopCloser(bytes.NewReader(out))
	resp.ContentLength = int64(len(out))
	resp.Header.Del("Content-Length")
	return resp, nil
}

func mayBeMissing(status int) bool {
	return status == http.StatusNotFound || status == http.StatusNotImplemented ||
		status == http.StatusUnprocessableEntity
}

// lacksKnownThing reports an answer saying the server lacks an operation, or a template
// that is built into this aboard.
func (t *outdatedServer) lacksKnownThing(req *http.Request, body []byte) bool {
	var w wireError
	if json.Unmarshal(body, &w) != nil {
		return false
	}
	switch w.Error.Code {
	case "not_found", "not_implemented":
		return true
	case "template_not_found":
		name := requestTemplate(req)
		if name == "" {
			return false
		}
		_, err := boardfile.Template(name)
		return err == nil
	}
	return false
}

// requestTemplate returns the template a request body names, or "".
func requestTemplate(req *http.Request) string {
	if req.GetBody == nil {
		return ""
	}
	r, err := req.GetBody()
	if err != nil {
		return ""
	}
	defer func() { _ = r.Close() }()
	var b struct {
		Template string `json:"template"`
	}
	if json.NewDecoder(r).Decode(&b) != nil {
		return ""
	}
	return b.Template
}

// olderOrUnknown reports a server whose build is older than this aboard's, or that
// doesn't say which build it is.
func (t *outdatedServer) olderOrUnknown(req *http.Request) bool {
	ireq, err := http.NewRequestWithContext(req.Context(), http.MethodGet,
		strings.TrimSuffix(t.srv.URL, "/")+"/v1/info", http.NoBody)
	if err != nil {
		return true
	}
	resp, err := (&http.Client{Transport: t.base, Timeout: 2 * time.Second}).Do(ireq)
	if err != nil {
		return true
	}
	defer func() { _ = resp.Body.Close() }()
	var info api.ServerInfo
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&info) != nil {
		return true
	}
	return compareBuilds(infoBuild(&info), currentBuild()) < 0
}

func (t *outdatedServer) outdated() *Error {
	if t.local {
		return newError("server_outdated",
			"The local server is from an older aboard and doesn't know this command.",
			"Run aboard down, then run this command again; it starts the current server.")
	}
	return newError("server_outdated",
		"The server at "+t.srv.URL+" is from an older aboard and doesn't know this command.",
		"Ask whoever runs that server to upgrade it to aboard "+version+" or later, then run this command again.")
}
