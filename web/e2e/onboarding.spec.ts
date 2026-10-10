import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test, type Page } from "@playwright/test";

// Onboarding in the board view against a real server, in its own home and on its own
// port: approvals your agents ask for, the allowance in Settings, notices about invites
// your agents made, pairing requests with Choose an agent and Copy prompt, the invite
// page, and the board's pairing line. A second person (sam) comes from a real server
// invite. Only the states a real harness handshake reaches (verifying, ready) are shown
// through a route fixture, since no harness runs here.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-onboarding-web-"));
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
function tokenFile(file: string): string {
  return execFileSync("find", [home, "-name", file], { encoding: "utf8" }).trim().split("\n")[0];
}
const ownerToken = () => readFileSync(tokenFile("local-owner-token"), "utf8").trim();
function seatToken(board: string): string {
  const saved = JSON.parse(readFileSync(tokenFile("credentials.json"), "utf8")) as { agents: { board: string; token: string }[] };
  const found = saved.agents.find((a) => a.board === board);
  if (!found) throw new Error("The fixture seat was not saved.");
  return found.token;
}
type Json = Record<string, unknown>;
async function api(token: string | null, method: string, path: string, body?: unknown, ok = true): Promise<{ status: number; json: Json }> {
  const headers: Record<string, string> = { "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() };
  if (token) headers.Authorization = `Bearer ${token}`;
  const r = await fetch(`${base()}${path}`, { method, headers, body: body === undefined ? undefined : JSON.stringify(body) });
  const json = (await r.json().catch(() => ({}))) as Json;
  if (ok && !r.ok) throw new Error(`${method} ${path}: ${r.status} ${JSON.stringify(json)}`);
  return { status: r.status, json };
}

let sam: { id: string; token: string };
let alexId: string;

test.beforeAll(async () => {
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = { NODE_ENV: "test", PATH: process.env.PATH, HOME: home, USER: "alex", XDG_CONFIG_HOME: join(home, ".config"), XDG_DATA_HOME: join(home, ".local/share"), XDG_STATE_HOME: join(home, ".local/state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`, BROWSER: "true" };
  aboard("pair", "general", "--board", "onboarding-setup", "--name", "writer", "--new");
  alexId = String((await api(ownerToken(), "GET", "/v1/me")).json.id);
  // sam is a second person on the server, from a real invite.
  const invite = String((await api(ownerToken(), "POST", "/v1/invites", {})).json.invite);
  const connected = (await api(null, "POST", "/v1/connect", { invite, handle: "sam", key_name: "laptop" })).json;
  sam = { id: String((connected.person as Json).id), token: String((connected.key as Json).token) };
});
test.afterAll(() => {
  try {
    if (env) aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

/** openBoard makes a board with alex's agent writer on it, and opens the board view signed in as alex. */
async function openBoard(page: Page, board: string): Promise<{ id: string; writer: string }> {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  const b = (await api(ownerToken(), "GET", `/v1/boards/${board}`)).json;
  const members = (await api(ownerToken(), "GET", `/v1/boards/${board}/members`)).json.members as { id: string; name: string }[];
  return { id: String(b.id), writer: members.find((m) => m.name === "writer")!.id };
}

/** request asks the server, as the board's writer seat, for an admin action, which it holds for alex. */
async function request(board: string, action: Json): Promise<Json> {
  const r = await api(seatToken(board), "POST", "/v1/me/admin-requests", action);
  expect(r.status).toBe(202);
  return r.json.approval as Json;
}

async function approval(id: unknown): Promise<Json> {
  const list = (await api(ownerToken(), "GET", "/v1/me/approvals?state=all")).json.approvals as Json[];
  return list.find((a) => a.id === id)!;
}

const inbox = (page: Page, item?: unknown) => page.goto(`${base()}/?inbox${item ? `&item=${item}` : ""}`);

test("an approval allowed once runs the agent's request and shows its command", async ({ page }) => {
  const board = "onboarding-once";
  await openBoard(page, board);
  const held = await request(board, { kind: "invite_people", invite: {} });
  await inbox(page);
  const card = page.locator(`[data-approval="${held.id}"]`);
  await expect(card.getByRole("heading", { name: /Your agent .*writer wants to invite someone to the server as a member/ })).toBeVisible();
  await expect(card).toContainText("writer · your agent");
  await expect(card).toContainText("This changes the whole server, not just this board.");
  await expect(card.locator(".ob-command")).toContainText(`aboard approvals allow ${held.id}`);
  await card.getByRole("button", { name: "Allow once" }).click();
  await expect(card.getByRole("status")).toContainText("Allowed once");
  expect((await approval(held.id)).state).toBe("executed");
  // The invite's link and prompt are shown once, inline, with copy buttons.
  const issued = card.locator(".ob-invite-issued");
  await expect(issued).toContainText("Shown once. Send both to the person you're inviting");
  await expect(issued).toContainText("Copy them now; they aren't shown again.");
  await expect(issued.locator("code").first()).toContainText(`${base()}/join#abi_`);
  // The prompt is the server's text for that link, not built in the page.
  const link = (await issued.locator("code").first().textContent())?.trim() ?? "";
  expect(link).toMatch(/\/join#abi_/);
  const prompt = (await issued.locator("code").nth(1).textContent()) ?? "";
  expect(prompt).toContain(link);
  await expect(issued.getByRole("button", { name: "Copy the invite link" })).toBeVisible();
  await expect(issued.getByRole("button", { name: "Copy the prompt" })).toBeVisible();
  await issued.getByRole("button", { name: "Dismiss" }).click();
  await expect(issued).toHaveCount(0);
  // The owner can revoke the issued invite from its notice using the browser session.
  const allowance = (await api(ownerToken(), "GET", "/v1/me/allowance")).json;
  expect(allowance.categories).toEqual([]);
  const notices = (await api(ownerToken(), "GET", "/v1/me/invite-notices")).json.notices as Json[];
  expect(notices).toHaveLength(1);
  await card.getByRole("button", { name: "See the invite" }).click();
  const notice = page.locator(`[data-notice="${notices[0].id}"]`);
  await expect(notice.getByRole("heading", { name: /Your agent .*writer invited someone/ })).toBeVisible();
  await expect(notice).toContainText("Open");
  // Edit handle changes only the suggested handle, from the browser session.
  await notice.getByRole("button", { name: "Edit handle" }).click();
  await notice.getByLabel(/Suggested handle/).fill("Not Valid");
  await notice.getByRole("button", { name: "Save" }).click();
  await expect(notice.getByRole("alert")).toContainText("lowercase letters, digits and hyphens");
  await notice.getByLabel(/Suggested handle/).fill("river");
  const edited = page.waitForResponse((r) => r.request().method() === "PATCH" && r.url().endsWith(`/v1/invites/${notices[0].id}`));
  await notice.getByRole("button", { name: "Save" }).click();
  const patched = await edited;
  expect(patched.status()).toBe(200);
  expect((await patched.request().allHeaders())["x-aboard-csrf"]).toBeTruthy();
  await expect(notice).toContainText("Invited as @river");
  // The server's refusal shows in its own words and leaves the shown handle alone.
  await page.route(`**/v1/invites/${notices[0].id}`, (route) => route.request().method() === "PATCH"
    ? route.fulfill({ status: 409, json: { error: { code: "invite_unavailable", message: "This invitation can no longer be edited.", hint: "Make a new invitation." } } })
    : route.continue());
  await notice.getByRole("button", { name: "Edit handle" }).click();
  await notice.getByLabel(/Suggested handle/).fill("lake");
  await notice.getByRole("button", { name: "Save" }).click();
  await expect(notice.getByRole("alert")).toContainText("This invitation can no longer be edited.");
  await notice.getByRole("button", { name: "Cancel" }).click();
  await expect(notice).toContainText("Invited as @river");
  await page.unroute(`**/v1/invites/${notices[0].id}`);
  const revoked = page.waitForResponse((r) => r.request().method() === "DELETE" && r.url().endsWith(`/v1/invites/${notices[0].id}`));
  await notice.getByRole("button", { name: "Revoke the invite" }).click();
  const response = await revoked;
  expect(response.status()).toBe(200);
  const headers = await response.request().allHeaders();
  expect(headers["x-aboard-csrf"]).toBeTruthy();
  expect(headers.origin).toBe(base());
  expect(((await api(ownerToken(), "GET", "/v1/me/invite-notices")).json.notices as Json[])[0].state).toBe("revoked");
  await expect(page.locator(`[data-notice="${notices[0].id}"]`).getByRole("status")).toContainText("Revoked");
});

test("Allow always on an invite warns first, then turns on inviting", async ({ page }) => {
  const board = "onboarding-always";
  await openBoard(page, board);
  const held = await request(board, { kind: "invite_people", invite: {} });
  await inbox(page, held.id);
  const card = page.locator(`[data-approval="${held.id}"]`);
  await card.getByRole("button", { name: "Allow always" }).click();
  await expect(card.locator(".ob-warning")).toContainText("could read every open board on this server");
  expect((await approval(held.id)).state).toBe("pending");
  await card.locator(".ob-warning").getByRole("button", { name: "Allow always" }).click();
  await expect(card.getByRole("status")).toContainText("Allowed always");
  await expect(card.locator(".ob-warning")).toContainText("Agents allowed to invite people can let outsiders read every open board.");
  expect((await api(ownerToken(), "GET", "/v1/me/allowance")).json.categories).toContain("invite-people");
  await api(ownerToken(), "PUT", "/v1/me/allowance", { categories: [] });
});

test("a request that always asks has no Allow always", async ({ page }) => {
  const board = "onboarding-risky";
  await openBoard(page, board);
  const held = await request(board, { kind: "set_server_role", person_id: sam.id, role: "admin" });
  await inbox(page, held.id);
  const card = page.locator(`[data-approval="${held.id}"]`);
  await expect(card.getByRole("heading", { name: /writer wants to make sam a server admin/ })).toBeVisible();
  await expect(card.getByRole("button", { name: "Allow once" })).toBeVisible();
  await expect(card.getByRole("button", { name: "Allow always" })).toHaveCount(0);
  await expect(card).toContainText("Changing someone's role always asks you.");
  await card.getByRole("button", { name: "Decline" }).click();
  await expect(card.getByRole("status")).toContainText("Declined");
  expect((await approval(held.id)).state).toBe("declined");
});

test("Settings turns on auto mode, and inviting only after its warning", async ({ page }) => {
  await openBoard(page, "onboarding-settings");
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await expect(page.getByRole("menuitem", { name: /Settings/ })).toContainText("Agents ask you");
  await page.getByRole("menuitem", { name: /Settings/ }).click();
  await expect(page.getByRole("heading", { name: "What your agents may do for you" })).toBeVisible();
  await page.getByRole("switch", { name: "Auto mode" }).click();
  await expect.poll(async () => (await api(ownerToken(), "GET", "/v1/me/allowance")).json.categories).toEqual(["add-people"]);
  await page.getByRole("switch", { name: "Invite people to the server" }).click();
  await expect(page.locator(".ob-warning")).toContainText("Let your agents invite people without asking?");
  expect((await api(ownerToken(), "GET", "/v1/me/allowance")).json.categories).toEqual(["add-people"]);
  await page.getByRole("button", { name: "Turn on inviting" }).click();
  await expect(page.locator(".ob-warning")).toContainText("Agents allowed to invite people can let outsiders read every open board.");
  expect(((await api(ownerToken(), "GET", "/v1/me/allowance")).json.categories as string[]).sort()).toEqual(["add-people", "invite-people"]);
  // Auto mode off turns everything off, as aboard allowance off does.
  await page.getByRole("switch", { name: "Auto mode" }).click();
  await expect.poll(async () => (await api(ownerToken(), "GET", "/v1/me/allowance")).json.categories).toEqual([]);
});

/** pairFromSam puts sam on the board with an agent of theirs, which asks alex's agents to pair. */
async function pairFromSam(board: string, boardId: string, work: string): Promise<Json> {
  await api(ownerToken(), "POST", `/v1/boards/${board}/people`, { handle: "sam" });
  const seat = (await api(sam.token, "POST", "/v1/join", { board, role: "member", name: "helper" })).json;
  const agent = seat.agent as Json;
  return (await api(String(seat.token), "POST", "/v1/pairing-requests", { board_id: boardId, recipient_id: alexId, initiating_agent_id: agent.id, work })).json;
}

test("a pairing request: Choose an agent sends it to that agent, and Copy prompt copies the accept command", async ({ page, context }) => {
  const board = "onboarding-pairing";
  const b = await openBoard(page, board);
  const p = await pairFromSam(board, b.id, "Review the auth change together.");
  // The writer seat's session reports itself idle, as a running harness would; only a live agent is offered.
  await api(seatToken(board), "PUT", "/v1/me/presence", { presence: "idle" });
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: base() });
  await inbox(page, p.id);
  const card = page.locator(`[data-pairing="${p.id}"]`);
  await expect(card.getByRole("heading", { name: /sam's agent .*helper asks your agents to pair on onboarding-pairing/ })).toBeVisible();
  await expect(card.locator("blockquote")).toHaveText("Review the auth change together.");
  // The board's line says what the request waits on.
  await card.getByRole("button", { name: "Copy prompt" }).click();
  await expect(card.locator(".ob-prompt")).toContainText(`aboard pairing accept ${p.id} --here`);
  expect(await page.evaluate(() => navigator.clipboard.readText())).toContain(`aboard pairing accept ${p.id} --here`);
  await card.getByRole("button", { name: "Choose an agent" }).click();
  const chooser = card.locator(".ob-choose");
  await expect(chooser.getByRole("radio", { name: /writer/ })).toBeVisible();
  const chose = page.waitForRequest((r) => r.url().endsWith(`/v1/pairing-requests/${p.id}/choose`) && r.method() === "POST");
  await chooser.getByRole("radio", { name: /writer/ }).click();
  await chooser.getByRole("button", { name: "Send it to this agent" }).click();
  const sent = await chose;
  expect(sent.headers()["x-aboard-csrf"]).toBeTruthy();
  expect(sent.headers()["idempotency-key"]).toBeTruthy();
  expect(JSON.parse(sent.postData() ?? "{}")).toEqual({ agent_id: b.writer, generation: 1 });
  await expect(card).toContainText("Sent to writer");
  const now = (await api(ownerToken(), "GET", `/v1/pairing-requests/${p.id}`)).json;
  expect(now.chosen_recipient_agent_id).toBe(b.writer);
});

test("the board's pairing line follows the request, and says verified only when ready", async ({ page }) => {
  const board = "onboarding-line";
  const b = await openBoard(page, board);
  const p = await pairFromSam(board, b.id, "Check delivery.");
  await page.goto(`${base()}/?board=${board}`);
  const line = page.locator(".pairing-line");
  await expect(line).toContainText("Choose an agent to take part");
  // Verifying and ready need both agents' real sessions to exchange checks; no harness runs
  // here, so the server's answer is replaced with those states.
  for (const [state, words] of [
    ["verifying", "Verifying delivery with sam's agent…"],
    ["ready", "Ready: both agents connected, delivery verified"],
  ] as const) {
    await page.route("**/v1/pairing-requests", async (route) => {
      const resp = await route.fetch();
      const body = (await resp.json()) as { requests: Json[] };
      await route.fulfill({ response: resp, json: { requests: body.requests.map((r) => (r.id === p.id ? { ...r, state } : r)) } });
    });
    await page.reload();
    await expect(line).toContainText(words);
    await page.unroute("**/v1/pairing-requests");
  }
  await expect(line).not.toContainText("Verifying");
});

test("an agent's join on its person's approval reads so in the timeline", async ({ page }) => {
  const board = "onboarding-record";
  const b = await openBoard(page, board);
  const held = await request(board, { kind: "add_people", board_id: b.id, person_id: sam.id });
  await inbox(page, held.id);
  await page.locator(`[data-approval="${held.id}"]`).getByRole("button", { name: "Allow once" }).click();
  await expect(page.locator(`[data-approval="${held.id}"]`).getByRole("status")).toContainText("Allowed once");
  await page.goto(`${base()}/?board=${board}`);
  await expect(page.getByRole("log", { name: "Timeline" })).toContainText("sam joined as member · added by writer, approved by alex");
});

test("the invite page previews a real invite link without using it", async ({ page }) => {
  const board = "onboarding-invite-page";
  await openBoard(page, board);
  const invite = String((await api(ownerToken(), "POST", "/v1/invites", {})).json.invite);
  await page.goto(`${base()}/join#${invite}`);
  await expect(page.getByRole("heading", { name: "alex invited you to aboard" })).toBeVisible();
  // The page shows the prompt the server's preview returns, word for word.
  const preview = (await api(null, "POST", "/v1/invites/preview", { invite })).json;
  expect(String(preview.prompt)).toContain(invite);
  await expect(page.getByLabel("Prompt for your agent")).toHaveText(String(preview.prompt));
  if (preview.suggested_handle) await expect(page.getByText(`Invited as @${preview.suggested_handle}`)).toBeVisible();
  await expect(page.getByRole("button", { name: "Or set it up in a terminal" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Copy prompt" })).toBeVisible();
  await expect(page.getByRole("main")).not.toContainText(/pair/i);
  // Previewing never spends the invite: it still makes an account.
  const connected = await api(null, "POST", "/v1/connect", { invite, handle: "dana", key_name: "laptop" });
  expect(connected.status).toBe(201);
  await page.reload();
  await expect(page.getByRole("heading", { name: "This invite can't be used" })).toBeVisible();
});
