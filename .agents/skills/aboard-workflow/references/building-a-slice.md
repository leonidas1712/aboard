# Building a slice

A slice works end to end (CLI, API, store, record, delivery or read back) before the
next starts. `AGENTS.md` has the rules; this is the order they happen in.

1. **Plan the slice** in its plan document: what a person can do afterwards, the
   refusals and their next steps, the acceptance cases.
2. **Contracts first**, following `spec/README.md`'s checklist: `spec/openapi.yaml`,
   `spec/cli.yaml`, `spec/events.md`, `spec/control.md`, `spec/delivery.md`. Contracts
   only grow. If the spec runs ahead of the code, follow the repository's convention
   (a `501 not_implemented` stub, planned examples marked as such). Regenerate code with
   `make generate`; never edit generated code by hand.
3. **Post the contracts for review** before code: the exact commit, what changed, the
   names other lanes depend on, and the points a reviewer should check.
4. **Failing tests first**, on the multi-person fixture where people are involved:
   every rule the slice touches, including each refusal. Watch them fail.
5. **Build in lanes**, one worktree each, with clear ownership. If a lane can land early,
   say what it must not switch on until the others land.
6. **Security review by a second agent**, at an exact commit: blockers versus nits, in a
   report file. Fix, then ask for a re-review of the new head, until it is cleared.
   Recheck credentials and access inside the transaction that reads or writes.
7. **Checks:** run `make quick` (format, lint, vet, generated code, core size; about a
   minute) before asking for review or landing, and the tests the slice touches while
   you work. CI's `check` workflow runs `make check`, the web UI and the docs on every
   push to the pull request, and it is the merge gate. A change to delivery or setup
   also passes `make live` with real harnesses, run with an isolated home, since CI
   can't.
8. **Land with `scripts/land-pr [--live] <number>`** from the pull request's worktree,
   as `engineering/release.md` describes: it merges once CI has passed on the exact
   commit it merges, pushing and waiting for CI when main moved, and runs nothing else
   locally but `make live-affected` with `--live` (`--local` runs the checks here
   instead). Load `.env` as above, and restore `e2e/live/support.json` first if a live
   run rewrote it. Whoever lands later takes the next free decision and migration
   numbers.
   **A flaky test:** if a failure is in a test the slice doesn't touch, check whether it
   fails on unchanged `main` too. If it does, it is a known flake: file it (an issue or
   a roadmap row, with the run's address), rerun the failed jobs (`gh run rerun
   --failed <run id>`) so it doesn't block this slice, and fix it in a pull request of
   its own, with priority. Never skip, loosen or retry it in the test itself
   (engineering/testing.md, "Flaky tests").
9. **Afterwards:** update the roadmap row, remove the worktree, and tell the board or
   the maintainer what landed, at which commit, and what is still unproven.
