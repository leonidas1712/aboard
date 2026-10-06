// (b) Solo: one person and two agents. The mocked features must stay quiet: no
// grouping, no people, a short brief, a small task board.

import type { Scenario } from "../scenario";

export const solo: Scenario = {
  id: "solo",
  title: "Solo: 2 agents",
  summary: "One person, claude and codex. Everything new should stay quiet here.",
  me: "leo",
  people: [{ name: "leo", admin: true }],
  agents: [
    { name: "claude", harness: "claude-code" },
    { name: "codex", harness: "codex" },
  ],
  board: { name: "blog-engine", policy: "starter" },
  steps: [
    {
      label: "Both agents at work",
      at: 10,
      presence: { claude: "working", codex: "working" },
      now: {
        claude: { text: "Rewriting the RSS feed builder", t: 8 },
        codex: { text: "Writing tests for the feed builder first", t: 9 },
      },
      tasks: [
        { id: "rss", title: "Rewrite the RSS feed", state: "working", owner: "claude", with: ["codex"], t: 3 },
        { id: "links", title: "Fix the broken permalinks", state: "open", t: 2 },
      ],
      messages: [
        { id: "s1", t: 1, from: "leo", body: "claude, rewrite the RSS feed; codex, write its tests first." },
        { id: "s2", t: 4, from: "claude", replyTo: "s1", to: ["@leo"], body: "On it." },
      ],
    },
    {
      label: "One done, one idle",
      at: 40,
      presence: { claude: "working", codex: "idle" },
      now: {
        claude: { text: "Fixing permalinks that end in a slash", t: 39 },
        codex: null,
      },
      tasks: [
        { id: "rss", title: "Rewrite the RSS feed", state: "done", owner: "claude", with: ["codex"], t: 37 },
        { id: "links", title: "Fix the broken permalinks", state: "working", owner: "claude", t: 39 },
      ],
      brief: { text: "Get the blog building again before Monday's release.", by: "leo", t: 15 },
      messages: [
        { id: "s3", t: 37, from: "codex", body: "Tests pass: 14 of 14." },
        { id: "s4", t: 38, from: "claude", body: "The RSS rewrite is done. Taking the permalinks next." },
      ],
    },
  ],
};
