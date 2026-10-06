---
name: aboard-workflow
description: How to work on the aboard repository itself, alone or with other agents on a board. Use when starting any task in this repo, when the maintainer shares an idea, article or post to think through, when planning or recording a decision, when building or reviewing a slice, when writing docs or public copy, or when coordinating with other agents on an aboard board about this project.
---

# Working on aboard

This is how work on aboard gets done: orienting, thinking ideas through, planning,
building, reviewing and reporting. It covers working alone and working with other
agents on a board (see [On a board](references/on-a-board.md)).

The maintainer is the person you work for. They decide scope, approve decisions and
merge. Your job is to understand, propose clearly, build carefully and report honestly.

## 1. Orient before you act

- Read `AGENTS.md` (also `CLAUDE.md`), then what the task touches in `design/VISION.md`,
  `design/DECISIONS.md` and `design/ROADMAP.md`. If `private/LOCAL_RULES.md` exists,
  read it and follow it.
- Check the live state, not your memory of it: `main`'s head, open pull requests, and the
  board if you're on one. Plans and status notes go stale quickly.
- Hard rules, every time:
  - Work in your own worktree on a branch from `origin/main`; never change, switch or
    pull the main checkout.
  - Never read `.env`; load it only with `set -a && . ./.env && set +a` when a command
    needs it.
  - Never read, run or mention anything under a top-level `reference/` folder.
  - `private/` is never committed, and nothing is copied out of it into tracked files.
  - Tracked text names no employer, workplace, colleague or real person, and no
    competitor outside the comparison material in `design/`.
  - Tests and live proofs never touch the maintainer's real config or state (rule 13):
    isolated `HOME` and state, checksums of real harness config before and after.
  - Never upgrade or migrate the maintainer's real install without asking.

## 2. Thinking an idea through

The maintainer often shares raw thoughts, an article, a post or a thread and wants a
thinking partner, not a summary.

1. **Read the source.** If they pasted notes or put a file in the repo root, move it to
   `private/` so it is never committed.
2. **Say what you understood** in a few lines, so they can correct you early.
3. **Give your own view.** Agree, disagree, add the idea they didn't have. Be specific
   and creative; offer a sharper version of the idea, not a list of considerations.
4. **Connect it to what exists.** Search VISION, the roadmap and `design/explorations/`
   first: most ideas are partly planned already. Say what is new.
5. **Keep the product's line.** Primitives in the server, everything else a client or an
   extension (D54); the server never calls a model (D79); harnesses run agents and aboard
   is where they work together; a solo user never sees team concepts.
6. **Sort it into now, next and later.** Anything outside the v0.1 scope needs the
   maintainer's approval: say so rather than building it.
7. **End with a recommendation** and the one or two decisions they need to make.

When asked to capture it: a note in `design/explorations/`, "to scope" rows in
`design/ROADMAP.md`, written generically (no names, no sources quoted), each part saying
whether it needs approval. Open a docs-only pull request.

## 3. Planning and decisions

- A plan lives in `design/` and is checked against what has actually merged.
- Define "done" as a short list of outcomes a person can observe, plus an explicit list
  of what is not needed.
- Put open choices to the maintainer as a numbered list, each with your recommendation.
  Don't ask about things you can decide from the code or the conventions.
- Record an approved decision in `design/DECISIONS.md` with the next unused number:
  check `main` and open pull requests first, since numbers collide when work lands in
  parallel.
- Keep `design/ROADMAP.md` current: a row's status changes when work starts, goes into
  review and merges.

## 4. Building a slice

The full checklist is in [Building a slice](references/building-a-slice.md). In short:
contracts first, failing tests first, one worktree per lane, a second agent reviews for
security, `make quick` before asking for review or landing, land with `scripts/land-pr`
once CI passes, live tests when delivery or setup changes. A test that fails on
unchanged `main` is a known flake: file it, fix it in its own pull request, never skip
or loosen it, and don't let it block unrelated work (engineering/testing.md).

## 5. Docs and public copy

- Follow `engineering/writing.md`. Write the product name "aboard" in lowercase in
  prose.
- `design/positioning.md` is the source for the pitch. Check every claim against the
  code: shipped features in the present tense, anything unbuilt only under "Where it's
  going".
- Plain, friendly language at the top of a page; technical detail lower down or linked.

## 6. Reporting to the maintainer

- **Lead with what you need from them.** When a long task is done, or you're stuck and
  need the maintainer to do something, list what you need from them at the very top of
  your reply, before any summary. One thing per item, numbered. Spell out what to do,
  where, and what to send back when it's done (for example: "Merge #108 once CI is
  green, then reply go"). Whatever blocks you most goes first. If you need nothing, say
  so in one line.
- Then the outcome, kept short; they may be reading on a small screen between other
  things.
- When they ask what's going on, or to explain plainly, drop the jargon: say what
  changes for a person using aboard, then the detail if they want it.
- Report faithfully: what passed, what wasn't run, what is still unproven. Never round
  "mostly works" up to "done".
- Never decide scope, merge another agent's pull request, or change anything public
  (the repository's description, a release, a post) without the maintainer's OK.
- Spread large work across agents from more than one provider when the maintainer asks,
  to share usage.
