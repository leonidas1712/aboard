//go:build live

package live

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/leonidas1712/aboard/e2e/support"
)

// handSample is how long one delivery took: from its newest message's posting until the
// daemon began handing it over (Aboard's part), and from then until the session
// confirmed it (the harness's part).
type handSample struct {
	aboard, harness time.Duration
}

// measureHandovers adds every delivery the lab's daemon handed over and saw confirmed to
// the run's hand-over samples, by harness. It reads the daemon's log ("bundle handed"
// with when handing began, "bundle confirmed") and when each message was posted from the
// board's event log, with the person's login. A message posted while its session was
// busy waited for the turn's end; the median keeps those from deciding the typical value.
func (l *lab) measureHandovers() {
	confirmed := l.logged("bundle confirmed")
	posted := map[string]map[int]time.Time{}
	for _, h := range l.logged("bundle handed") {
		if h.Error != "" || len(h.Seqs) == 0 || h.Began.IsZero() || h.Board == "" {
			continue
		}
		if posted[h.Board] == nil {
			posted[h.Board] = l.postedTimes(h.Board)
		}
		var newest time.Time
		for _, seq := range h.Seqs {
			if at, ok := posted[h.Board][seq]; ok && at.After(newest) {
				newest = at
			}
		}
		i := slices.IndexFunc(confirmed, func(c handover) bool {
			return c.Session == h.Session && c.Board == h.Board && slices.Contains(c.Seqs, h.Seqs[0]) && !c.Time.Before(h.Began)
		})
		if newest.IsZero() || i < 0 {
			continue
		}
		harness, _, _ := strings.Cut(h.Session, ":")
		liveResults.Lock()
		liveResults.handovers[harness] = append(liveResults.handovers[harness],
			handSample{aboard: h.Began.Sub(newest), harness: confirmed[i].Time.Sub(h.Began)})
		liveResults.Unlock()
	}
}

// postedTimes returns when each message on a board was posted, by sequence number, from
// the board's event log, or nothing when it can't be read.
func (l *lab) postedTimes(board string) map[int]time.Time {
	out := map[int]time.Time{}
	token, err := os.ReadFile(filepath.Join(l.configDir(), "local-owner-token"))
	if err != nil {
		return out
	}
	for after := 0; ; {
		// t.Context() has ended by the time a test cleans up.
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet,
			"http://"+l.addr+"/v1/boards/"+board+"/events?limit=200&after="+strconv.Itoa(after), http.NoBody)
		if err != nil {
			return out
		}
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(token)))
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err != nil {
			return out
		}
		var page struct {
			Events []struct {
				Seq  int       `json:"seq"`
				Type string    `json:"type"`
				At   time.Time `json:"at"`
			} `json:"events"`
			NextAfter int `json:"next_after"`
		}
		err = json.NewDecoder(resp.Body).Decode(&page)
		_ = resp.Body.Close()
		if err != nil || len(page.Events) == 0 {
			return out
		}
		for _, e := range page.Events {
			if e.Type == "message.posted" {
				out[e.Seq] = e.At
			}
		}
		if page.NextAfter <= after {
			return out
		}
		after = page.NextAfter
	}
}

// typicalHandover is the median of a harness's samples, for support.json.
func typicalHandover(samples []handSample) *support.Handover {
	if len(samples) == 0 {
		return nil
	}
	median := func(part func(handSample) time.Duration) int64 {
		ds := make([]time.Duration, len(samples))
		for i, s := range samples {
			ds[i] = part(s)
		}
		slices.Sort(ds)
		return ds[len(ds)/2].Milliseconds()
	}
	return &support.Handover{
		AboardMS:   median(func(s handSample) time.Duration { return s.aboard }),
		HarnessMS:  median(func(s handSample) time.Duration { return s.harness }),
		Deliveries: len(samples),
		Date:       time.Now().Format(time.DateOnly),
	}
}
