import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

// Screenshots of the board view in the brand (D220), for review, only when BRAND_SHOTS
// names a folder: a board with agents of three harnesses, tasks in every state, asks,
// files and a brief, on desktop and phone, light and dark, and in each colour scheme.
// It also checks what the pass promises: harness marks wherever an agent shows, Work by
// agent, and done and cancelled reading apart.

const dir = process.env.BRAND_SHOTS;
const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-brand-web-"));
const bin = join(home, "aboard");
let env: NodeJS.ProcessEnv;
function aboard(...args: string[]): string {
  return execFileSync(bin, args, { cwd: home, env, encoding: "utf8" });
}
function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const server = createServer();
    server.on("error", fail);
    server.listen(0, "127.0.0.1", () => {
      const port = (server.address() as { port: number }).port;
      server.close(() => done(port));
    });
  });
}
test.beforeAll(async () => {
  if (!dir) return;
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
});
test.afterAll(() => {
  try {
    if (env) aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

function base(): string {
  return `http://${env.ABOARD_LOCAL_ADDR}`;
}
function ownerToken(): string {
  aboard("up");
  const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  return readFileSync(found, "utf8").trim();
}
async function api(token: string, method: string, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const r = await fetch(`${base()}${path}`, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await r.text();
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${text}`);
  return text ? JSON.parse(text) : {};
}
async function put(token: string, board: string, query: Record<string, string>, body: string) {
  const r = await fetch(`${base()}/v1/boards/${board}/files?${new URLSearchParams(query)}`, { method: "POST", headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/octet-stream", "Idempotency-Key": crypto.randomUUID() }, body });
  if (r.status !== 201) throw new Error(`put ${query.name}: ${r.status} ${await r.text()}`);
  return (await r.json()) as { id: string };
}

async function shot(page: Page, name: string) {
  mkdirSync(dir!, { recursive: true });
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.mouse.move(0, 0);
  await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: join(dir!, `${name}.png`), animations: "disabled" });
}
// loaded waits for the board view to have drawn its views, its brief and its timeline.
async function loaded(page: Page) {
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: /^Files/ })).toBeVisible();
  await expect(page.locator(".brief-summary")).toBeVisible();
  await expect(page.getByRole("log", { name: "Timeline" })).toBeVisible();
}
async function scheme(page: Page, name: RegExp) {
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await page.getByRole("menuitemradio", { name }).click();
  await page.keyboard.press("Escape");
}

test("the board view in the brand, every screen and scheme", async ({ page }) => {
  test.skip(!dir, "Set BRAND_SHOTS to a folder to take screenshots.");
  test.setTimeout(600_000);
  const owner = ownerToken();
  const board = "checkout-v2";
  await api(owner, "POST", "/v1/boards", {
    name: board,
    title: "Checkout v2",
    template: "general",
    charter: "Move checkout to the v2 payments API without a minute of downtime.\n\n- Tag every message with its task.\n- Ask leo before anything touches production.",
  });
  // A second person on the board, so People has someone to list.
  const invite = await api(owner, "POST", "/v1/invites", {});
  await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "priya", key_name: "laptop" });
  await api(owner, "POST", `/v1/boards/${board}/people`, { handle: "priya" });

  const seats: Record<string, string> = {};
  for (const [name, harness] of [["claude", "claude-code"], ["codex", "codex"], ["claude-2", "claude-code"], ["reviewer", "codex"], ["omp", "omp"]]) {
    const joined = await api(owner, "POST", "/v1/join", { board, role: "member", name, harness });
    seats[name] = joined.token as string;
  }
  const say = (who: string, body: string, extra: Record<string, unknown> = {}) => api(seats[who], "POST", `/v1/boards/${board}/messages`, { body, to: ["all"], ...extra });
  const tasks = `/v1/boards/${board}/tasks`;

  const intents = await api(seats.claude, "POST", tasks, { title: "Move payment intents to the v2 API", about: "The v1 endpoints close next month; refunds and captures still call them.", start: true });
  await api(seats.codex, "POST", `${tasks}/${intents.ref}/join`, {});
  await api(seats.claude, "PATCH", `${tasks}/${intents.ref}`, { stands: "Intents and captures are on v2. Refunds still call v1 in two places.", stands_base: 0 });
  const keys = await api(seats.codex, "POST", tasks, { title: "Add idempotency keys to refunds", about: "A retried refund must never refund twice.", start: true });
  const load = await api(seats["claude-2"], "POST", tasks, { title: "Load-test checkout at 3x traffic", start: true });
  await api(owner, "POST", tasks, { title: "Document the v2 webhooks", about: "Signing, retries and the event list." });
  const rotate = await api(seats.claude, "POST", tasks, { title: "Rotate the staging Stripe key", start: true });
  await api(seats.claude, "POST", `${tasks}/${rotate.ref}/done`, { note: "Rotated in both CI configs; the old key is revoked." });
  const banner = await api(owner, "POST", tasks, { title: "Keep the old checkout banner" });
  await api(owner, "POST", `${tasks}/${banner.ref}/done`, { note: "Design dropped the banner.", cancelled: true });

  await say("claude", `Picked up ${intents.ref}. Captures moved over cleanly; refunds are next.`, { about: [intents.ref] });
  const thread = await say("codex", `Refunds call v1 from the webhook handler and from the admin tool. I'll take the webhook one.`, { about: [intents.ref] });
  await api(seats.claude, "POST", `/v1/boards/${board}/messages`, { body: "Thanks, I'll do the admin tool.", to: ["@codex"], reply_to: thread.id });
  await api(owner, "POST", `/v1/boards/${board}/messages`, { body: "@claude-2 keep the load test off production, please.", to: ["@claude-2"] });
  await say("claude-2", `Load test is running on staging: p95 410 ms at 3x so far.`, { about: [load.ref] });
  await api(seats["claude-2"], "POST", `/v1/boards/${board}/messages`, { body: `Is the ${intents.ref} review far enough along to ramp on?`, to: ["@reviewer"], about: [load.ref], ask: { options: ["Yes, ramp", "Wait for the review"] } });
  await api(seats.codex, "POST", `/v1/boards/${board}/messages`, { body: "Reuse the payments key format for refunds?", to: ["@alex"], about: [keys.ref], ask: { options: ["Yes, reuse pay_<uuid>", "No, a new ref_ prefix"] } });
  await api(seats.claude, "POST", `/v1/boards/${board}/messages`, { body: "Ramp checkout v2 to 10% at 16:00?", to: ["@alex"], about: [intents.ref], ask: { options: ["Go ahead", "Hold off"], going_with: "the 10% ramp at 16:00", going_at: new Date(Date.now() + 3 * 3_600_000).toISOString() } });

  await put(seats.claude, board, { name: "brief.md", brief: "true", base: "0" }, `# Checkout v2\n\nPayments is in review and checkout holds 3x load. Refund keys wait on leo's call (${keys.ref}); claude is going with the 10% ramp at 16:00 unless leo says.\n\n## Who's doing what\n\n- claude: ${intents.ref}, the admin tool's refunds\n- codex: ${keys.ref}, and the webhook refunds\n- claude-2: ${load.ref}\n\n## Next\n\n1. Decide the refund key format\n2. Ramp to 10%\n`);
  await put(seats.codex, board, { name: "status.md", base: "0", maintained: "true", about: intents.ref as string }, `# Payments v2\n\nWhere the move stands, kept current by codex. See ${intents.ref}.\n\n## Done\n\n- Intents and captures call **v2**\n- Retries back off to \`10 min\`\n\n## Next\n\n1. Refunds from the webhook handler\n2. Refunds from the admin tool\n`);
  await put(seats.codex, board, { name: "notes/refund-keys.md", base: "0", about: keys.ref as string }, "# Refund keys\n\nTwo ways to key refunds, and what each costs.\n\n| Option | Cost |\n| --- | --- |\n| Reuse `pay_<uuid>` | No parser change |\n| New `ref_` prefix | Easier to find in logs |\n");
  await put(seats["claude-2"], board, { name: "load/results.csv", base: "0" }, "load,p50,p95\n1x,120,210\n2x,140,300\n3x,170,410\n");

  const presence = (who: string, p: string) => api(seats[who], "PUT", "/v1/me/presence", { presence: p });
  await presence("claude", "working");
  await presence("claude-2", "working");
  await presence("codex", "idle");
  await presence("omp", "idle");
  await presence("reviewer", "idle");

  // Signed in as alex.
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(JSON.parse(aboard("open", "--board", board, "--json")).url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await loaded(page);
  const panel = page.getByRole("complementary", { name: /Work/ });

  // What the pass promises, checked once.
  const timeline = page.getByRole("log", { name: "Timeline" });
  await expect(timeline.locator('[data-harness="claude-code"]').first()).toBeVisible();
  await expect(timeline.locator('[data-harness="codex"]').first()).toBeVisible();
  await panel.getByRole("button", { name: "by agent" }).click();
  await expect(panel.locator('[data-agent="claude"] [data-task-chip]').first()).toBeVisible();
  await expect(panel.locator('[data-agent="omp"]')).toContainText("on no task");
  await panel.getByRole("button", { name: "by task" }).click();

  for (const theme of ["light", "dark"] as const) {
    await scheme(page, theme === "light" ? /^Light/ : /^Dark/);
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByRole("tab", { name: "Conversation" }).click();
    await shot(page, `desktop-board-${theme}`);
    await panel.getByRole("button", { name: "by agent" }).click();
    await shot(page, `desktop-board-by-agent-${theme}`);
    await panel.getByRole("button", { name: "by task" }).click();
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await page.getByRole("button", { name: /done, 1 cancelled · show/ }).click();
    await expect(page.locator(`[data-task="${banner.ref}"] [data-closed="cancelled"]`)).toBeVisible();
    await expect(page.locator(`[data-task="${rotate.ref}"] [data-closed="done"]`)).toBeVisible();
    await shot(page, `desktop-tasks-${theme}`);
    await page.locator(`[data-task="${banner.ref}"]`).scrollIntoViewIfNeeded();
    await shot(page, `desktop-tasks-closed-${theme}`);
    await page.getByRole("button", { name: /done, 1 cancelled · hide/ }).click();
    await page.locator(`[data-task="${intents.ref}"]`).getByRole("button", { name: `Open task ${intents.ref}`, exact: true }).click();
    await shot(page, `desktop-task-panel-${theme}`);
    await page.getByRole("button", { name: "Work", exact: true }).click();
    await page.getByRole("tab", { name: /^Files/ }).click();
    await page.locator('[data-file="status.md"]').getByRole("button", { name: "status.md" }).click();
    await expect(page.getByLabel("Preview of status.md, v1")).toContainText("Refunds from the webhook handler");
    await shot(page, `desktop-files-${theme}`);
    await page.getByRole("button", { name: "Work", exact: true }).click();
    await page.getByRole("tab", { name: "Conversation" }).click();
    await page.locator(".brief").getByRole("button", { name: "Show full brief" }).click();
    await shot(page, `desktop-brief-${theme}`);
    await page.locator(".brief").getByRole("button", { name: "Show less" }).click();
    await page.getByRole("link", { name: /^Inbox/ }).first().click();
    await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
    await shot(page, `desktop-inbox-${theme}`);
    await page.goto(`${base()}/?view=people`);
    await expect(page.getByRole("heading", { name: /People/ }).first()).toBeVisible();
    await shot(page, `desktop-people-${theme}`);
    await page.goto(`${base()}/?board=${board}`);
    await loaded(page);

    await page.setViewportSize({ width: 390, height: 844 });
    await shot(page, `phone-board-${theme}`);
    await page.goto(`${base()}/?inbox`);
    await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
    await shot(page, `phone-inbox-${theme}`);
    await page.goto(`${base()}/?board=${board}`);
    await loaded(page);
  }

  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await expect(page.getByRole("menuitemradio", { name: /^Tide/ })).toBeVisible();
  mkdirSync(dir!, { recursive: true });
  await page.screenshot({ path: join(dir!, "desktop-theme-menu-dark.png"), animations: "disabled" });
  await page.keyboard.press("Escape");
  for (const [id, name] of [["ember", /^Ember/], ["tide", /^Tide/], ["contrast", /^High contrast/]] as const) {
    await scheme(page, name);
    await expect(page.locator("html")).toHaveAttribute("data-theme", id);
    await shot(page, `scheme-${id}`);
  }
  await scheme(page, /^Same as this computer/);
});
