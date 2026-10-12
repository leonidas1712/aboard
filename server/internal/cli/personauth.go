package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
)

// personAuthTransport supplies person recovery hints only when the client sends
// the exact saved person key for this issuer. It never retries or changes a token.
type personAuthTransport struct {
	base   http.RoundTripper
	server serverRef
}

func (t *personAuthTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err != nil || response.StatusCode != http.StatusUnauthorized {
		return response, err
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	var failure wireError
	if json.Unmarshal(body, &failure) == nil && failure.Error.Code != "" {
		if changed, err := json.Marshal(keyRejected(t.server, response.StatusCode, body).wire()); err == nil {
			body = changed
		}
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return response, nil
}
