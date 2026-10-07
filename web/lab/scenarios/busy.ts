// (d) A busy board: 12 agents, 3 people and 25 tasks, to check density. The brief is
// most of a day old, and a few working lines have gone stale.

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
  { id: "PLT-1", title: "Split the monolith's auth module into a package", state: "working", owner: "claude", with: ["claude-2", "codex"], t: 560 },
  { id: "PLT-2", title: "Migrate session storage to Redis", state: "working", owner: "codex-2", with: ["codex-3"], t: 571 },
  { id: "PLT-3", title: "Add rate limits to the public API", state: "working", owner: "claude-3", t: 590 },
  { id: "PLT-4", title: "Port the admin dashboard to the new design", state: "working", owner: "claude-4", with: ["omp", "sam"], t: 585 },
  { id: "PLT-5", title: "Write load tests for search", state: "working", owner: "codex-4", t: 520 },
  { id: "PLT-6", title: "Rebuild the search index nightly", state: "working", owner: "omp-2", t: 575 },
  { id: "PLT-7", title: "Review the auth package split", state: "working", owner: "reviewer", with: ["claude"], t: 595 },
  { id: "PLT-8", title: "Fix timezone bugs in the billing report", state: "working", owner: "omp-3", t: 480 },
  { id: "PLT-9", title: "Remove the legacy v1 webhooks", state: "claimed", owner: "codex", t: 540 },
  { id: "PLT-10", title: "Document the new rate limits", state: "claimed", owner: "claude-2", t: 565 },
  { id: "PLT-11", title: "Upgrade the ORM to 6.x", state: "claimed", owner: "codex-3", t: 550 },
  {
    id: "PLT-12",
    title: "Decide the retention period for audit logs",
    state: "waiting",
    owner: "claude-3",
    waitingOn: "leo",
    reason: "90 days, or a year for enterprise plans?",
    t: 580,
  },
  { id: "PLT-13", title: "Rotate the production database password", state: "waiting", owner: "omp", waitingOn: "priya", reason: "needs her vault approval", t: 500 },
  { id: "PLT-14", title: "Ship the CSV export", state: "waiting", owner: "codex-4", waitingOn: "codex-2", reason: "needs the new session API first", t: 545 },
  { id: "PLT-15", title: "Add SSO for enterprise accounts", state: "open", t: 400 },
  { id: "PLT-16", title: "Dark mode for the settings pages", state: "open", t: 380 },
  { id: "PLT-17", title: "Cache avatars at the edge", state: "open", t: 360 },
  { id: "PLT-18", title: "Flaky test: search paginates past the end", state: "open", t: 590 },
  { id: "PLT-19", title: "Translate the onboarding emails", state: "open", t: 300 },
  { id: "PLT-20", title: "Audit third-party scripts on the marketing site", state: "open", t: 280 },
  { id: "PLT-21", title: "Bump Node to 22 in CI", state: "done", owner: "omp-2", t: 420 },
  { id: "PLT-22", title: "Fix the broken invoice PDF footer", state: "done", owner: "omp-3", t: 450 },
  { id: "PLT-23", title: "Add health checks to the worker pool", state: "done", owner: "codex", with: ["codex-2"], t: 470 },
  { id: "PLT-24", title: "Retire the old status page", state: "done", owner: "claude-4", t: 300 },
  { id: "PLT-25", title: "Pin the base Docker images", state: "done", owner: "omp", t: 250 },
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
  // claude-3's question is an ask about PLT-12, the audit log retention.
  ...(body.startsWith("Audit logs") ? { task: "PLT-12", question: "Keep audit logs 90 days, or a year for enterprise plans?", options: ["90 days for everyone", "A year for enterprise plans"] } : {}),
        ...(body.startsWith("Sessions now") ? { task: "PLT-2", ahead: true, goingWith: "the Redis cutover at 18:00", question: "Cut sessions over to Redis at 18:00?", options: ["Go ahead", "Hold off"] } : {}),
}));

export const busy: Scenario = {
  id: "busy",
  title: "Busy: 12 agents, 25 tasks",
  summary: "A full board, to check density: 3 people, 12 agents, 25 tasks, an old brief.",
  me: "leo",
  people: [{ name: "leo", admin: true }, { name: "priya" }, { name: "sam" }],
  agents,
  steward: "reviewer",
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
        summary: "Finish the auth split (PLT-1) and the Redis session move (PLT-2), then freeze for the audit.",
        by: "reviewer",
        t: 60,
        goal: "The auth package split and Redis sessions done before Thursday's audit freeze.",
        approach: "Small PRs, under 400 lines, one reviewer each. Anything touching billing goes through priya.",
        who: "claude, claude-2 and codex on the auth split (PLT-1). codex-2 and codex-3 on Redis (PLT-2). claude-3 on rate limits (PLT-3).",
        blocked: "Your call on audit log retention (PLT-12). priya's vault approval (PLT-13).",
        next: "Freeze on Thursday; the CSV export (PLT-14) after the session API lands.",
        sources: "Cites 31 messages.",
      },
      messages,
    },
  ],
};
