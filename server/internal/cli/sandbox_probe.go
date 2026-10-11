package cli

import (
	"context"
	"net"
	"time"
)

func loopbackProbe(ctx context.Context) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = ln.Close() }()
	dialer := net.Dialer{Timeout: time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", ln.Addr().String())
	if conn != nil {
		_ = conn.Close()
	}
	return err
}
