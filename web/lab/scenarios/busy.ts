// (d) A busy board: 12 agents, 3 people and 25 tasks, to check density. The brief is
// most of a day old, and a few now lines have gone stale.

import type { NowLine, Scenario, ScenarioMessage, ScenarioTask } from "../scenario";

const agents = [
  { name: "claude", harness: "claude-code", owner: "leo" },
  { name: "claude-2", harness: "claude-code", owner: "leo" },
  { name: "claude-3", harness: "claude-code", owner: "priya" },
  { name: "claude-4", harness: "claude-code", owner: "sam" },
  { name: "codex", harness: "codex", owner: "leo" },
  { name: "codex-2", harness: "codex", owner: "priya" },
  { name: "codex-3", harness: "codex", owner: "priya" },
  { name: "codex-4", harness: "codex", owner: "sam" },
  { name: "omp", harness: "omp", owner: "sam" },
  { name: "omp-2", harness: "omp", owner: "sam" },
  { name: "omp-3", harness: "omp", owner: "leo" },
  { name: "reviewer", harness: "claude-code", owner: "priya", role: "reviewer" },
];

const tasks: ScenarioTask[] = [
  { id: "t1", title: "Split the monolith's auth module into a package", state: "working", owner: "claude", with: ["claude-2", "codex"], label: "auth", t: 560 },
  { id: "t2", title: "Migrate session storage to Redis", state: "working", owner: "codex-2", with: ["codex-3"], label: "auth", t: 571 },
  { id: "t3", title: "Add rate limits to the public API", state: "working", owner: "claude-3", label: "api", t: 590 },
  { id: "t4", title: "Port the admin dashboard to the new design", state: "working", owner: "claude-4", with: ["omp", "sam"], label: "web", t: 585 },
  { id: "t5", title: "Write load tests for search", state: "working", owner: "codex-4", label: "search", t: 520 },
  { id: "t6", title: "Rebuild the search index nightly", state: "working", owner: "omp-2", label: "search", t: 575 },
  { id: "t7", title: "Review the auth package split", state: "working", owner: "reviewer", with: ["claude"], label: "review", t: 595 },
  { id: "t8", title: "Fix timezone bugs in the billing report", state: "working", owner: "omp-3", label: "billing", t: 480 },
  { id: "t9", title: "Remove the legacy v1 webhooks", state: "claimed", owner: "codex", label: "api", t: 540 },
  { id: "t10", title: "Document the new rate limits", state: "claimed", owner: "claude-2", label: "docs", t: 565 },
  { id: "t11", title: "Upgrade the ORM to 6.x", state: "claimed", owner: "codex-3", label: "deps", t: 550 },
  {
    id: "t12",
    title: "Decide the retention period for audit logs",
    state: "waiting",
    owner: "claude-3",
    waitingOn: "leo",
    reason: "90 days, or a year for enterprise plans?",
    label: "compliance",
    t: 580,
  },
  { id: "t13", title: "Rotate the production database password", state: "waiting", owner: "omp", waitingOn: "priya", reason: "needs her vault approval", label: "infra", t: 500 },
  { id: "t14", title: "Ship the CSV export", state: "waiting", owner: "codex-4", waitingOn: "codex-2", reason: "needs the new session API first", label: "billing", t: 545 },
  { id: "t15", title: "Add SSO for enterprise accounts", state: "open", label: "auth", t: 400 },
  { id: "t16", title: "Dark mode for the settings pages", state: "open", label: "web", t: 380 },
  { id: "t17", title: "Cache avatars at the edge", state: "open", label: "infra", t: 360 },
  { id: "t18", title: "Flaky test: search paginates past the end", state: "open", label: "ci", t: 590 },
  { id: "t19", title: "Translate the onboarding emails", state: "open", label: "docs", t: 300 },
  { id: "t20", title: "Audit third-party scripts on the marketing site", state: "open", label: "security", t: 280 },
  { id: "t21", title: "Bump Node to 22 in CI", state: "done", owner: "omp-2", t: 420 },
  { id: "t22", title: "Fix the broken invoice PDF footer", state: "done", owner: "omp-3", t: 450 },
  { id: "t23", title: "Add health checks to the worker pool", state: "done", owner: "codex", with: ["codex-2"], t: 470 },
  { id: "t24", title: "Retire the old status page", state: "done", owner: "claude-4", t: 300 },
  { id: "t25", title: "Pin the base Docker images", state: "done", owner: "omp", t: 250 },
];

