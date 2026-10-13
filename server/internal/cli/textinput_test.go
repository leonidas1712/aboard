package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

type failedTextReader struct{ called bool }

func (r *failedTextReader) Read([]byte) (int, error) {
	r.called = true
	return 0, errors.New("test input failed")
}

func TestBadStdinFailsBeforeSelectingAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		args       []string
		exit       int
		read       bool
	}{
		{"read failure", "stdin_text_read_failed", []string{"say", "-"}, 1, true},
		{"ambiguous input", "stdin_text_ambiguous", []string{"task", "new", "-", "--about", "-"}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			home := t.TempDir()
			reader := &failedTextReader{}
			var stdout, stderr bytes.Buffer
			env := Env{Stdin: reader, Stdout: &stdout, Stderr: &stderr, Dir: home, Getenv: func(key string) string {
				switch key {
				case "HOME":
					return home
				case "ABOARD_HOME":
					return filepath.Join(home, "aboard")
				case "ABOARD_NO_UPDATE_CHECK":
					return "1"
				}
				return ""
			}}
			code := Run(context.Background(), append(tc.args, "--json"), env)
			var result struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if code != tc.exit || result.Error.Code != tc.code || reader.called != tc.read {
				t.Fatalf("exit=%d body=%s stderr=%s read=%t", code, stdout.String(), stderr.String(), reader.called)
			}
		})
	}
}
