# How we write anything people or agents read

This covers code comments, API descriptions in `spec/openapi.yaml`, CLI help and
output, error messages, user docs, commit messages and PR descriptions.

**The core rule:** everything must make sense to someone reading it from scratch, with
no access to our chats, plans or memory of a conversation. Explain from first
principles, in plain words.

## Say what it does today

Never refer to plans, roadmaps, slices, phases, version targets ("v0.2"), "the vision",
"as discussed", or decision numbers (`D12`) in code comments, API descriptions, CLI
text, user docs or error messages. Plans and decisions live only in `design/`
(`VISION.md`, `DECISIONS.md`).

If something isn't supported, say plainly what happens today, not when it might change.

```
Do:    Files are stored on the server's disk.
Don't: Files are stored on disk for now; S3 support lands in v0.2.

Do:    // Retries stop after 5 attempts so a dead session doesn't block the queue.
Don't: // Per D7 we cap retries (see slice 2 plan).
```

## Code comments

- Explain why, not what the code already says.
- Every package has a doc comment of two or three sentences saying what it's for.
- Exported identifiers have doc comments that start with the identifier's name.

```go
// Do
// Package redact finds credentials in message and note text and replaces them before
// the text is stored. It only reports what it replaced, never the secret itself.
package redact

// Don't
// increment i
i++
```

## API descriptions

Describe behaviour: what the endpoint does, its inputs and outputs, which errors it
returns and when, and an example. No internal package, type or table names.

```yaml
# Do
description: |
  Posts a message to the board. Posting to `all` needs the broadcast permission;
  otherwise returns 403 `broadcast_not_allowed`. Secrets in `body` are replaced
  before the message is stored.
# Don't
description: Calls store.AppendMessage via the rules pipeline.
```

## Error messages

Say what happened and what to do next, with a stable code.

```
Do:    You don't have permission to message everyone on this board.
       Use --to @name or --to role:R.            (code: broadcast_not_allowed)
Don't: forbidden
Don't: Error: permission check failed in rules.Check (broadcast=false)
```

## User docs

Start with what the reader will have at the end. Use exact, copyable commands and show
the real output under each. One page, one task. No page longer than about two screens.

## Words

Use the product vocabulary in [glossary.md](glossary.md) consistently, and don't invent
new terms for the same things. Link to the glossary rather than redefining a term.

```
Do:    The agent's owner can revoke it.
Don't: The bot's user can kick it.   (agent, owner, revoke; not bot, user, kick)
```

Prefer short sentences and plain words. No adjectives doing the work of facts:
"redacted before storage" instead of "secure"; "under 60 seconds" instead of "fast".

## Commit messages and PRs

A short imperative summary line (under about 60 characters), a blank line, then a body
saying why the change was made and anything a reviewer needs to know.

```
Do:    Reject join codes after their expiry time

       Expired codes were accepted until the server restarted because expiry was
       checked against a cached clock. Check against the injected clock on each
       redemption.

Don't: fix stuff
Don't: Implement slice 1 part 3 as discussed
```

PR descriptions say what changed, why, and how it was tested (which e2e or integration
tests cover it).
