//go:build e2e

package e2e

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Refusals of the install script that protect a download in transit.

// Without curl the script refuses rather than fall back to wget, which follows a
// redirect from https to http: with cosign optional, that would let someone on the
// network replace both the archive and its checksums.
func TestInstallScriptNeedsCurl(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.existing()
	if err := os.Remove(filepath.Join(r.bin, "curl")); err != nil {
		t.Fatal(err)
	}
	called := filepath.Join(r.home, "wget-called")
	if err := os.WriteFile(filepath.Join(r.bin, "wget"), []byte("#!/bin/sh\n: >"+called+"\nexit 1\n"), 0o755); err != nil { //nolint:gosec // a fake command the script might run
		t.Fatal(err)
	}
	out, code := r.run()
	r.refused(out, code, "downloading needs curl")
	if _, err := os.Stat(called); err == nil {
		t.Fatal("the script ran wget")
	}
}

// A release server that redirects a download from https to http is refused.
func TestInstallScriptRefusesARedirectToHTTP(t *testing.T) {
	t.Parallel()
	r := newInstallRelease(t)
	r.existing()
	plain := strings.TrimSuffix(r.url, "/releases")
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.URL.Path, ".tar.gz") {
			http.Redirect(w, req, plain+req.URL.Path, http.StatusFound)
			return
		}
		r.mu.Lock()
		body, ok := r.files[req.URL.Path]
		r.mu.Unlock()
		if !ok {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(tls.Close)
	ca := filepath.Join(r.home, "ca.pem")
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: tls.Certificate().Raw})
	if err := os.WriteFile(ca, cert, 0o600); err != nil {
		t.Fatal(err)
	}
	out, code := r.run("ABOARD_DOWNLOAD_URL="+tls.URL+"/releases", "CURL_CA_BUNDLE="+ca, "SSL_CERT_FILE="+ca)
	// The checksums came over https; the archive's redirect to http was refused.
	r.refused(out, code, "couldn't download "+tls.URL+r.archivePath())
	if asked := r.requests(); len(asked) != 0 {
		t.Fatalf("the script followed the redirect to http: %v", asked)
	}
}
