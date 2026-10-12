package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
)

// statusReads retains timeouts from diagnostic reads that otherwise omit unavailable
// metadata. All status reads share the command's deadline, including daemon calls.
type statusReads struct{ timeouts map[string]string }

func (s *statusReads) record(server string, err error) {
	if s == nil || (!errors.Is(err, context.DeadlineExceeded) && !os.IsTimeout(err)) {
		return
	}
	s.timeouts[server] = "request timed out"
}

func (s *statusReads) failure() *Error {
	if len(s.timeouts) == 0 {
		return nil
	}
	servers := make([]string, 0, len(s.timeouts))
	for server := range s.timeouts {
		servers = append(servers, server)
	}
	slices.Sort(servers)
	lines := make([]string, 0, len(servers))
	for _, server := range servers {
		lines = append(lines, "could not reach "+server+": "+s.timeouts[server])
	}
	return newError("server_unreachable", strings.Join(lines, "; "), "Check the server connection, then run aboard status again.")
}

// statusHTTPClient observes both request and body timeouts, even when a diagnostic
// reader deliberately omits metadata it could not fetch.
type statusHTTPClient struct {
	client *http.Client
	reads  *statusReads
	server string
}

func (c *statusHTTPClient) Do(req *http.Request) (*http.Response, error) {
	response, err := c.client.Do(req) //nolint:gosec // CLI requests go to the selected issuer; redirects are disabled.
	c.reads.record(c.server, err)
	if response != nil && response.Body != nil {
		response.Body = &statusBody{ReadCloser: response.Body, reads: c.reads, server: c.server}
	}
	return response, err
}

type statusBody struct {
	io.ReadCloser
	reads  *statusReads
	server string
}

func (b *statusBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.reads.record(b.server, err)
	return n, err
}
