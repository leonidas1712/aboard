import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

// The Inbox's board-add and arrival items, and the invite notice's suggested handle,
// against a real server in its own home. The server's onboarding-inbox answer, the
// agent list and the notices are route fixtures, so these pass whatever the server
// returns for them; everything else (sign-in, boards) is real.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-onboarding-inbox-web-"));
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
const base = () => `http://${env.ABOARD_LOCAL_ADDR}`;

test.beforeAll(async () => {
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
  aboard("pair", "general", "--board", "inbox-items-setup", "--name", "writer", "--new");
});
test.afterAll(() => {
  try {
    if (env) aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

const prompt = "You were added to launch. Run: aboard join launch --server http://127.0.0.1:1 --as <your-name>";
const launch = { id: "brd_launch", name: "launch", title: "Launch" };
const fixture = {
  board_adds: [{ board: launch, added: { seq: 4, at: new Date().toISOString(), by: { kind: "human", member_id: "mem_leo", name: "leo", owner: null } }, join_command: "aboard join launch", join_prompt: prompt }],
  arrivals: [{ invite_id: "inv_1", person_id: "per_maya", handle: "maya", at: new Date().toISOString(), boards: [{ board: launch, agents: [{ id: "mem_cm", name: "claude-maya", harness: "claude-code" }] }] }],
};
const agents = [
  { kind: "agent", id: "mem_a", name: "reviewer", board: "docs", harness: "claude-code", access: "member", joined_at: new Date().toISOString(), presence: "idle", presence_since: null, location: { harness: "claude-code", session_id: "sess-123", folder: "/work/docs", last_active: new Date().toISOString() } },
];

async function openInbox(page: Page, inbox: unknown = fixture) {
  await page.route("**/v1/me/onboarding-inbox", (route) => route.fulfill({ json: inbox }));
  await page.route("**/v1/me/agents", (route) => route.fulfill({ json: { agents } }));
  const link = JSON.parse(aboard("open", "--board", "inbox-items-setup", "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  await page.goto(`${base()}/?inbox`);
}

test("a board add shows the join prompt, copies it, and offers an agent you already have", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: base() });
  await openInbox(page);
  const list = page.getByRole("list", { name: "Added and arrived" });
  await expect(list.getByRole("button", { name: /@leo added you to launch/ })).toBeVisible();
  await list.getByRole("button", { name: /@leo added you to launch/ }).click();
  const card = page.locator('[data-board-add="brd_launch"]');
  await expect(card.getByRole("heading", { name: "@leo added you to launch" })).toBeVisible();
  await expect(card).toContainText("Paste this into any agent session");
  await expect(card.getByLabel("Join prompt")).toHaveText(prompt);
  await card.getByRole("button", { name: "Copy the join prompt", exact: true }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(prompt);

  // The picker lists your agents with their resume command; copying gives the same prompt.
  await card.getByRole("button", { name: "Join with an agent you already have" }).click();
  const picker = card.getByRole("list", { name: "Your agents" });
  await expect(picker).toContainText("reviewer");
  await expect(picker.locator(".ob-command")).toContainText("cd /work/docs && claude --resume sess-123");
  await expect(picker).toContainText("Resume it, then paste the join prompt into that session.");
  await page.evaluate(() => navigator.clipboard.writeText(""));
  await picker.getByRole("button", { name: "Copy the join prompt for reviewer" }).click();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(prompt);
});

test("an arrival names the person and their agents, links to the board, and both items can be dismissed", async ({ page }) => {
  await openInbox(page);
  const list = page.getByRole("list", { name: "Added and arrived" });
  await list.getByRole("button", { name: /@maya joined launch with @claude-maya/ }).click();
  const card = page.locator('[data-arrival="inv_1"]');
  await expect(card.getByRole("heading", { name: "@maya joined launch with @claude-maya" })).toBeVisible();
  await expect(card.getByRole("link", { name: "Launch" })).toHaveAttribute("href", "/?board=launch");
  await card.getByRole("button", { name: "Dismiss" }).click();
  await expect(list.getByRole("button", { name: /@maya joined/ })).toHaveCount(0);
  // Dismissed on this browser: it stays hidden after a reload.
  await page.reload();
  await expect(list.getByRole("button", { name: /@leo added you to launch/ })).toBeVisible();
  await expect(list.getByRole("button", { name: /@maya joined/ })).toHaveCount(0);
  await list.getByRole("button", { name: /@leo added you to launch/ }).click();
  await page.locator('[data-board-add="brd_launch"]').getByRole("button", { name: "Dismiss" }).click();
  await expect(page.getByRole("list", { name: "Added and arrived" })).toHaveCount(0);
});

test("an invite notice shows the suggested handle when it has one", async ({ page }) => {
  const notice = (id: string, extra: object) => ({ id, issuing_agent_id: "mem_a", message: "", created_at: new Date().toISOString(), expires_at: new Date(Date.now() + 86_400_000).toISOString(), state: "active", next: { command: "x", resume: "x" }, ...extra });
  await page.route("**/v1/me/invite-notices", (route) => route.fulfill({ json: { notices: [notice("inv_with", { suggested_handle: "maya" }), notice("inv_without", {})] } }));
  await openInbox(page, { board_adds: [], arrivals: [] });
  await page.goto(`${base()}/?inbox&item=inv_with`);
  await expect(page.locator('[data-notice="inv_with"]')).toContainText("Invited as @maya");
  await expect(page.locator('[data-notice="inv_with"]').getByRole("button", { name: "Edit handle" })).toBeVisible();
  await page.goto(`${base()}/?inbox&item=inv_without`);
  await expect(page.locator('[data-notice="inv_without"]')).toContainText("No suggested handle");
});
