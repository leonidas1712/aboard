package cli

import (
	"context"
	"errors"
	"io"
	"net"
	"syscall"
	"time"

	"github.com/leonidas1712/aboard/server/internal/api"
	"github.com/leonidas1712/aboard/server/internal/retry"
)

// waitInbox repeats only the waiting read, with the same client and original deadline.
func (c *client) waitInbox(ctx context.Context, deadline time.Time, params api.GetInboxParams) (*api.GetInboxResponse, error) {
	failures := 0
	for {
		remaining := time.Until(deadline)
		if remaining > 0 {
			params.Wait = ptrTo(int((remaining + time.Second - 1) / time.Second))
		}
		res, err := c.api.GetInboxWithResponse(ctx, &params)
		if err == nil || ctx.Err() != nil || !transientRead(err) || !time.Now().Before(deadline) {
			return res, err
		}
		failures++
		wait := min(retry.Delay(watchBackoff(failures)), time.Until(deadline))
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if !time.Now().Before(deadline) {
			return nil, err
		}
	}
}

func transientRead(err error) bool {
	var network net.Error
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) ||
		(errors.As(err, &network) && network.Timeout())
}