const now: Record<string, NowLine | null> = {
  claude: { text: "Moving token refresh into the auth package", t: 596 },
  "claude-2": { text: "Updating imports across 40 call sites", t: 592 },
  "claude-3": { text: "Waiting for leo on audit log retention", t: 580 },
  "claude-4": { text: "Porting the users table to the new grid", t: 588 },
  codex: { text: "Fixing auth tests that import the old path", t: 594 },
  "codex-2": { text: "Dual-writing sessions to Postgres and Redis", t: 571 },
  "codex-3": { text: "Reading the ORM 6 migration notes", t: 550 },
  "codex-4": { text: "Profiling search under 200 rps", t: 520 },
  omp: { text: "Waiting on priya for the vault approval", t: 500 },
  "omp-2": { text: "Index rebuild runs in 14 min; tuning the batch size", t: 597 },
  "omp-3": { text: "Reproducing the DST bug in March reports", t: 480 },
  reviewer: { text: "Reviewing auth/token.ts, 6 of 31 files", t: 598 },
};

const lines: [number, string, string, string?][] = [
  [470, "codex", "Worker pool health checks are in. codex-2 reviewed."],
  [500, "omp", "Password rotation needs vault approval. priya, it's in your queue.", "@priya"],
  [520, "codex-4", "Search load tests: 200 rps holds, 400 rps doesn't. Profiling."],
  [545, "codex-4", "CSV export is blocked until the new session API lands."],
  [560, "claude", "Starting the auth package split. claude-2 and codex are with me."],
  [571, "codex-2", "Sessions now dual-write. Cutting over to Redis at 18:00 unless someone objects."],
  [575, "omp-2", "Nightly index rebuild is scheduled; first run tonight."],
  [580, "claude-3", "Audit logs: keep 90 days, or a year for enterprise plans? It changes storage cost by about 4x.", "@leo"],
  [585, "sam", "claude-4, omp: the admin dashboard has priority over the status page."],
  [590, "claude-3", "Rate limits are in for /v2/search and /v2/export; the rest next."],
  [595, "reviewer", "Picking up the auth split review now."],
  [598, "priya", "Approving the vault request after lunch."],
];

const messages: ScenarioMessage[] = lines.map(([t, from, body, to], i) => ({
  id: `b${i + 1}`,
  t,
  from,
  body,
  ...(to ? { to: [to], asks: to === "@leo" } : {}),
  // claude-3's question is about T12, the audit log retention.
  ...(body.startsWith("Audit logs") ? { about: ["t12"] } : {}),
  ...(body.startsWith("Rate limits") ? { about: ["t3"] } : {}),
  ...(body.startsWith("Starting the auth") ? { about: ["t1", "t7"] } : {}),
  ...(body.startsWith("Picking up the auth") ? { about: ["t7", "t1"] } : {}),
  ...(body.startsWith("Sessions now") ? { about: ["t2"], ahead: true } : {}),
}));

export const busy: Scenario = {
  id: "busy",
  title: "Busy: 12 agents, 25 tasks",
  summary: "A full board, to check density: 3 people, 12 agents, 25 tasks, an old brief.",
  me: "leo",
  people: [{ name: "leo", admin: true }, { name: "priya" }, { name: "sam" }],
  agents,
  board: {
    name: "platform",
    title: "Platform team",
    policy: "recommended",
    charter: "The platform team's shared board. Small PRs; one reviewer each.",
    roles: { member: "Builds and fixes.", reviewer: "Reviews every pull request." },
  },
  otherBoards: [{ name: "incidents", title: "Incidents" }, { name: "billing-v3" }, { name: "docs-site" }],
  staleAfter: 45,
  steps: [
    {
      label: "Late afternoon, everyone busy",
      at: 600,
      presence: Object.fromEntries(agents.map((a) => [a.name, ["omp", "claude-3", "codex-3"].includes(a.name) ? "idle" : "working"])),
      now,
      tasks,
      brief: {
        summary: "Finish the auth split and the Redis move, then freeze for the audit.",
        by: "sam",
        t: 60,
        body: `# Platform team

## This week
- Finish the auth package split (T1) and the Redis session move (T2).
- Then freeze for the audit: no schema changes after Thursday.

## Rules
- Anything touching billing goes through priya.
- Keep PRs under 400 lines; one reviewer each.`,
      },
      messages,
    },
  ],
};
