import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

// The Inbox's Replies waiting section, on a real server: the read, messages, replies,
// boards and sessions are all real.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-replies-web-"));
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
  execFileSync("go", ["build", "-tags", "ui,webtest", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
});
test.afterAll(() => {
  try { if (env) aboard("down"); } finally { rmSync(home, { recursive: true, force: true }); }
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
type Msg = { id: string; board: string; seq: number; at: string; body: string; from: { name: string; kind: string }; to: string[]; reply_to: string | null; thread_root: string | null; expects_reply: boolean; ask?: unknown };
async function api<T = Record<string, unknown>>(token: string, method: string, path: string, body?: unknown): Promise<T> {
  const r = await fetch(`${base()}${path}`, { method, headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: body === undefined ? undefined : JSON.stringify(body) });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status}`);
  return (await r.json()) as T;
}

function addBoard(board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
}
async function signIn(page: Page, board: string) {
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}
function waitFor(board: string, body: string): Promise<Msg> {
  return api<Msg>(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body, to: ["@alex"], expects_reply: true });
}

const replyRows = (page: Page) => page.getByRole("list", { name: "Messages waiting for your reply" }).locator("button[data-reply]");
const selected = (page: Page) => page.locator("button[aria-current=true]:is([data-ask], [data-reply])");
const inbox = (page: Page) => page.getByRole("link", { name: /^Inbox/ }).first();

test("the Inbox lists replies waiting from every board, below the asks, and counts them", async ({ page }) => {
  addBoard("replies-list-a");
  addBoard("replies-list-b");
  await signIn(page, "replies-list-a");
  await api(seatToken("replies-list-a"), "POST", "/v1/boards/replies-list-a/messages", { body: "Which release do you want?", to: ["@alex"], ask: { options: ["Ship it", "Hold it"] } });
  await waitFor("replies-list-a", "Can you read `notes.md` before I merge?\nIt covers the release steps.");
  await waitFor("replies-list-b", "Is the staging key still valid?");
  // A message to everyone, and one that expects no reply, never wait on alex.
  await api(seatToken("replies-list-b"), "POST", "/v1/boards/replies-list-b/messages", { body: "Done with the rotation.", to: ["@alex"] });
  await inbox(page).click();
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toContainText("3 need you");
  await expect(replyRows(page)).toHaveCount(2);
  await expect(replyRows(page).first()).toContainText("Is the staging key still valid?");
  await expect(replyRows(page).first()).toContainText("replies-list-b · writer (alex's agent)");
  await expect(replyRows(page).nth(1)).toContainText("Can you read notes.md before I merge?");
  await expect(replyRows(page).nth(1)).not.toContainText("It covers the release steps.");
  // The asks come first: the section sits below them.
  const askBox = await page.getByRole("group", { name: "Needs you asks" }).boundingBox();
  const replyBox = await page.getByRole("group", { name: "Replies waiting" }).boundingBox();
  expect(replyBox!.y).toBeGreaterThan(askBox!.y);
  // The sidebar's Inbox count adds the replies waiting to the blocking ask.
  await expect(inbox(page)).toContainText("3");
  await replyRows(page).nth(1).click();
  const detail = page.getByRole("region", { name: "The selected ask" });
  await expect(detail).toContainText("writer(alex's agent) waits for your reply");
  await expect(detail.locator(".reply-waiting-body code")).toHaveText("notes.md");
  await expect(detail.locator(".reply-waiting-body")).toContainText("It covers the release steps.");
});

test("a reply sent in place is a real reply, and the row leaves the list", async ({ page }) => {
  const board = "replies-send";
  addBoard(board);
  await signIn(page, board);
  const first = await waitFor(board, "Should the docs mention the new flag?");
  await waitFor(board, "Did the deploy finish on your side?");
  await inbox(page).click();
  const mine = replyRows(page).filter({ hasText: board });
  await expect(mine).toHaveCount(2);
  await mine.filter({ hasText: "Should the docs mention the new flag?" }).click();
  await page.keyboard.press("r");
  const box = page.getByLabel("Your reply, sent to writer");
  await expect(box).toBeFocused();
  await box.fill("Yes, one line under Options.");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(page.getByRole("status").filter({ hasText: "Sent to @writer" })).toContainText("Yes, one line under Options.");
  await expect(mine).toHaveCount(1);
  await expect(mine).toContainText("Did the deploy finish on your side?");
  // The message now at its place is open, ready for the next reply.
  await expect(page.getByLabel("Your reply, sent to writer")).toBeVisible();
  const messages = await api<{ messages: Msg[] }>(ownerToken(), "GET", `/v1/boards/${board}/messages`);
  const reply = messages.messages.find((m) => m.body === "Yes, one line under Options.");
  expect(reply?.reply_to).toBe(first.id);
  expect(reply?.from.name).toBe("alex");
});

test("J and K move across asks and replies waiting, Enter opens the message on its board", async ({ page }) => {
  const board = "replies-keys";
  addBoard(board);
  await signIn(page, board);
  await api(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body: "Keys ask", to: ["@alex"], ask: { options: ["Ship it", "Hold it"] } });
  const waiting = await waitFor(board, "Keys reply waiting");
  await inbox(page).click();
  await expect(replyRows(page).filter({ hasText: "Keys reply waiting" })).toHaveCount(1);
  await page.getByRole("group", { name: "Needs you asks" }).getByRole("button", { name: /Keys ask/ }).click();
  await expect(selected(page)).toContainText("Keys ask");
  // J walks down from the asks into the replies waiting, K walks back.
  const ids = await page.locator("button:is([data-ask], [data-reply])").evaluateAll((rows) => rows.map((r) => r.getAttribute("data-ask") ?? r.getAttribute("data-reply")));
  const from = ids.indexOf(await selected(page).getAttribute("data-ask"));
  for (let i = from + 1; i < ids.length; i++) await page.keyboard.press("j");
  await expect(selected(page)).toHaveAttribute("data-reply", ids[ids.length - 1]!);
  while (!(await selected(page).getAttribute("data-reply") === waiting.id)) await page.keyboard.press("k");
  await expect(selected(page)).toContainText("Keys reply waiting");
  // L snoozes asks only: on a reply waiting it does nothing.
  await page.keyboard.press("l");
  await expect(selected(page)).toHaveAttribute("data-reply", waiting.id);
  await page.keyboard.press("?");
  const sheet = page.getByRole("dialog", { name: "Keys in the Inbox" });
  for (const sentence of ["Move to the next ask or reply waiting. The down arrow does the same.", "Write your own answer, or go to the reply box of a message waiting for your reply.", "Open the ask or message on its board."]) await expect(sheet.getByText(sentence, { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(sheet).toBeHidden();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(new RegExp(`board=${board}.*message=${waiting.id}`));
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("Keys reply waiting");
  await page.getByRole("link", { name: /^Back to Inbox/ }).click();
  await expect(selected(page)).toHaveAttribute("data-reply", waiting.id);
  // The link in the detail opens it the same way.
  await page.getByRole("region", { name: "The selected ask" }).getByRole("link", { name: "Open on the board" }).click();
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("Keys reply waiting");
});

test("the Inbox with only replies waiting is not empty, and reads well in both themes and on a phone", async ({ page }, testInfo) => {
  const board = "replies-look";
  addBoard(board);
  await signIn(page, board);
  await waitFor(board, "Can you look at **the onboarding copy** before I send it?\nTwo lines changed:\n- the welcome line\n- the first hint");
  await inbox(page).click();
  await expect(replyRows(page).filter({ hasText: board })).toHaveCount(1);
  await expect(page.getByText("Nothing waits on you.")).toHaveCount(0);
  await replyRows(page).filter({ hasText: board }).click();
  // The text reads as Markdown: the row as plain words, the detail formatted.
  await expect(replyRows(page).filter({ hasText: board })).toContainText("Can you look at the onboarding copy before I send it?");
  const body = page.getByRole("region", { name: "The selected ask" }).locator(".reply-waiting-body");
  await expect(body.locator("strong")).toHaveText("the onboarding copy");
  await expect(body.getByRole("listitem")).toHaveText(["the welcome line", "the first hint"]);
  for (const theme of ["Light", "Dark"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme, exact: true }).click();
    await page.keyboard.press("Escape");
    await expect(page.getByLabel("Your reply, sent to writer")).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`replies-${theme.toLowerCase()}.png`), fullPage: true, animations: "disabled" });
  }
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath("replies-phone-list.png"), fullPage: true, animations: "disabled" });
  await replyRows(page).filter({ hasText: board }).click();
  await expect(page.getByLabel("Your reply, sent to writer")).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.screenshot({ path: testInfo.outputPath("replies-phone-dark.png"), fullPage: true, animations: "disabled" });
  await page.getByLabel("Your reply, sent to writer").fill("Looks good.");
  await page.getByRole("button", { name: "Send to writer" }).click();
  // Sent from a phone, the list comes back with the row gone.
  await expect(page.getByRole("heading", { name: /^Inbox\b/ })).toBeVisible();
  await expect(replyRows(page).filter({ hasText: board })).toHaveCount(0);
});

test("other members' replies leave a message waiting, a newer request replaces it, and alex's reply clears it", async ({ page }) => {
  const board = "replies-clear";
  addBoard(board);
  await signIn(page, board);
  const invite = await api(ownerToken(), "POST", "/v1/invites", {});
  const made = await api<{ key: { token: string } }>("", "POST", "/v1/connect", { invite: invite.invite, handle: "pat", key_name: "web-fixture" });
  await api(ownerToken(), "POST", `/v1/boards/${board}/people`, { handle: "pat" });
  const waiting = await waitFor(board, "Does the change need a note?");
  const read = () => api<{ messages: Msg[]; count: number }>(ownerToken(), "GET", `/v1/me/replies-waiting?board=${board}`);
  // Neither another person's reply in the thread nor the sender's own clears it.
  await api(made.key.token, "POST", `/v1/boards/${board}/messages`, { body: "I think it does.", reply_to: waiting.id });
  await api(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body: "Still checking on my side.", reply_to: waiting.id });
  expect((await read()).messages.map((m) => m.id)).toEqual([waiting.id]);
  await inbox(page).click();
  const rows = replyRows(page).filter({ hasText: board });
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("Does the change need a note?");
  // A newer request from the same sender in the thread takes its place.
  const newer = await api<Msg>(seatToken(board), "POST", `/v1/boards/${board}/messages`, { body: "Short or long note?", to: ["@alex"], expects_reply: true, reply_to: waiting.id });
  expect((await read()).messages.map((m) => m.id)).toEqual([newer.id]);
  await expect(rows).toHaveCount(1);
  await expect(rows).toContainText("Short or long note?");
  await rows.click();
  await page.getByLabel("Your reply, sent to writer").fill("Short.");
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(rows).toHaveCount(0);
  const after = await read();
  expect(after.messages).toEqual([]);
  expect(after.count).toBe(0);
});

// inboxLoaded goes to the Inbox and waits until it has replaced the board view, so a
// message posted next is never shown, and acknowledged, by the board view on its way out.
async function inboxLoaded(page: Page) {
  await inbox(page).click();
  await expect(page.locator("#inbox-title")).toBeVisible();
}

// A decided approval, as the server lists it; only the Inbox's list reads it here.
const at = () => new Date().toISOString();
const decided = { id: "apr_01M4MZJNPHX08FRHZ84P73TNF9", person_id: "hum_alex", agent_id: "mem_claude", action: { kind: "invite_people", invite: { suggested_handle: "maya" } }, state: "executed", created_at: at(), decided_at: at(), execution: { at: at(), invite_id: "inv_01M4MZJTM1VBAK8M74RJ7AVFP5", authorization: { kind: "invited", person_id: "hum_alex", agent_id: "mem_claude", via: "approval" } }, display: { agent_name: "claude", agent_harness: "claude-code", requested_on: { id: "brd_setup", name: "replies-approval", title: "" }, boards: [] } };

test("opening an approval leaves one row selected, and K walks back into the replies waiting", async ({ page }) => {
  const board = "replies-approval";
  addBoard(board);
  await page.route("**/v1/me/approvals*", (route) => route.fulfill({ json: { approvals: [decided] } }));
  await signIn(page, board);
  await inboxLoaded(page);
  await waitFor(board, "Approval neighbour waiting");
  await replyRows(page).filter({ hasText: "Approval neighbour waiting" }).click();
  await expect(selected(page)).toContainText("Approval neighbour waiting");
  await page.getByRole("list", { name: "Decided" }).getByRole("button").click();
  await expect(page.locator(`[data-approval="${decided.id}"]`)).toBeVisible();
  await expect(page.locator("button[aria-current=true]")).toHaveCount(1);
  await expect(selected(page)).toHaveCount(0);
  // The Decided group sits below the list: K goes to its last row.
  await page.keyboard.press("k");
  const last = await page.locator("button:is([data-ask], [data-reply])").last().getAttribute("data-reply");
  await expect(selected(page)).toHaveAttribute("data-reply", last!);
  await expect(page.locator(`[data-approval="${decided.id}"]`)).toHaveCount(0);
});

test("a reply in place names everyone in the thread, marks the message read, and the board credits the right person", async ({ page }) => {
  const board = "replies-thread";
  addBoard(board);
  await signIn(page, board);
  const invite = await api(ownerToken(), "POST", "/v1/invites", {});
  const made = await api<{ key: { token: string } }>("", "POST", "/v1/connect", { invite: invite.invite, handle: "sam", key_name: "web-fixture" });
  await api(ownerToken(), "POST", `/v1/boards/${board}/people`, { handle: "sam" });
  // Posted while alex is in the Inbox, so no board view has shown them.
  await inboxLoaded(page);
  const waiting = await waitFor(board, "Which install path should the docs lead with?");
  await api(made.key.token, "POST", `/v1/boards/${board}/messages`, { body: "The script, I think.", reply_to: waiting.id });

  // Before alex replies, the board says sam replied, not that the question is answered.
  // This look acknowledges nothing, so the read position below is the Inbox's doing.
  const look = await page.context().newPage();
  await look.route(`**/v1/boards/${board}/ack`, (route) => route.fulfill({ status: 500, json: { error: { code: "test", message: "Not acknowledged in this check.", hint: "" } } }));
  await look.goto(`${base()}/?board=${board}`);
  await expect(look.locator("button.answered")).toHaveText("sam replied");
  await look.close();
  const before = await api<{ read_up_to: number }>(ownerToken(), "GET", `/v1/boards/${board}`);
  expect(before.read_up_to).toBeLessThan(waiting.seq);

  const rows = replyRows(page).filter({ hasText: "Which install path" });
  await rows.click();
  const box = page.getByLabel("Your reply, sent to writer and sam");
  await expect(box).toBeVisible();
  await box.fill("The script.");
  await expect(page.getByRole("button", { name: "Send to writer and sam" })).toBeVisible();
  await page.keyboard.press("ControlOrMeta+Enter");
  await expect(page.getByRole("status").filter({ hasText: "Sent to @writer and @sam" })).toContainText("The script.");
  await expect(rows).toHaveCount(0);
  const messages = await api<{ messages: Msg[] }>(ownerToken(), "GET", `/v1/boards/${board}/messages`);
  const reply = messages.messages.find((m) => m.body === "The script.")!;
  expect([...reply.to].sort()).toEqual(["@sam", "@writer"]);
  // The question is read; sam's reply, which alex never saw, holds the position there.
  const seen = await api<{ read_up_to: number }>(ownerToken(), "GET", `/v1/boards/${board}`);
  expect(seen.read_up_to).toBe(waiting.seq);

  await page.goto(`${base()}/?board=${board}`);
  await expect(page.locator("button.answered")).toHaveText("Answered by you");
});

test("a reply in place with nothing else unread marks the board read through the reply", async ({ page }) => {
  const board = "replies-read";
  addBoard(board);
  await signIn(page, board);
  await inboxLoaded(page);
  await waitFor(board, "Is the changelog ready?");
  await replyRows(page).filter({ hasText: "Is the changelog ready?" }).click();
  await page.getByLabel("Your reply, sent to writer").fill("Yes.");
  await page.keyboard.press("ControlOrMeta+Enter");
  // The sent line shows once the read position has moved; the row can leave before it.
  await expect(page.getByRole("status").filter({ hasText: "Sent to @writer" })).toContainText("Yes.");
  await expect(replyRows(page).filter({ hasText: "Is the changelog ready?" })).toHaveCount(0);
  const seen = await api<{ unread: number }>(ownerToken(), "GET", `/v1/boards/${board}`);
  expect(seen.unread).toBe(0);
});
