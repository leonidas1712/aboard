import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-asks-web-"));
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
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
});
test.afterAll(() => {
  try { if (env) aboard("down"); } finally { rmSync(home, { recursive: true, force: true }); }
});
async function openBoard(page: Page, board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}
test("Inbox is reachable from the board without replacing Work or Tasks", async ({ page }) => {
  await openBoard(page, "asks-navigation");
  await api(seatToken("asks-navigation"), "POST", "/v1/boards/asks-navigation/tasks", { title: "Keep the Work view", start: true });
  await expect(page.getByRole("tab", { name: /^Tasks/ })).toBeVisible();
  await expect(page.getByRole("link", { name: /^Inbox/ })).toBeVisible();
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await expect(page.getByRole("heading", { name: "Inbox", exact: true })).toBeVisible();
});

function base(): string { return `http://${env.ABOARD_LOCAL_ADDR}`; }
function tokenFile(file: string): string { return execFileSync("find", [home, "-name", file], { encoding: "utf8" }).trim().split("\n")[0]; }
function ownerToken(): string { return readFileSync(tokenFile("local-owner-token"), "utf8").trim(); }
function seatToken(board: string): string {
  const saved = JSON.parse(readFileSync(tokenFile("credentials.json"), "utf8")) as { agents: { board: string; token: string }[] };
  const found = saved.agents.find((a) => a.board === board);
  if (!found) throw new Error("The fixture seat was not saved.");
  return found.token;
}
async function api(token: string, method: string, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const r = await fetch(`${base()}${path}`, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status}`);
  return await r.json();
}
async function makeAsk(board: string, body: string, extra: Record<string, unknown> = {}) {
  return await api(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body, to: ["@alex"], ask: { options: ["Ship it", "Hold it"], ...extra } });
}
async function currentAsk(id: unknown) {
  const r = await api(ownerToken(), "GET", `/v1/messages/${id}`);
  return (r.message as { ask: { state: string; answer_option: number | null } }).ask;
}
test("a numbered timeline option sends its words and option as a real reply", async ({ page }) => {
  const board = "asks-timeline";
  await openBoard(page, board);
  const task = await api(seatToken(board), "POST", `/v1/boards/${board}/tasks`, { title: "Prepare the release", start: true });
  const ask = await makeAsk(board, "Ready to ship the change?");
  await expect(page.getByRole("heading", { name: "Work · by task", exact: true })).toBeVisible();
  await expect.poll(async () => (await api(ownerToken(), "GET", `/v1/boards/${board}/tasks/${task.ref}`)).blocked).toBe(true);
  await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true }).click();
  await expect.poll(async () => (await currentAsk(ask.id)).state).toBe("answered");
  expect((await currentAsk(ask.id)).answer_option).toBe(1);
  await expect.poll(async () => (await api(ownerToken(), "GET", `/v1/boards/${board}/tasks/${task.ref}`)).blocked).toBe(false);
  const messages = await api(ownerToken(), "GET", `/v1/boards/${board}/messages`);
  expect((messages.messages as { body: string; answer?: { ask_id: string; option: number } }[]).some((m) => m.body === "Ship it" && m.answer?.ask_id === ask.id && m.answer?.option === 1)).toBe(true);
});
test("Inbox puts blocking asks first and accepts a numbered keyboard answer", async ({ page }) => {
  const board = "asks-keyboard";
  await openBoard(page, board);
  const blocking = await makeAsk(board, "Which release do you want?");
  await makeAsk(board, "I'll keep checking", { going_with: "keep checking", going_at: new Date(Date.now() + 3_600_000).toISOString() });
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const choices = page.getByRole("list", { name: "Needs you asks" }).getByRole("button");
  await expect(choices.first()).toContainText("Which release do you want?");
  await expect(page.getByRole("button", { name: "Answer with option 2: Hold it", exact: true })).toBeVisible();
  await page.keyboard.press("2");
  await expect.poll(async () => (await currentAsk(blocking.id)).state).toBe("answered");
  expect((await currentAsk(blocking.id)).answer_option).toBe(2);
});
test("a failed answer stays open, then a reply in words records no option", async ({ page }) => {
  const board = "asks-own-words";
  await openBoard(page, board);
  const ask = await makeAsk(board, "Choose what happens next");
  await page.route(`**/v1/boards/${board}/messages`, async (route) => {
    if (route.request().method() === "POST") await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "server_unreachable", message: "The answer was not sent.", hint: "Try again." } }) });
    else await route.continue();
  });
  await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true }).click();
  await expect(page.getByText("The answer was not sent.", { exact: true })).toBeVisible();
  expect((await currentAsk(ask.id)).state).toBe("open");
  await page.unroute(`**/v1/boards/${board}/messages`);
  await page.getByRole("button", { name: /Reply with something else/ }).click();
  await page.getByLabel("Your answer, sent to writer").fill("Ship after the documentation update.");
  await page.getByRole("button", { name: "Send to writer", exact: true }).click();
  await expect.poll(async () => (await currentAsk(ask.id)).state).toBe("answered");
  expect((await currentAsk(ask.id)).answer_option).toBeNull();
});
test("archived asks show options without enabling an answer", async ({ page }) => {
  const board = "asks-archived";
  await openBoard(page, board);
  await makeAsk(board, "Keep this decision in the archive");
  await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
  await api(ownerToken(), "POST", `/v1/boards/${board}/archive`, {});
  await expect(page.getByRole("region", { name: "Archived board", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /^Answer with option/ })).toHaveCount(0);
  await expect(page.getByRole("list", { name: "Answers", exact: true })).toContainText("Ship it");
});
test("Inbox keeps options readable in both themes and on a narrow screen", async ({ page }) => {
  const board = "asks-responsive";
  await openBoard(page, board);
  await makeAsk(board, "A decision with enough words to wrap comfortably on a small screen");
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await page.getByRole("list", { name: "Needs you asks" }).getByRole("button", { name: /A decision with enough words/ }).click();
  mkdirSync("/private/tmp/aboard-asks-evidence", { recursive: true });
  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme, exact: true }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
    await page.screenshot({ path: `/private/tmp/aboard-asks-evidence/inbox-${theme.toLowerCase()}.png`, fullPage: true });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: "Inbox", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: "/private/tmp/aboard-asks-evidence/inbox-mobile.png", fullPage: true });
});

test("only the server-authorized reader gets answer controls", async ({ page }) => {
  const board = "asks-authority";
  await openBoard(page, board);
  const keys: Record<string, string> = {};
  for (const handle of ["pat", "sol"]) {
    const invite = await api(ownerToken(), "POST", "/v1/invites", {});
    const made = await api("", "POST", "/v1/connect", { invite: invite.invite, handle, key_name: "web-fixture" });
    keys[handle] = (made.key as { token: string }).token;
    await api(ownerToken(), "POST", `/v1/boards/${board}/people`, { handle });
  }
  const ask = await api(keys.pat, "POST", `/v1/boards/${board}/messages`, { body: "Sol should decide this", to: ["@sol"], ask: { options: ["Ship it", "Hold it"] } });
  await expect(page.getByText("Sol should decide this", { exact: true })).toBeVisible();
  await expect(page.getByRole("list", { name: "Answers", exact: true })).toContainText("Ship it");
  await expect(page.getByRole("button", { name: /^Answer with option/ })).toHaveCount(0);
  expect((await currentAsk(ask.id)).state).toBe("open");
});

test("Worth a look derives an old blocker from the real task and ask", async ({ page }) => {
  const board = "asks-worth-a-look";
  await openBoard(page, board);
  const invite = await api(ownerToken(), "POST", "/v1/invites", {});
  await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "jacob", key_name: "web-fixture" });
  await api(ownerToken(), "POST", `/v1/boards/${board}/people`, { handle: "jacob" });
  const task = await api(seatToken(board), "POST", `/v1/boards/${board}/tasks`, { title: "A decision waiting on Jacob", start: true });
  await api(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body: "Jacob should pick the release", to: ["@jacob"], ask: { options: ["Ship", "Hold"] } });
  await page.clock.setFixedTime(new Date(Date.now() + 3 * 3_600_000 + 60_000));
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const notice = page.getByRole("list", { name: "Worth a look", exact: true }).getByRole("link", { name: new RegExp(`${task.ref} blocked on jacob for 3h`) });
  await expect(notice).toBeVisible();
  await notice.click();
  await expect(page.getByRole("heading", { name: "A decision waiting on Jacob", exact: true })).toBeVisible();
});
