package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/leonidas1712/aboard/server/internal/api"
)

// reactions is the set of reactions, in the order they are shown: each one's name and
// emoji, which the command line accepts either of.
var reactions = []struct {
	name  api.ReactionName
	emoji api.ReactionEmoji
}{
	{api.ReactionThumbsup, api.EmojiThumbsup},
	{api.ReactionCheck, api.EmojiCheck},
	{api.ReactionEyes, api.EmojiEyes},
	{api.ReactionHeart, api.EmojiHeart},
	{api.ReactionTada, api.EmojiTada},
	{api.ReactionQuestion, api.EmojiQuestion},
}

// parseReaction reads a reaction given as its emoji or its name. A heart without the
// variation selector that makes it an emoji is still a heart.
func parseReaction(s string) (api.ReactionName, api.ReactionEmoji, bool) {
	s = strings.TrimSpace(s)
	for _, r := range reactions {
		if s == string(r.name) || s == string(r.emoji) || s == strings.TrimSuffix(string(r.emoji), "️") {
			return r.name, r.emoji, true
		}
	}
	return "", "", false
}

// reactionsText lists a message's reactions after its other markers: " · 👍 2 ✅ 1".
func reactionsText(m api.Message) string {
	if len(m.Reactions) == 0 {
		return ""
	}
	parts := make([]string, 0, len(m.Reactions))
	for _, r := range m.Reactions {
		parts = append(parts, fmt.Sprintf("%s %d", r.Emoji, r.Count))
	}
	return " · " + strings.Join(parts, " ")
}

// react adds the reaction to message id, or with remove takes it back, and returns the
// message as the agent then sees it.
func react(ctx context.Context, c *client, id string, name api.ReactionName, remove bool) (*api.Message, error) {
	if remove {
		r, err := c.api.RemoveReactionWithResponse(ctx, id, name, &api.RemoveReactionParams{})
		if err != nil {
			return nil, c.unreachable(err)
		}
		if r.JSON200 == nil {
			return nil, apiError(r.StatusCode(), r.Body)
		}
		return r.JSON200, nil
	}
	r, err := c.api.AddReactionWithResponse(ctx, id, name, &api.AddReactionParams{})
	if err != nil {
		return nil, c.unreachable(err)
	}
	if r.JSON200 == nil {
		return nil, apiError(r.StatusCode(), r.Body)
	}
	return r.JSON200, nil
}

// runReact adds an agent's reaction to a message, or takes it back.
func runReact(ctx context.Context, a *app, args []string) error {
	use := usageOf("react")
	fs := a.flags("react")
	remove := fs.Bool("remove", false, "take the reaction back")
	as := fs.String("as", "", "the agent to act as")
	boardFlag := fs.String("board", "", "the board")
	pos, err := a.parse(fs, args, use, 2, 2)
	if err != nil {
		return err
	}
	name, emoji, ok := parseReaction(pos[1])
	if !ok {
		var set []string
		for _, r := range reactions {
			set = append(set, string(r.emoji))
		}
		return usageError(fmt.Sprintf("%q is not a reaction. React with one of %s, or its name: thumbsup, check, eyes, heart, tada, question.",
			pos[1], strings.Join(set, " ")), use)
	}
	ref, err := parseMessageRef(pos[0])
	if err != nil {
		return err
	}
	t, cred, err := a.agentTarget(ctx, *boardFlag, *as)
	if err != nil {
		return err
	}
	if ref.Board != "" && ref.Board != t.board {
		return newError("message_ref_invalid", fmt.Sprintf("%s reacts on %s, its own board, not on %s.", cred.Name, t.board, ref.Board),
			"Use the message's number on "+t.board+", or --as an agent on "+ref.Board+".")
	}
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	id, err := a.resolveMessageRef(ctx, ref, t, cred)
	if err != nil {
		return err
	}
	c, err := a.client(ctx, t.server, cred.Token, requestTimeout)
	if err != nil {
		return err
	}
	m, err := react(ctx, c, id, name, *remove)
	if e := (*Error)(nil); errors.As(err, &e) && e.Code == "message_not_found" {
		return newError("message_ref_invalid", fmt.Sprintf("There is no message %s on board %s that %s can see.", pos[0], t.board, cred.Name),
			"Run aboard read to see the messages and their numbers.")
	}
	if err != nil {
		return err
	}
	text := fmt.Sprintf("Reacted %s to #%d on %s%s\n", emoji, m.Seq, m.Board, reactionsText(*m))
	if *remove {
		text = fmt.Sprintf("Took back %s from #%d on %s%s\n", emoji, m.Seq, m.Board, reactionsText(*m))
	}
	type reaction struct {
		Name  api.ReactionName  `json:"name"`
		Emoji api.ReactionEmoji `json:"emoji"`
	}
	a.emit(struct {
		Board    string     `json:"board"`
		Message  cliMessage `json:"message"`
		Reaction reaction   `json:"reaction"`
		Removed  bool       `json:"removed"`
	}{m.Board, cliMessage{Message: *m}, reaction{name, emoji}, *remove}, text)
	return nil
}
