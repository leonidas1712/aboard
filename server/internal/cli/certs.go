package cli

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
)

// TrustCertFile makes every HTTPS request this process sends trust the PEM
// certificates in SSL_CERT_FILE as well as the system's roots, on every platform: Go
// reads that variable on Linux but not on macOS. A team server whose certificate comes
// from a team's own authority is then reachable from either (D199). With the variable
// unset it changes nothing. Call it once, before any request.
func TrustCertFile(getenv func(string) string) error {
	path := getenv("SSL_CERT_FILE")
	if path == "" {
		return nil
	}
	pem, err := os.ReadFile(path) //nolint:gosec // a file the person named for this purpose
	if err != nil {
		return fmt.Errorf("read SSL_CERT_FILE: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return errors.New("SSL_CERT_FILE holds no PEM certificate")
	}
	t, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return errors.New("the default HTTP transport can't be configured")
	}
	if t.TLSClientConfig == nil {
		t.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	t.TLSClientConfig.RootCAs = pool
	return nil
}
