//go:build live

package live

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The person answers through the board view; the idle native agent must receive the
// recorded decision and act on it without another prompt from its owner.
func TestAskAnsweredInTheBoardViewWakesTheAsker(t *testing.T) {
	eachHarness(t, "AskAnsweredInTheBoardViewWakesTheAsker", func(t *testing.T, d *driver, rec *recorder) {
		if !d.p.Delivers() {
			rec.notApplicable("no automatic delivery")
		}
		binary := filepath.Join(t.TempDir(), "aboard")
		build := command(t.Context(), "go", "build", "-tags", "ui", "-o", binary, "./server/cmd/aboard")
		build.Dir = repoRoot
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build the board view for native asks: %v\n%s", err, out)
		}
		l := newLabWith(t, binary)
		d.setUp(l)
		board := l.pairCLI()
		writer := d.start(l, "writer", l.project("project", d.p.Harness))
		writer.bind("writer")
		const question = "Which native ask path should I check?"
		const chosen = "Check the second path"
		writer.submit(fmt.Sprintf("Use the Aboard skill to ask your person %q with options %q and %q. Make it a blocking ask, then end your turn. When the answer arrives, send @reviewer exactly ASK-ANSWER-RECEIVED and end your turn. Do nothing else.", question, "Check the first path", chosen))
		var ask struct {
			Seq int `json:"seq"`
			Ask *struct {
				Blocking bool   `json:"blocking"`
				State    string `json:"state"`
				To       struct {
					Kind string `json:"kind"`
				} `json:"to"`
			} `json:"ask"`
		}
		l.waitFor(3*time.Minute, "a blocking ask to the agent's person", func() bool {
			var page struct {
				Asks []json.RawMessage `json:"asks"`
			}
			l.ownerModeRequest(http.MethodGet, "/v1/asks?from_me=false&state=open&board="+url.QueryEscape(board), "", &page)
			for _, raw := range page.Asks {
				var m struct {
					Body string `json:"body"`
				}
				// The skill asks for two lines (what the agent did, then what it needs), so
				// the question is one line of the body, not all of it.
				if json.Unmarshal(raw, &m) == nil && strings.Contains(m.Body, question) {
					return json.Unmarshal(raw, &ask) == nil && ask.Ask != nil
				}
			}
			return false
		})
		if !ask.Ask.Blocking || ask.Ask.State != "open" || ask.Ask.To.Kind != "human" {
			t.Fatal("native ask did not record its blocking person recipient")
		}
		writer.waitIdle(2 * time.Minute)
		l.answerAskInBrowser(board, chosen)
		ack := l.waitMessage("writer", time.Time{}, "ASK-ANSWER-RECEIVED", 3*time.Minute)
		var page struct {
			Asks []struct {
				Seq int `json:"seq"`
				Ask struct {
					State        string `json:"state"`
					AnswerOption *int   `json:"answer_option"`
					AnswerSeq    *int   `json:"answer_seq"`
				} `json:"ask"`
			} `json:"asks"`
		}
		l.ownerModeRequest(http.MethodGet, "/v1/asks?state=all&board="+url.QueryEscape(board), "", &page)
		found := false
		for _, m := range page.Asks {
			if m.Seq == ask.Seq {
				found = m.Ask.State == "answered" && m.Ask.AnswerOption != nil && *m.Ask.AnswerOption == 2 && m.Ask.AnswerSeq != nil && *m.Ask.AnswerSeq < ack.Seq
			}
		}
		if !found {
			t.Fatal("the browser's option answer was not recorded before the agent acted")
		}
		writer.waitIdle(2 * time.Minute)
	})
}

func (l *lab) answerAskInBrowser(board, option string) {
	l.t.Helper()
	ctx, cancel := context.WithTimeout(l.t.Context(), 90*time.Second)
	defer cancel()
	opener := command(ctx, l.bin, "open", "--board", board, "--json")
	opener.Dir = l.human
	opener.Env = append(append([]string{}, l.vars...), "BROWSER=true")
	raw, err := opener.Output()
	if err != nil {
		l.t.Fatal("could not issue the isolated browser login")
	}
	var opened struct {
		URL string `json:"url"`
	}
	if json.Unmarshal(raw, &opened) != nil || opened.URL == "" {
		l.t.Fatal("browser login returned no link")
	}
	input, err := json.Marshal(map[string]string{"url": opened.URL, "option": option})
	if err != nil {
		l.t.Fatal("encode the private browser input")
	}
	cmd := command(ctx, "node", filepath.Join("..", "e2e", "live", "answer-ask.cjs"))
	cmd.Dir = filepath.Join(repoRoot, "web")
	cache := os.Getenv("PLAYWRIGHT_BROWSERS_PATH")
	if cache == "" {
		cache = playwrightCache()
	}
	if cache == "" {
		l.t.Fatal("find the installed Chromium cache: set PLAYWRIGHT_BROWSERS_PATH, or run npx playwright install chromium in web/")
	}
	// The installed browser is read-only; its profile and every process home remain isolated.
	cmd.Env = append(append([]string{}, l.vars...), "PLAYWRIGHT_BROWSERS_PATH="+cache)
	cmd.Stdin = strings.NewReader(string(input))
	if err := cmd.Run(); err != nil {
		l.t.Fatalf("answer the ask through the board view: %v", err)
	}
}

// playwrightCache finds where Playwright installed its browsers. A run may set HOME to a
// scratch folder, which moves os.UserCacheDir there, so it also looks under the account's
// home from the user database. It only reads that folder.
func playwrightCache() string {
	var dirs []string
	if base, err := os.UserCacheDir(); err == nil {
		dirs = append(dirs, filepath.Join(base, "ms-playwright"))
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if runtime.GOOS == "darwin" {
			dirs = append(dirs, filepath.Join(u.HomeDir, "Library", "Caches", "ms-playwright"))
		} else {
			dirs = append(dirs, filepath.Join(u.HomeDir, ".cache", "ms-playwright"))
		}
	}
	for _, dir := range dirs {
		if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
			return dir
		}
	}
	return ""
}
