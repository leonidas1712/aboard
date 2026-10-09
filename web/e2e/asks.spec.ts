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
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
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
  const r = await api(ownerToken(), "GET", "/v1/asks?state=all&limit=200");
  const found = (r.asks as { id: string; ask: { state: string; answer_option: number | null } }[]).find((m) => m.id === id);
  if (!found) throw new Error("The fixture ask was not listed.");
  return found.ask;
}
test("a numbered timeline option sends its words and option as a real reply", async ({ page }) => {
  const board = "asks-timeline";
  await openBoard(page, board);
  const task = await api(seatToken(board), "POST", `/v1/boards/${board}/tasks`, { title: "Prepare the release", start: true });
  const ask = await makeAsk(board, "Ready to ship the change?");
  await expect(page.getByRole("group", { name: "Group the work" }).getByRole("button", { name: "by task", pressed: true })).toBeVisible();
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
  const going = await makeAsk(board, "I'll keep checking", { going_with: "keep checking", going_at: new Date(Date.now() + 3_600_000).toISOString() });
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const choices = page.getByRole("group", { name: "Needs you asks" }).getByRole("button");
  await expect(choices.first()).toContainText("Which release do you want?");
  await expect(page.getByRole("button", { name: "Answer with option 2: Hold it", exact: true })).toBeVisible();
  await page.keyboard.press("2");
  await expect.poll(async () => (await currentAsk(blocking.id)).state).toBe("answered");
  expect((await currentAsk(blocking.id)).answer_option).toBe(2);
  await expect(page.getByRole("heading", { name: "I'll keep checking", exact: true })).toBeVisible();
  await Promise.all([
    page.waitForResponse((r) => { const u = new URL(r.url()); return u.pathname === `/v1/boards/${board}/messages` && u.searchParams.get("before") === String(Number(going.seq) + 1); }),
    page.getByRole("link", { name: /Open on the board/ }).click(),
  ]);
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("I'll keep checking");
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
test("Inbox keeps options readable in both themes and on a narrow screen", async ({ page }, testInfo) => {
  const board = "asks-responsive";
  await openBoard(page, board);
  await makeAsk(board, "A decision with enough words to wrap comfortably on a small screen");
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await page.getByRole("group", { name: "Needs you asks" }).getByRole("button", { name: /A decision with enough words/ }).click();
  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme, exact: true }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`inbox-${theme.toLowerCase()}.png`), fullPage: true, animations: "disabled" });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  // On a phone the list and the ask take turns (D219): reading one is a tap away.
  await page.getByRole("group", { name: "Needs you asks" }).getByRole("button", { name: /A decision with enough words/ }).click();
  await expect(page.getByRole("button", { name: "Answer with option 1: Ship it", exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("inbox-mobile.png"), fullPage: true, animations: "disabled" });
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

test("historical ask links and failed lookups never advance a person's cursor", async ({ page }) => {
  await openBoard(page, "asks-history-login");
  const board = "asks-history";
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const ask = await makeAsk(board, "A decision from before the newest page");
  for (let i = 0; i < 53; i++) await api(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body: `Later discussion ${i}` });
  const before = await api(ownerToken(), "GET", `/v1/boards/${board}`);
  let acks = 0;
  await page.route(`**/v1/boards/${board}/ack`, async (route) => { acks++; await route.continue(); });
  await page.goto(`${base()}/?board=${board}&message=${ask.id}&seq=${ask.seq}`);
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("A decision from before the newest page");
  await page.getByText("A decision from before the newest page", { exact: true }).scrollIntoViewIfNeeded();
  await expect(page.getByText("A decision from before the newest page", { exact: true })).toBeVisible();
  await page.getByText("Later discussion 52", { exact: true }).scrollIntoViewIfNeeded();
  await expect(page.getByText("Later discussion 52", { exact: true })).toBeVisible();
  await page.evaluate(() => new Promise<void>((done) => requestAnimationFrame(() => requestAnimationFrame(() => done()))));
  expect(acks).toBe(0);
  expect((await api(ownerToken(), "GET", `/v1/boards/${board}`)).read_up_to).toBe(before.read_up_to);
  await page.route(`**/v1/boards/${board}/messages?**`, async (route) => {
    if (new URL(route.request().url()).searchParams.has("before")) await route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "server_unreachable", message: "The historical read failed.", hint: "Try again." } }) });
    else await route.continue();
  });
  await page.reload();
  await expect(page.getByText("The historical read failed.", { exact: true })).toBeVisible();
  await expect(page.getByText("Later discussion 52", { exact: true })).toBeVisible();
  expect(acks).toBe(0);
  expect((await api(ownerToken(), "GET", `/v1/boards/${board}`)).read_up_to).toBe(before.read_up_to);
  await page.unroute(`**/v1/boards/${board}/messages?**`);
  await page.goto(`${base()}/?board=${board}`);
  await page.getByRole("button", { name: "Show earlier messages", exact: true }).click();
  await page.getByText("A decision from before the newest page", { exact: true }).scrollIntoViewIfNeeded();
  await expect.poll(async () => (await api(ownerToken(), "GET", `/v1/boards/${board}`)).read_up_to as number).toBeGreaterThan(before.read_up_to as number);
  expect(acks).toBeGreaterThan(0);
});

