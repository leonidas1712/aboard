package delivery

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/leonidas1712/aboard/server/internal/deliverytext"
)

// A register's task note is a bounded read, never a queue offer or a wake. Failure leaves
// registration usable and avoids repeating task state cached under an earlier token.
func (s *session) taskStartNote(ctx context.Context, note string) string {
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	refs := s.agentRefs()
	for _, ref := range refs {
		a := s.agents[ref.Key()]
		if a == nil || a.gone() {
			continue
		}
		provider, ok := s.d.server(ref.Server).srv.(TaskWorkServer)
		if !ok {
			continue
		}
		work, e := provider.TaskWork(readCtx, ref)
		if e != nil || work == nil {
			continue
		}
		textContext := s.textContext(ref)
		if len(refs) > 1 {
			textContext.BoardQualified = true
		}
		text := deliverytext.ReorientTask(*work, s.now(), ref.Board, textContext)
		if text == "" {
			continue
		}
		if note != "" {
			note += "\n"
		}
		note += text
		if len(note) >= 600 {
			note = note[:600]
			for !utf8.ValidString(note) {
				note = note[:len(note)-1]
			}
			break
		}
		if readCtx.Err() != nil {
			break
		}
	}
	return strings.TrimSpace(note)
}

func (s *session) briefStartNudge(ctx context.Context) string {
	if s.d.cfg.AllowBriefNudge == nil {
		return ""
	}
	readCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	text := ""
	refs := s.agentRefs()
	for _, ref := range refs {
		a := s.agents[ref.Key()]
		if a == nil || a.gone() {
			continue
		}
		provider, ok := s.d.server(ref.Server).srv.(TaskWorkServer)
		if !ok {
			continue
		}
		work, err := provider.TaskWork(readCtx, ref)
		if err != nil || work == nil {
			continue
		}
		dc := s.textContext(ref)
		dc.BoardQualified = len(refs) > 1
		n := deliverytext.BriefStale(*work, s.now(), ref.Board, dc)
		if n == nil {
			continue
		}
		if len(text)+len(n.Text)+1 > 600 {
			break
		}
		if !s.d.cfg.AllowBriefNudge(ref, *work.Brief) {
			continue
		}
		if text != "" {
			text += "\n"
		}
		text += n.Text
	}
	return text
}
