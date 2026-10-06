// (c) Empty states: a board made a minute ago, then its first agent. No tasks, no brief,
// no now lines, no messages.

import type { Scenario } from "../scenario";

export const empty: Scenario = {
  id: "empty",
  title: "Empty: a new board",
  summary: "Just made: no messages, no tasks, no brief. Then one agent joins with no now line.",
  me: "leo",
  people: [{ name: "leo", admin: true }],
  agents: [{ name: "claude", harness: "claude-code", joined: 3 }],
  board: { name: "new-board", policy: "starter" },
  steps: [
    { label: "The board was just made", at: 1 },
    { label: "An agent joins", at: 4, presence: { claude: "idle" } },
  ],
};
