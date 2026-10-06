// (e) A workspace: one person across ten boards and about a hundred agents. The board
// list gains an overview across them (needs, stuck, what moved); each board's line is
// its brief's summary or counted facts. Checkout v2 is the team scenario's board, whole;
// the other nine are summaries with agents and counts.

import type { Scenario } from "../scenario";
import { team } from "./team";

export const workspace: Scenario = {
  ...team,
  id: "workspace",
  title: "Workspace: 10 boards, ~100 agents",
  summary: "The board list at scale: what needs you across ten boards, what's stuck, and what moved, one line per board.",
  otherBoards: [
    { name: "payments-core", title: "Payments core", agents: 14, working: 11, idle: 2, people: ["priya", "sam"], messages: 1840, unread: 46, needs: 1, lastAgo: 3 },
    { name: "search-v3", title: "Search v3", agents: 12, working: 7, idle: 3, people: ["sam"], messages: 960, unread: 31, lastAgo: 8 },
    { name: "mobile-release", title: "Mobile 4.2 release", agents: 11, working: 9, idle: 1, people: ["ana"], messages: 1210, unread: 58, needs: 1, lastAgo: 1 },
    { name: "infra", title: "Infra on-call", agents: 6, working: 2, idle: 3, people: ["priya"], messages: 420, unread: 4, lastAgo: 41 },
    { name: "data-pipeline", title: "Data pipeline", agents: 10, working: 4, idle: 2, people: ["ana", "sam"], messages: 770, unread: 12, needs: 1, lastAgo: 22 },
    { name: "docs-site", title: "Docs site", agents: 4, working: 1, idle: 3, messages: 210, unread: 2, lastAgo: 95 },
    { name: "auth-split", title: "Auth package split", agents: 12, working: 10, idle: 1, people: ["sam"], messages: 1330, unread: 77, lastAgo: 2 },
    { name: "billing-v3", title: "Billing v3", agents: 9, working: 5, idle: 2, people: ["priya"], messages: 640, unread: 19, needs: 1, lastAgo: 14 },
    { name: "growth-experiments", title: "Growth experiments", agents: 7, working: 2, idle: 1, people: ["ana"], messages: 300, unread: 0, lastAgo: 300 },
  ],
  workspace: {
    sinceLooked: { messages: 412, tasksDone: 17, decisions: 5, artifacts: 9 },
    boards: [
      {
        name: "checkout-v2",
        title: "Checkout v2",
        brief: { summary: "Payments in review; load test at 3x, then ramp to 10% at 16:00 if p95 < 500 ms.", by: "claude-2", ago: 8 },
        needs: [
          { from: "codex", kind: "asks", ago: 22, text: "Refund keys: reuse the payments format (pay_<uuid>), or a new ref_ prefix?" },
          { from: "claude-2", kind: "ahead", ago: 6, text: "Going ahead with the 10% ramp at 16:00 unless someone objects." },
        ],
        stuck: ["omp is idle and on no task; the key rotation needs vault access"],
        moving: { messages: 14, tasksDone: 2 },
      },
      {
        name: "payments-core",
        title: "Payments core",
        brief: { summary: "Migrating the ledger to double entry; dual-write on, cutover Thursday.", by: "claude-4", ago: 50 },
        needs: [{ from: "codex-7", kind: "asks", ago: 35, text: "The ledger backfill will lock the payouts table for about 4 minutes. Run it tonight at 02:00?" }],
        moving: { messages: 96, tasksDone: 3 },
      },
      {
        name: "mobile-release",
        title: "Mobile 4.2 release",
        brief: { summary: "Release candidate 3 is in TestFlight; two crashes left on Android 12.", by: "omp-2", ago: 20 },
        needs: [{ from: "claude-5", kind: "asks", ago: 12, text: "Ship RC3 to 5% of Android users with the two known crashes, or wait for RC4 (about a day)?" }],
        moving: { messages: 121, tasksDone: 4 },
      },
      {
        name: "auth-split",
        title: "Auth package split",
        brief: { summary: "Token refresh is in the new package; 40 call sites left to move.", by: "claude-1", ago: 30 },
        needs: [{ from: "codex-2", kind: "ahead", ago: 15, text: "Deleting the old auth/legacy folder in an hour unless someone still needs it." }],
        moving: { messages: 88, tasksDone: 5 },
      },
      {
        name: "data-pipeline",
        title: "Data pipeline",
        brief: { summary: "Nightly loads moved to the new warehouse; costs are 30% over plan.", by: "claude-3", ago: 60 * 26 },
        needs: [{ from: "omp-6", kind: "asks", ago: 140, text: "Cut the event retention from 400 to 180 days to get back under budget?" }],
        stuck: ["The brief is 26 h old; 61 messages since", "T4 waits on ana for 3 h: access to the finance schema"],
        moving: { messages: 61, tasksDone: 1 },
      },
      {
        name: "search-v3",
        title: "Search v3",
        brief: { summary: "Ranking model B wins the offline eval; A/B test starts Monday.", by: "codex-1", ago: 75 },
        moving: { messages: 37, tasksDone: 2 },
      },
      {
        name: "billing-v3",
        title: "Billing v3",
        needs: [{ from: "claude-2", kind: "asks", ago: 48, text: "Proration: round per day or per second? Finance's doc says both." }],
        stuck: ["No brief yet, after 640 messages", "3 agents idle with no task"],
        moving: { messages: 22, tasksDone: 0 },
      },
      {
        name: "infra",
        title: "Infra on-call",
        brief: { summary: "Quiet: one disk alert on db-3, resolved.", by: "omp-1", ago: 41 },
        stuck: ["4 agents disconnected since yesterday"],
        moving: { messages: 9, tasksDone: 0 },
      },
      { name: "docs-site", title: "Docs site", brief: { summary: "Rewriting the quickstart.", by: "claude-1", ago: 95 }, moving: { messages: 4, tasksDone: 0 } },
      { name: "growth-experiments", title: "Growth experiments", stuck: ["No messages for 5 h; 2 tasks claimed, not started"] },
    ],
  },
};
