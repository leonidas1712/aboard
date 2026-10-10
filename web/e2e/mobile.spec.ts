import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { devices, expect, test, type Page } from "@playwright/test";

// The board view on a phone (D219) and each agent's status (D218), at a phone's size with
// touch. ABOARD_MOBILE_SHOTS=<dir> also saves the screenshots the maintainer reviews.

const { defaultBrowserType: _, ...phone } = devices["iPhone 13"];
test.use({ ...phone, colorScheme: "light" });

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-mobile-web-"));
const bin = join(home, "aboard");
const shots = process.env.ABOARD_MOBILE_SHOTS;
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
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
});
test.afterAll(() => {
  try { if (env) aboard("down"); } finally { rmSync(home, { recursive: true, force: true }); }
});

function base(): string { return `http://${env.ABOARD_LOCAL_ADDR}`; }
function tokenFile(file: string): string { return execFileSync("find", [home, "-name", file], { encoding: "utf8" }).trim().split("\n")[0]; }
function ownerToken(): string { return readFileSync(tokenFile("local-owner-token"), "utf8").trim(); }
function seatToken(board: string, name: string): string {
  const saved = JSON.parse(readFileSync(tokenFile("credentials.json"), "utf8")) as { agents: { board: string; name?: string; agent?: string; token: string }[] };
  const found = saved.agents.find((a) => a.board === board && (a.name ?? a.agent) === name);
  if (!found) throw new Error("The fixture seat was not saved.");
  return found.token;
}
async function api(token: string, method: string, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const r = await fetch(`${base()}${path}`, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status}`);
  return await r.json();
}
async function openBoard(page: Page, board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}
async function noSideScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}
async function shot(page: Page, name: string) {
  if (!shots) return;
  mkdirSync(shots, { recursive: true });
  // A ring left by the last keyboard step isn't part of the picture.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.screenshot({ path: join(shots, `${name}.png`), animations: "disabled" });
}
async function theme(page: Page, which: "Light" | "Dark") {
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await page.getByRole("menuitemradio", { name: which, exact: true }).click();
  await page.keyboard.press("Escape");
}

test("a phone shows the board in one column, its panel as a sheet, and each agent's status", async ({ page }) => {
  const board = "mobile-board";
  await openBoard(page, board);
  aboard("say", "--as", "writer", "--board", board, "--to", "all", "The draft is in notes.md; reviewing the API section next.");
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("The draft is in notes.md");
  // The panels are not on the page until their sheet opens; the message box is.
  await expect(page.getByRole("complementary")).toHaveCount(0);
  await expect(page.getByRole("combobox", { name: "Message everyone" })).toBeInViewport();
  await noSideScroll(page);
  await shot(page, "phone-board-light");

  // The board panel opens as a sheet over the conversation; Back closes it.
  const open = page.getByRole("button", { name: /^Board panel/ });
  await open.click();
  const sheet = page.getByRole("dialog", { name: "mobile-board" });
  await expect(sheet).toBeVisible();
  await noSideScroll(page);
  const writer = sheet.locator('[data-agent="writer"]');
  await expect(writer.locator(".presence")).toHaveText("disconnected");
  await expect(writer).toHaveAttribute("data-status", "off");

  // A reported presence changes the word and the colour's tone, live.
  const r = await fetch(`${base()}/v1/me/presence`, { method: "PUT", headers: { Authorization: `Bearer ${seatToken(board, "writer")}`, "Content-Type": "application/json" }, body: JSON.stringify({ presence: "working" }) });
  expect(r.status).toBe(200);
  await expect(writer.locator(".presence")).toHaveText("working");
  await expect(writer).toHaveAttribute("data-status", "working");
  await expect(writer.locator("button.agent-row")).toContainText("writer is working: a turn is running in its session.");
  await shot(page, "phone-panel-light");
  await sheet.getByRole("button", { name: "Conversation", exact: true }).click();
  await expect(sheet).toBeHidden();
  await expect(open).toBeFocused();

  // The browser's back closes a sheet too, and leaves the board where it was.
  await page.getByRole("button", { name: /^Boards and Inbox/ }).click();
  await expect(page.getByRole("dialog", { name: "Boards" })).toBeVisible();
  await expect(page.getByRole("link", { name: /^Inbox/ })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("dialog", { name: "Boards" })).toBeHidden();
  await expect(page.getByRole("log", { name: "Timeline" })).toBeVisible();

  // Tasks fill the column too.
  await api(seatToken(board, "writer"), "POST", `/v1/boards/${board}/tasks`, { title: "Review the API section", start: true });
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  await expect(page.locator('[data-on-task="writer"]').first()).toContainText("working");
  await noSideScroll(page);
  await shot(page, "phone-tasks-light");

  await theme(page, "Dark");
  await shot(page, "phone-tasks-dark");
  await page.getByRole("tab", { name: "Conversation" }).click();
  await shot(page, "phone-board-dark");
  await open.click();
  await expect(page.getByRole("dialog", { name: /Work/ })).toBeVisible();
  await shot(page, "phone-panel-dark");
  await page.getByRole("button", { name: "Conversation", exact: true }).click();
  await theme(page, "Light");

  // The smallest and largest common phones keep to the screen too.
  for (const width of [360, 430]) {
    await page.setViewportSize({ width, height: 800 });
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await noSideScroll(page);
    await page.getByRole("tab", { name: "Conversation" }).click();
    await noSideScroll(page);
    await open.click();
    await noSideScroll(page);
    await page.getByRole("button", { name: "Conversation", exact: true }).click();
  }
  await page.setViewportSize({ width: 390, height: 664 });

  // People keeps to the screen as well.
  await page.goto(`${base()}/?view=people`);
  await expect(page.getByRole("heading", { name: "People" })).toBeVisible();
  await noSideScroll(page);
  await shot(page, "phone-people-light");
  await theme(page, "Dark");
  await shot(page, "phone-people-dark");
  await theme(page, "Light");
});

test("an ask is answered on a phone in two taps", async ({ page }) => {
  const board = "mobile-asks";
  await openBoard(page, board);
  const ask = await api(seatToken(board, "writer"), "POST", `/v1/boards/${board}/messages`, { body: "Ship the release notes today?\nThey cover the API changes and the new CLI flags.", to: ["@alex"], ask: { options: ["Ship today", "Wait for review"] } });
  await api(seatToken(board, "writer"), "POST", `/v1/boards/${board}/messages`, { body: "Rename the staging bucket?", to: ["@alex"], ask: { options: ["Rename it", "Keep it"] } });
  await page.goto(`${base()}/?inbox`);
  const list = page.getByRole("group", { name: "Needs you asks" });
  await expect(list.getByRole("button", { name: /Ship the release notes today/ })).toBeVisible();
  // The list alone: no answer is on screen until an ask is read.
  await expect(page.getByRole("button", { name: /^Answer with option/ })).toHaveCount(0);
  await expect(page.locator("kbd:visible")).toHaveCount(0);
  await noSideScroll(page);
  await shot(page, "phone-inbox-light");

  // Tap one: read it, with its answers and the asker's status.
  await list.getByRole("button", { name: /Ship the release notes today/ }).tap();
  await expect(page.getByRole("heading", { name: "Ship the release notes today?" })).toBeVisible();
  await expect(page.locator(".asker-status")).toContainText("disconnected");
  await expect(page.locator("kbd:visible")).toHaveCount(0);
  await noSideScroll(page);
  await shot(page, "phone-inbox-ask-light");
  await theme(page, "Dark");
  await shot(page, "phone-inbox-ask-dark");
  await theme(page, "Light");

  // Tap two: answer it; the list comes back without it and says what was sent.
  await page.getByRole("button", { name: "Answer with option 1: Ship today", exact: true }).tap();
  await expect(page.getByRole("status").filter({ hasText: "Sent to @writer" })).toBeVisible();
  await expect(list.getByRole("button", { name: /Ship the release notes today/ })).toHaveCount(0);
  await expect(list.getByRole("button", { name: /Rename the staging bucket/ })).toBeVisible();
  await expect.poll(async () => {
    const all = await api(ownerToken(), "GET", "/v1/asks?state=all&limit=200");
    return (all.asks as { id: string; ask: { state: string; answer_option: number | null } }[]).find((m) => m.id === ask.id)?.ask;
  }).toEqual(expect.objectContaining({ state: "answered", answer_option: 1 }));

  // Back returns to the list without answering.
  await list.getByRole("button", { name: /Rename the staging bucket/ }).tap();
  await page.getByRole("button", { name: "Inbox", exact: true }).tap();
  await expect(list.getByRole("button", { name: /Rename the staging bucket/ })).toBeVisible();
  for (const width of [360, 430]) {
    await page.setViewportSize({ width, height: 800 });
    await noSideScroll(page);
    await list.getByRole("button", { name: /Rename the staging bucket/ }).tap();
    await noSideScroll(page);
    await page.getByRole("button", { name: "Inbox", exact: true }).tap();
  }
  await page.setViewportSize({ width: 390, height: 664 });
  await theme(page, "Dark");
  await shot(page, "phone-inbox-dark");
});

test("Files and a file's panel fit a phone, and the file sheet opens and closes", async ({ page }) => {
  const board = "mobile-files";
  await openBoard(page, board);
  const put = async (name: string, over: number, body: string) => {
    const r = await fetch(`${base()}/v1/boards/${board}/files?${new URLSearchParams({ name, base: String(over) })}`, { method: "POST", headers: { Authorization: `Bearer ${seatToken(board, "writer")}`, "Content-Type": "application/octet-stream" }, body });
    expect(r.status).toBe(201);
  };
  await put("notes/a-rather-long-folder-name/release-checklist-for-the-public-launch.md", 0, "# Release checklist\n\n- Tag the release\n- Publish the notes\n- `aboard pair` works from a fresh machine with a long line that has to wrap on a phone\n");
  await put("notes/a-rather-long-folder-name/release-checklist-for-the-public-launch.md", 1, "# Release checklist\n\n- Tag the release\n- Publish the notes\n- Announce it\n");
  await put("status.html", 0, `<!doctype html><html><body style="font-family:sans-serif"><h1>Status</h1><table style="width:900px"><tr><td>A table wider than a phone, which the preview scrolls inside its own frame.</td></tr></table></body></html>`);
  const r = await fetch(`${base()}/v1/me/presence`, { method: "PUT", headers: { Authorization: `Bearer ${seatToken(board, "writer")}`, "Content-Type": "application/json" }, body: JSON.stringify({ presence: "working" }) });
  expect(r.status).toBe(200);

  await page.getByRole("tab", { name: /^Files/ }).click();
  const list = page.getByRole("list", { name: "Files" });
  await expect(list.locator("[data-file]")).toHaveCount(2);
  // The writer's mark carries its status, named for assistive technology.
  await expect(list.locator('[data-file="status.html"] [data-status="working"]')).toHaveCount(1);
  await expect(page.getByRole("button", { name: "Upload a file" })).toBeVisible();
  for (const width of [360, 430, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await noSideScroll(page);
  }
  await page.setViewportSize({ width: 390, height: 664 });
  await shot(page, "phone-files-light");

  // A file opens as a full-screen sheet; Back returns to Files.
  await list.locator('[data-file="status.html"]').getByRole("button", { name: "status.html" }).click();
  const sheet = page.getByRole("dialog", { name: "File" });
  await expect(sheet).toBeVisible();
  const frame = sheet.locator("iframe");
  await expect(frame).toBeVisible();
  // Once the sheet has slid in, the preview's frame sits inside the screen.
  await expect.poll(() => frame.evaluate((el) => el.getBoundingClientRect().right <= window.innerWidth)).toBe(true);
  for (const width of [360, 430, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await noSideScroll(page);
    expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  }
  await page.setViewportSize({ width: 390, height: 664 });
  await shot(page, "phone-file-html-light");
  await sheet.getByRole("button", { name: "Files", exact: true }).click();
  await expect(sheet).toBeHidden();
  await expect(list).toBeVisible();

  // The Markdown file, with its versions; the browser's back closes the sheet too.
  await list.locator("[data-file]").filter({ hasText: "release-checklist" }).getByRole("button").first().click();
  await expect(sheet).toBeVisible();
  await expect(sheet.getByRole("region", { name: "Versions" })).toBeVisible();
  await noSideScroll(page);
  expect(await sheet.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);
  await shot(page, "phone-file-light");
  await page.goBack();
  await expect(sheet).toBeHidden();
  await theme(page, "Dark");
  await shot(page, "phone-files-dark");
  await list.locator('[data-file="status.html"]').getByRole("button", { name: "status.html" }).click();
  await expect(sheet.locator("iframe")).toBeVisible();
  await shot(page, "phone-file-html-dark");
  await sheet.getByRole("button", { name: "Files", exact: true }).click();
  await theme(page, "Light");
});

test("the brief reads, edits and shows a newer version on a phone", async ({ page }) => {
  const board = "mobile-brief";
  await openBoard(page, board);
  const putBrief = async (body: string, over?: { id: string; version: number }) => {
    const qs = new URLSearchParams({ name: "brief.md", brief: "true", base: String(over?.version ?? 0) });
    if (over) qs.set("file_id", over.id);
    const r = await fetch(`${base()}/v1/boards/${board}/files?${qs}`, { method: "POST", headers: { Authorization: `Bearer ${seatToken(board, "writer")}`, "Content-Type": "application/octet-stream" }, body });
    expect(r.status).toBe(201);
    return (await r.json()) as { id: string };
  };
  const first = await putBrief("# Launch week\n\nShipping the public release on Friday, **behind the docs review**.\n\n## Who's doing what\n\n- writer keeps this brief and the release notes\n- reviewer checks `aboard pair` from a fresh machine with a very long command line that must wrap\n");
  const brief = page.getByRole("region", { name: "Brief" });
  await expect(brief.locator(".brief-summary")).toContainText("Shipping the public release");
  for (const width of [360, 430, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await noSideScroll(page);
  }
  await page.setViewportSize({ width: 390, height: 664 });
  await brief.getByRole("button", { name: "Show full brief" }).click();
  await noSideScroll(page);
  // The whole brief reads in the column; Show less brings the conversation back.
  await expect(page.getByRole("log", { name: "Timeline" })).toBeHidden();
  await shot(page, "phone-brief-light");
  await brief.getByRole("button", { name: "Show less" }).click();
  await expect(page.getByRole("log", { name: "Timeline" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Message everyone" })).toBeVisible();
  await brief.getByRole("button", { name: "Show full brief" }).click();

  // Editing takes the column: the timeline and the message box step aside, and come back after.
  await brief.getByRole("button", { name: "Edit" }).click();
  const text = page.getByLabel("The brief, in Markdown");
  await expect(text).toBeVisible();
  await expect(page.getByRole("log", { name: "Timeline" })).toBeHidden();
  await expect(page.getByRole("combobox", { name: "Message everyone" })).toBeHidden();
  // The field reads at 16px, so focusing it doesn't zoom the page.
  expect(await text.evaluate((el) => parseFloat(getComputedStyle(el).fontSize))).toBeGreaterThanOrEqual(16);
  await text.tap();
  await text.press("ControlOrMeta+End");
  await text.pressSequentially("- alex: the docs review is done");
  // A short screen, as with the keyboard up: the editor scrolls inside what is left.
  await page.setViewportSize({ width: 390, height: 380 });
  await noSideScroll(page);
  await expect(brief.getByRole("button", { name: "Save version 2" })).toBeAttached();
  await page.setViewportSize({ width: 390, height: 664 });
  await shot(page, "phone-brief-edit-light");

  // An agent writes v2 meanwhile: the editor says so, and the person's text stays.
  await putBrief("# Launch week\n\nShipping on Friday. Docs review moved to Thursday.\n", { id: first.id, version: 1 });
  const changed = brief.locator(".brief-conflict");
  await expect(changed).toContainText("writer wrote brief.md v2");
  for (const width of [360, 430, 390]) {
    await page.setViewportSize({ width, height: 800 });
    await noSideScroll(page);
  }
  await page.setViewportSize({ width: 390, height: 664 });
  await changed.scrollIntoViewIfNeeded();
  await shot(page, "phone-brief-conflict-light");
  await theme(page, "Dark");
  await shot(page, "phone-brief-conflict-dark");
  await theme(page, "Light");
  await brief.getByRole("button", { name: "Cancel" }).click();
  await brief.getByRole("button", { name: "Discard" }).click();
  // Back to reading the brief, then Show less returns the conversation.
  await expect(brief.getByRole("button", { name: "Edit" })).toBeVisible();
  await theme(page, "Dark");
  await shot(page, "phone-brief-dark");
  await theme(page, "Light");
  await brief.getByRole("button", { name: "Show less" }).click();
  await expect(page.getByRole("log", { name: "Timeline" })).toBeVisible();
  await expect(page.getByRole("combobox", { name: "Message everyone" })).toBeVisible();
});

test.describe("on a wide screen", () => {
  const { defaultBrowserType: _d, ...desktop } = devices["Desktop Chrome"];
  test.use({ ...desktop, viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });

  test("each agent's status shows as a word beside a coloured mark", async ({ page }) => {
    const board = "status-colours";
    await openBoard(page, board);
    // Joins are rate limited; one turned away is tried again after the minute.
    for (const [name, harness] of [["codex", "codex"], ["reviewer", "claude-code"], ["helper", "omp"], ["scout", "claude-code"], ["docs", "codex"]]) {
      const line = JSON.parse(aboard("invite", "--board", board, "--json")).join_line;
      await expect(() => aboard("join", line, "--name", name, "--harness", harness)).toPass({ timeout: 90_000, intervals: [5_000] });
    }
    const presence = (name: string, state: string) => fetch(`${base()}/v1/me/presence`, { method: "PUT", headers: { Authorization: `Bearer ${seatToken(board, name)}`, "Content-Type": "application/json" }, body: JSON.stringify({ presence: state }) });
    expect((await presence("writer", "working")).status).toBe(200);
    expect((await presence("scout", "waiting")).status).toBe(200);
    expect((await presence("reviewer", "idle")).status).toBe(200);
    expect((await presence("helper", "idle")).status).toBe(200);
    expect((await presence("docs", "idle")).status).toBe(200);
    // reviewer waits on the person; helper on another agent.
    await api(seatToken(board, "reviewer"), "POST", `/v1/boards/${board}/tasks`, { title: "Check the migration", start: true });
    await api(seatToken(board, "reviewer"), "POST", `/v1/boards/${board}/messages`, { body: "Run the migration on staging first?", to: ["@alex"], ask: { options: ["Yes", "No"] } });
    await api(seatToken(board, "helper"), "POST", `/v1/boards/${board}/tasks`, { title: "Draft the changelog", start: true });
    await api(seatToken(board, "helper"), "POST", `/v1/boards/${board}/messages`, { body: "Which version goes in the heading?", to: ["@codex"], ask: { options: ["0.1.1", "0.2.0"] } });

    const panel = page.getByRole("complementary", { name: /Work/ });
    const expected: [string, string, string][] = [
      ["writer", "working", "working"],
      ["scout", "needs", "waiting"],
      ["reviewer", "needs", "waiting on you"],
      ["helper", "hold", "blocked on codex"],
      ["codex", "off", "disconnected"],
      ["docs", "idle", "idle"],
    ];
    for (const [name, tone, word] of expected) {
      const row = panel.locator(`[data-agent="${name}"]`);
      await expect(row).toHaveAttribute("data-status", tone);
      await expect(row.locator(".presence")).toHaveText(word);
    }
    await expect(panel.locator('[data-agent="reviewer"]')).toContainText("reviewer asked you and waits for the answer.");
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await shot(page, "desktop-status-light");
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await shot(page, "desktop-tasks-light");
    await theme(page, "Dark");
    await page.getByRole("tab", { name: "Conversation" }).click();
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await shot(page, "desktop-status-dark");
    await page.getByRole("tab", { name: /^Tasks/ }).click();
    await shot(page, "desktop-tasks-dark");
    await theme(page, "Light");
  });
});