const selectedAsk = (page: Page) => page.getByRole("group", { name: "Needs you asks" }).locator("button[aria-current=true]");
async function replies(board: string, ask: unknown) {
  const r = await api(ownerToken(), "GET", `/v1/boards/${board}/messages`);
  return (r.messages as { body: string; answer?: { ask_id: string } }[]).filter((m) => m.answer?.ask_id === ask);
}
test("answering in the Inbox opens the ask now in the answered one's place", async ({ page }) => {
  const board = "inbox-advance";
  await openBoard(page, board);
  const first = await makeAsk(board, "Advance first");
  const second = await makeAsk(board, "Advance second");
  await makeAsk(board, "Advance third");
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await expect(selectedAsk(page)).toContainText("Advance third");
  await page.keyboard.press("j");
  await expect(selectedAsk(page)).toContainText("Advance second");
  await page.keyboard.press("1");
  await expect.poll(async () => (await currentAsk(second.id)).state).toBe("answered");
  await expect(page.getByRole("status").filter({ hasText: "Sent to @writer:" })).toContainText("Ship it");
  await expect(selectedAsk(page)).toContainText("Advance first");
  await expect(page.getByRole("heading", { name: "Advance first", exact: true })).toBeVisible();
  expect((await currentAsk(first.id)).state).toBe("open");
});
test("Inbox keys move, accept the proposal, and write an answer", async ({ page }) => {
  const board = "inbox-keys";
  await openBoard(page, board);
  const later = new Date(Date.now() + 3_600_000).toISOString();
  const words = await makeAsk(board, "Keys need words");
  const matching = await makeAsk(board, "Keys proposal matches an option", { going_with: "Hold it", going_at: later });
  const own = await makeAsk(board, "Ran the checks on staging.\nKeys proposal in its own words?", { going_with: "keep checking", going_at: later });
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const list = page.getByRole("group", { name: "Needs you asks" });
  await expect(list.getByRole("heading", { name: "Blocking", exact: true })).toBeVisible();
  await expect(list.getByRole("heading", { name: "Going ahead unless you say", exact: true })).toBeVisible();
  await expect(list.getByRole("button", { name: /Ran the checks on staging\./ })).toContainText(/going with keep checking at \d/);

  await list.getByRole("button", { name: /Keys need words/ }).click();
  await page.keyboard.press("ArrowDown");
  await expect(selectedAsk(page)).not.toContainText("Keys need words");
  await page.keyboard.press("k");
  await expect(selectedAsk(page)).toContainText("Keys need words");

  await page.keyboard.press("r");
  const box = page.getByLabel("Your answer, sent to writer");
  await expect(box).toBeFocused();
  await box.fill("After the docs land.");
  await page.keyboard.press("Escape");
  await expect(box).toHaveCount(0);
  await expect(page.getByRole("button", { name: /Reply with something else/ })).toBeFocused();
  await page.keyboard.press("r");
  await expect(box).toHaveValue("After the docs land.");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect.poll(async () => (await currentAsk(words.id)).state).toBe("answered");
  expect((await currentAsk(words.id)).answer_option).toBeNull();

  await list.getByRole("button", { name: /Ran the checks on staging\./ }).click();
  await expect(page.getByRole("heading", { name: "Ran the checks on staging.", exact: true })).toBeVisible();
  await expect(page.getByText("Keys proposal in its own words?", { exact: true })).toBeVisible();
  await page.keyboard.press("e");
  await expect.poll(async () => (await currentAsk(own.id)).state).toBe("answered");
  expect((await replies(board, own.id)).map((m) => m.body)).toEqual(["keep checking"]);

  await list.getByRole("button", { name: /Keys proposal matches an option/ }).click();
  await expect(page.getByRole("button", { name: "Answer with option 2: Hold it", exact: true })).toContainText("proposed");
  await page.keyboard.press("e");
  await expect.poll(async () => (await currentAsk(matching.id)).state).toBe("answered");
  expect((await currentAsk(matching.id)).answer_option).toBe(2);
});
test("L snoozes an ask on this browser and ? lists the keys", async ({ page }) => {
  const board = "inbox-sheet";
  await openBoard(page, board);
  await makeAsk(board, "Snooze me for later");
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const list = page.getByRole("group", { name: "Needs you asks" });
  await list.getByRole("button", { name: /Snooze me for later/ }).click();
  await page.keyboard.press("l");
  await expect(list.getByRole("button", { name: /Snooze me for later/ })).toHaveCount(0);
  await page.getByRole("button", { name: "Show it now", exact: true }).click();
  await expect(list.getByRole("button", { name: /Snooze me for later/ })).toBeVisible();

  await page.keyboard.press("?");
  const sheet = page.getByRole("dialog", { name: "Keys in the Inbox" });
  await expect(sheet.getByText("Go to the Inbox.", { exact: true })).toHaveCount(0);
  await expect(sheet).toBeVisible();
  for (const sentence of ["Move to the next ask. The down arrow does the same.", "Answer with that option.", "Let the agent go ahead with what it proposed.", "Write your own answer.", "Send the answer you wrote.", "Leave the answer box without sending.", "Snooze the ask for an hour, on this browser only."]) await expect(sheet.getByText(sentence, { exact: true })).toBeVisible();
  await page.keyboard.press("j");
  await expect(sheet).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
});

