package apiserver

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A lost connection may still accept writes locally. Drop responses on the first
// connection to reproduce requests repeatedly timing out on a reused HTTP/2 socket.
type blackholeListener struct {
	net.Listener
	first atomic.Pointer[blackholeConn]
}
type blackholeConn struct {
	net.Conn
	drop atomic.Bool
}

func (l *blackholeListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	b := &blackholeConn{Conn: c}
	l.first.CompareAndSwap(nil, b)
	return b, nil
}
func (c *blackholeConn) Write(p []byte) (int, error) {
	if c.drop.Load() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func TestRequestsRecoverAfterAConnectionStopsResponding(t *testing.T) {
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			t.Errorf("protocol = %s, want HTTP/2", r.Proto)
		}
		_, _ = io.WriteString(w, "ok")
	}))
	listener := &blackholeListener{Listener: peer.Listener}
	peer.Listener = listener
	peer.EnableHTTP2 = true
	peer.StartTLS()
	t.Cleanup(peer.Close)
	// Preserve the same system-trust configuration that New clones in production.
	original := http.DefaultTransport
	base := original.(*http.Transport).Clone()
	roots := x509.NewCertPool()
	roots.AddCert(peer.Certificate())
	base.TLSClientConfig = peer.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	base.TLSClientConfig.InsecureSkipVerify = false
	base.TLSClientConfig.RootCAs = roots
	http.DefaultTransport = base
	t.Cleanup(func() { http.DefaultTransport = original; base.CloseIdleConnections() })
	srv := New(peer.URL, tokens{}, rand.Reader)
	t.Cleanup(srv.http.CloseIdleConnections)
	get := func(ctx context.Context) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, peer.URL, nil)
		if err != nil {
			return err
		}
		res, err := srv.http.Do(req)
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		_, err = io.Copy(io.Discard, res.Body)
		return err
	}
	if err := get(t.Context()); err != nil {
		t.Fatal(err)
	}
	listener.first.Load().drop.Store(true)
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if err := get(ctx); err == nil {
		t.Fatal("blackholed request unexpectedly succeeded")
	}
	ctx2, cancel2 := context.WithTimeout(t.Context(), time.Second)
	defer cancel2()
	if err := get(ctx2); err != nil {
		t.Fatalf("next request must use a fresh connection: %v", err)
	}
}
