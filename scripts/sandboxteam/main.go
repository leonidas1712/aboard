// Command sandboxteam runs a loopback HTTPS proxy and a real team server for local QA.
// It keeps its certificate, bootstrap key and process control inside the team's folder.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: sandboxteam start|serve|stop <team folder> <aboard binary>")
		os.Exit(2)
	}
	if err := run(os.Args[1], os.Args[2], os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func control(dir string) *http.Client {
	return &http.Client{Timeout: time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(dir, "control.sock"))
	}}}
}

func running(dir string) bool {
	req, e := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://sandbox/health", http.NoBody)
	if e != nil {
		return false
	}
	r, e := control(dir).Do(req)
	if e != nil {
		return false
	}
	_ = r.Body.Close()
	return r.StatusCode == 200
}

// The developer chooses these filesystem paths and binary; no network input reaches them.
//
//nolint:gosec // local dev-tool paths and executable arguments
func run(action, dir, bin string) error {
	switch action {
	case "stop":
		if !running(dir) {
			return nil
		}
		req, e := http.NewRequestWithContext(context.Background(), http.MethodPost, "http://sandbox/stop", http.NoBody)
		if e != nil {
			return e
		}
		r, e := control(dir).Do(req)
		if e != nil {
			return e
		}
		_ = r.Body.Close()
		deadline := time.Now().Add(10 * time.Second)
		for running(dir) {
			if time.Now().After(deadline) {
				return errors.New("team server did not stop")
			}
			time.Sleep(25 * time.Millisecond)
		}
		return nil
	case "start":
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		lock, err := os.OpenFile(filepath.Join(dir, "start.lock"), os.O_CREATE|os.O_RDWR, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = lock.Close() }()
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			return err
		}
		defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) //nolint:errcheck // closing releases the lock too
		if running(dir) {
			return nil
		}
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		log, err := os.OpenFile(filepath.Join(dir, "proxy.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer func() { _ = log.Close() }()
		cmd := exec.CommandContext(context.Background(), exe, "serve", dir, bin)
		cmd.Stdout, cmd.Stderr = log, log
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case err = <-done:
				return fmt.Errorf("team startup failed (%w); see %s", err, filepath.Join(dir, "proxy.log"))
			case <-deadline.C:
				_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
				<-done
				return errors.New("team startup timed out; see proxy.log")
			case <-tick.C:
				if running(dir) {
					return nil
				}
			}
		}
	case "serve":
		return serve(dir, bin)
	default:
		return errors.New("unknown team action")
	}
}

//nolint:gosec // private paths and dev binary supplied by the local developer
func serve(dir, bin string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var backend atomic.Pointer[url.URL]
	proxy := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		target := backend.Load()
		if target == nil {
			target = &url.URL{Scheme: "http", Host: "127.0.0.1:1"}
		}
		r.SetURL(target)
		r.Out.Host = r.In.Host
	}, FlushInterval: -1})
	if raw, err := os.ReadFile(filepath.Join(dir, "url")); err == nil {
		previous, err := url.Parse(strings.TrimSpace(string(raw)))
		if err != nil || previous.Hostname() != "127.0.0.1" || previous.Scheme != "https" {
			return errors.New("invalid saved loopback team URL")
		}
		_ = proxy.Listener.Close()
		proxy.Listener, err = (&net.ListenConfig{}).Listen(ctx, "tcp", previous.Host)
		if err != nil {
			return fmt.Errorf("reuse team address: %w", err)
		}
	}
	pair, err := certificate(dir)
	if err != nil {
		_ = proxy.Listener.Close()
		return err
	}
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{pair}}
	proxy.StartTLS()
	defer proxy.Close()
	cert := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw})
	if err := os.WriteFile(filepath.Join(dir, "cert.pem"), cert, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(dir, "home"), 0o700); err != nil {
		return err
	}
	data := filepath.Join(dir, "data")
	log, err := os.OpenFile(filepath.Join(dir, "server.log"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = log.Close() }()
	cmd := exec.CommandContext(ctx, bin, "serve", "--team")
	cmd.Cancel = func() error { return cmd.Process.Signal(syscall.SIGTERM) }
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = []string{"HOME=" + filepath.Join(dir, "home"), "PATH=" + os.Getenv("PATH"), "ABOARD_HOME=" + filepath.Join(dir, "aboard-home"), "ABOARD_ADMIN=leo", "ABOARD_DATA=" + data, "ABOARD_LISTEN=127.0.0.1:0", "ABOARD_PUBLIC_URL=" + proxy.URL}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		return err
	}
	waited := false
	defer func() {
		cancel()
		if !waited {
			_ = cmd.Wait()
		}
	}()
	scan := bufio.NewScanner(stdout)
	for scan.Scan() {
		line := scan.Bytes()
		_, _ = log.Write(append(bytes.Clone(line), '\n'))
		var entry struct{ Msg, Addr string }
		if json.Unmarshal(line, &entry) == nil && entry.Msg == "serving" && entry.Addr != "" {
			backend.Store(&url.URL{Scheme: "http", Host: entry.Addr})
			break
		}
	}
	if backend.Load() == nil {
		return fmt.Errorf("team server did not start; see %s", log.Name())
	}
	go func() {
		for scan.Scan() {
			_, _ = log.Write(append(bytes.Clone(scan.Bytes()), '\n'))
		}
		cancel()
	}()
	if err := os.WriteFile(filepath.Join(dir, "url"), []byte(proxy.URL+"\n"), 0o600); err != nil {
		return err
	}
	socket := filepath.Join(dir, "control.sock")
	_ = os.Remove(socket)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "unix", socket)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(socket) }()
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("/stop", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			w.WriteHeader(405)
			return
		}
		w.WriteHeader(200)
		cancel()
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = srv.Serve(ln) }()
	<-ctx.Done()
	cancel()
	_ = cmd.Wait()
	waited = true
	_ = srv.Close()
	return nil
}

// certificate keeps the team's self-signed key private and stable across restarts.
//
//nolint:gosec // private dev-team paths chosen locally
func certificate(dir string) (tls.Certificate, error) {
	certPath, keyPath := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "tls-key.pem")
	if _, err := os.Stat(certPath); err == nil {
		return tls.LoadX509KeyPair(certPath, keyPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return tls.Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	template := &x509.Certificate{SerialNumber: serial, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(365 * 24 * time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}, BasicConstraintsValid: true, IsCA: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(certPath, keyPath)
}