const askRows = (page: Page) => page.getByRole("group", { name: "Needs you asks" }).locator("button[data-ask]");
test("an ask opened on its board, answered there and left with I comes back to the next ask", async ({ page }) => {
  const board = "inbox-roundtrip";
  await openBoard(page, board);
  const names = ["Roundtrip first", "Roundtrip second", "Roundtrip third"];
  const asks: Record<string, unknown>[] = [];
  for (const name of names) asks.push(await makeAsk(board, name));
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const mine = askRows(page).filter({ hasText: board });
  await expect(mine).toHaveCount(3);
  // Whatever other boards have waiting, answering moves to the ask that takes its place.
  const all = await askRows(page).evaluateAll((rows) => rows.map((r) => r.getAttribute("data-ask")));
  const id = await mine.nth(1).getAttribute("data-ask");
  const place = all.indexOf(id);
  const next = all[place + 1] ?? all[place - 1];
  const target = (await mine.nth(1).innerText()).match(/Roundtrip \w+/)![0];
  await mine.nth(1).click();
  await expect(selectedAsk(page)).toContainText(target);
  // Enter opens the ask on its board, with the way back in plain sight.
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: /^Back to Inbox/ })).toBeVisible();
  await page.getByRole("listitem").filter({ hasText: target }).getByRole("button", { name: "Answer with option 1: Ship it", exact: true }).click();
  await expect.poll(async () => (await currentAsk(asks[names.indexOf(target)].id)).state).toBe("answered");
  await page.keyboard.press("i");
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  // The answered ask is gone and the ask now at its place is selected.
  await expect(mine).toHaveCount(2);
  await expect(askRows(page).filter({ hasText: target })).toHaveCount(0);
  await expect(selectedAsk(page)).toHaveAttribute("data-ask", next!);
});
test("Escape on a board opened from the Inbox returns with the same ask selected", async ({ page }) => {
  const board = "inbox-escape";
  await openBoard(page, board);
  for (let i = 1; i <= 12; i++) await makeAsk(board, `Escape ask ${String(i).padStart(2, "0")}`);
  await page.getByRole("link", { name: /^Inbox/ }).click();
  const mine = askRows(page).filter({ hasText: board });
  await expect(mine).toHaveCount(12);
  await mine.last().click();
  const picked = (await selectedAsk(page).getAttribute("data-ask"))!;
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: /^Back to Inbox/ })).toBeVisible();
  // Escape in a text field is the field's own.
  await page.locator(".composer textarea").focus();
  await page.keyboard.press("Escape");
  await expect(page).toHaveURL(/board=inbox-escape/);
  await page.locator(".composer textarea").blur();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  await expect(selectedAsk(page)).toHaveAttribute("data-ask", picked);
  await expect(selectedAsk(page)).toBeInViewport();
  // The browser's own back gesture goes the same way.
  await page.keyboard.press("Enter");
  await expect(page.getByRole("link", { name: /^Back to Inbox/ })).toBeVisible();
  await page.goBack();
  await expect(selectedAsk(page)).toHaveAttribute("data-ask", picked);
});
test("a phone gets a Back to Inbox link on the board, and the board's keys list I", async ({ page }) => {
  const board = "inbox-phone-back";
  await openBoard(page, board);
  await makeAsk(board, "Phone roundtrip");
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await askRows(page).filter({ hasText: board }).click();
  await page.getByRole("link", { name: "Open on the board" }).click();
  await page.getByRole("link", { name: /^Back to Inbox/ }).click();
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  await expect(askRows(page).filter({ hasText: board })).toHaveCount(1);
  // Without a way in from the Inbox, a board has no Back link, but I still goes there.
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.goto(`${base()}/?board=${board}`);
  await expect(page.getByRole("button", { name: "Board details", exact: false }).first()).toBeVisible();
  await expect(page.getByRole("link", { name: /^Back to Inbox/ })).toHaveCount(0);
  await page.keyboard.press("?");
  const sheet = page.getByRole("dialog", { name: "Keys on a board" });
  await expect(sheet.getByText("Go to the Inbox.", { exact: true })).toBeVisible();
  await expect(sheet.getByText("Go back to the Inbox, when you opened this board from it.", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await page.keyboard.press("i");
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
});
