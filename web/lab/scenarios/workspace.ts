// (e) A workspace: one person over ten boards and about a hundred agents. The Inbox is
// home: asks from several boards, the selected one large with its evidence, and what is
// worth a look across them. The sidebar's red dots and unread counts say where the
// person is needed without opening anything. Checkout v2 is the team scenario's board,
// whole; the other nine are summaries with agents, counts and asks.

import type { Scenario } from "../scenario";
import { team } from "./team";

export const workspace: Scenario = {
  ...team,
  id: "workspace",
  title: "Workspace: 10 boards, ~100 agents",
  summary: "The Inbox at scale: asks from five boards, what's late or idle across them, and boards with red dots and unread counts.",
  otherBoards: [
    { name: "payments-core", title: "Payments core", agents: 14, working: 11, idle: 2, people: ["priya", "sam"], messages: 1840, unread: 46, lastAgo: 3 },
    { name: "mobile-release", title: "Mobile 4.2 release", agents: 11, working: 9, idle: 1, people: ["ana"], messages: 1210, unread: 58, lastAgo: 1 },
    { name: "auth-split", title: "Auth package split", agents: 12, working: 10, idle: 1, people: ["sam"], messages: 1330, unread: 77, lastAgo: 2 },
    { name: "search-v3", title: "Search v3", agents: 12, working: 7, idle: 3, people: ["sam"], messages: 960, unread: 31, lastAgo: 8 },
    { name: "data-pipeline", title: "Data pipeline", agents: 10, working: 4, idle: 2, people: ["ana", "sam"], messages: 770, unread: 12, lastAgo: 22 },
    { name: "billing-v3", title: "Billing v3", agents: 9, working: 5, idle: 2, people: ["priya"], messages: 640, unread: 19, lastAgo: 14 },
    { name: "infra", title: "Infra on-call", agents: 6, working: 2, idle: 3, people: ["priya"], messages: 420, unread: 4, lastAgo: 41 },
    { name: "growth", title: "Growth experiments", agents: 7, working: 2, idle: 1, people: ["ana"], messages: 300, lastAgo: 300 },
    { name: "docs-site", title: "Docs site", agents: 4, working: 1, idle: 3, messages: 210, lastAgo: 95 },
  ],
  inbox: [
    {
      id: "w1",
      board: "mobile-release",
      from: "claude-5",
      task: "MOB-42",
      question: "Ship RC3 to 5% of Android users with the two known crashes?",
      body: "Both crashes are on Android 12 and hit about 0.3% of sessions. RC4 fixes them but needs about a day of QA.",
      options: ["Yes, ship RC3 to 5%", "No, wait for RC4"],
      artifact: { name: "rc3-crash-report.html", summary: "2 crashes, 0.3% of sessions" },
      ago: 4,
    },
    {
      id: "w2",
      board: "payments-core",
      from: "codex-7",
      task: "PAY-88",
      question: "Run the ledger backfill tonight at 02:00?",
      body: "It locks the payouts table for about 4 minutes. 02:00 is the quietest hour; payouts retry on their own.",
      options: ["Yes, tonight at 02:00", "No, find a way without a lock"],
      ago: 35,
    },
    {
      id: "w3",
      board: "billing-v3",
      from: "claude-2",
      task: "BIL-7",
      question: "Prorate per day or per second?",
      body: "Finance's doc says both in different places. Per day matches the invoices people already get.",
      options: ["Per day", "Per second"],
      ago: 48,
    },
    {
      id: "w4",
      board: "auth-split",
      from: "codex-2",
      task: "AUTH-31",
      ahead: true,
      question: "Deleting auth/legacy in an hour",
      body: "Nothing imports it since this morning. I'll delete it at 12:30 unless someone holds it.",
      options: ["Hold it", "Let it go ahead"],
      ago: 15,
    },
    {
      id: "w5",
      board: "data-pipeline",
      from: "omp-6",
      task: "DATA-19",
      question: "Cut event retention from 400 to 180 days?",
      body: "The new warehouse runs 30% over budget; most of it is events older than six months that nobody queries.",
      options: ["Yes, 180 days", "No, keep 400 days", "Keep 400 days for enterprise only"],
      artifact: { name: "warehouse-costs.md", summary: "costs by table, last 30 days" },
      ago: 140,
    },
  ],
  notices: [
    { board: "infra", who: "omp-1", text: "omp-1 said back by 11:00, now 32m over", detail: "INF-9 disk alerts" },
    { board: "billing-v3", who: "claude-4", text: "claude-4 has been idle for 3h", detail: "no task" },
    { board: "growth", who: "claude-1", text: "No message for 5h; 2 tasks claimed, not started", detail: "GRO-3, GRO-4" },
  ],
};
