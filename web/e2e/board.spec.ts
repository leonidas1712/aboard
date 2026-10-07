import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Page, type Response, type Route, expect, test } from "@playwright/test";
import type { Board } from "../app/api";
import { modeRules, settableModes } from "../app/delivery-modes.gen";
import { ReadProgress } from "../app/read-progress";

// One isolated machine: its own home directory, local server port and aboard binary
// built with the UI embedded. Nothing touches the real home directory.
const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-web-e2e-"));
const bin = join(home, "aboard");
let env: Record<string, string | undefined> = {};

function aboard(...args: string[]): string {
  return execFileSync(bin, args, { cwd: env.HOME ?? home, env: env as NodeJS.ProcessEnv, encoding: "utf8" });
}

function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const srv = createServer();
    srv.on("error", fail);
    srv.listen(0, "127.0.0.1", () => {
      const { port } = srv.address() as { port: number };
      srv.close(() => done(port));
    });
  });
}

test.beforeAll(async () => {
  execFileSync("go", ["build", "-tags", "ui", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = {
    PATH: process.env.PATH,
    HOME: home,
    USER: "alex",
    XDG_CONFIG_HOME: join(home, ".config"),
    XDG_DATA_HOME: join(home, ".local", "share"),
    XDG_STATE_HOME: join(home, ".local", "state"),
    ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`,
    BROWSER: "true", // the test opens the link itself
  };
});

test.afterAll(() => {
  try {
    aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

// The board view runs under a strict Content-Security-Policy; every test fails if the
// browser reports breaking it, which would mean a script the policy doesn't allow.
let violations: string[] = [];
test.beforeEach(({ page }) => {
  violations = [];
  page.on("console", (m) => {
    if (m.text().includes("Content Security Policy")) violations.push(m.text());
  });
});
test.afterEach(async ({ page }) => {
  expect(violations).toEqual([]);
  // A test that holds requests may leave one in flight as it ends; it isn't answered.
  await page.unrouteAll({ behavior: "ignoreErrors" });
});

// base is the local server's address, as aboard open links to it.
function base(): string {
  return `http://${env.ABOARD_LOCAL_ADDR}`;
}

// openLink opens an aboard open link in a browser with no session, as alex: the page asks
// first, naming alex, and signs in on Continue. It returns the page's response.
async function openLink(page: Page, url: string): Promise<Response | null> {
  const resp = await page.goto(url);
  await expect(page.getByRole("heading", { name: /^Sign in to .* as @alex\?$/ })).toBeVisible();
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  return resp;
}

// agentToken reads an agent's token from the isolated home's saved credentials, to act
// as that agent's delivery daemon would.
function agentToken(name: string): string {
  const found = execFileSync("find", [home, "-name", "credentials.json"], { encoding: "utf8" }).trim().split("\n")[0];
  const creds = JSON.parse(readFileSync(found, "utf8")) as { agents: { name: string; token: string }[] };
  const agent = creds.agents.find((a) => a.name === name);
  if (!agent) throw new Error(`no saved token for ${name}`);
  return agent.token;
}

test("the board view shows the room live, posts as the person and verifies the record", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--title", "Docs review", "--json"));
  aboard("join", pair.join.line);

  const open = JSON.parse(aboard("open", "--json"));
  expect(open.url).toMatch(/\/#code=abl_[^&]+&board=writer-reviewer$/);
  await openLink(page, open.url);

  // The page swaps the code for a session in a cookie its scripts can't read: the address
  // bar loses the code, and nothing secret is in the page's storage.
  await expect(page).toHaveURL(/\/\?board=writer-reviewer$/);
  expect(page.url()).not.toContain("code");
  expect(await page.evaluate(() => document.cookie)).toBe("");
  const cookies = await page.context().cookies();
  expect(cookies.map((c) => [c.name.startsWith("aboard_session_"), c.httpOnly, c.sameSite, c.value.startsWith("abb_")])).toEqual([
    [true, true, "Lax", true],
  ]);
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }));
  expect(stored).not.toContain("abb_");
  expect(stored).not.toContain("abh_");

  // The header shows the board's title with its name beside it.
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Docs review");
  await expect(page.getByRole("heading", { level: 1 })).toContainText("writer-reviewer");

  // The tab shows Aboard's icon: every icon the head links to is served with its type.
  const icons = await page.locator('link[rel="icon"], link[rel="apple-touch-icon"]').evaluateAll((links) =>
    links.map((l) => [(l as HTMLLinkElement).href, l.getAttribute("type") ?? ""]),
  );
  expect(icons.map(([, type]) => type).sort()).toEqual(["image/png", "image/png", "image/svg+xml"]);
  for (const [href, type] of icons) {
    const resp = await page.request.get(href);
    expect(resp.status()).toBe(200);
    expect(resp.headers()["content-type"]).toBe(type);
  }

  // The top bar says who you are, on which server, and keeps this browser's theme.
  const account = page.getByRole("button", { name: /^You are alex/ });
  await expect(account).toContainText("alex");
  await account.click();
  const menu = page.locator(".account-menu");
  await expect(menu).toContainText("This computer (local)");
  await expect(menu).not.toContainText("Admin");
  await page.getByRole("menuitemradio", { name: "Dark" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await page.getByRole("menuitemradio", { name: "Same as this computer" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", /./);
  await page.keyboard.press("Escape");

  // A new board says what to do next, shows the starter policy and its verified record.
  await expect(page.getByText("Nothing has been said on this board yet.")).toBeVisible();
  await expect(page.getByRole("banner").getByText("Starter policy")).toBeVisible();
  await expect(page.locator(".record")).toContainText(/Record verified · \d+ events/);
  // The header shows Aboard's own mark, drawn from the theme's colours.
  const mark = page.getByRole("banner").getByRole("link", { name: "aboard" }).locator("svg");
  await expect(mark.locator("rect")).toHaveCount(3);

  // The left panel is navigation only: the boards, the current one selected, each with
  // its message count. Nothing about one board sits there.
  const nav = page.getByRole("complementary", { name: "Boards" });
  await expect(nav.getByRole("navigation", { name: "Boards" }).getByRole("link", { name: /Docs review/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
  await expect(nav.locator(".unread-count")).toHaveCount(0);
  await expect(nav.locator(".charter, .record, .agent")).toHaveCount(0);
  await expect(nav).not.toContainText("Rules Aboard enforces");

  // The right panel is this board, headed by its title, in sections: agents, charter,
  // rules and details. An agent with no session reads "disconnected", here and in the
  // "Now:" line.
  const crew = page.getByRole("complementary", { name: "Docs review" });
  for (const section of ["Agents", "Charter", "Rules Aboard enforces", "Details"]) {
    await expect(crew.getByRole("heading", { level: 3, name: section })).toBeVisible();
  }
  await expect(crew.getByRole("button", { name: "Add an agent" })).toBeVisible();
  await expect(crew.locator('[data-agent="writer"]')).toBeVisible();
  await expect(crew.locator('[data-agent="reviewer"]')).toContainText("disconnected");
  await expect(page.locator(".now")).toContainText("2 agents disconnected");
  await expect(crew.locator(".board-facts")).toContainText("writer-reviewer");
  await expect(crew.locator(".board-facts")).toContainText("Starter policy");

  // The charter reads as paragraphs, not the template's hard line breaks, and both the
  // charter and the enforced rules explain themselves.
  const charter = page.locator(".charter");
  await expect(charter.locator("p").first()).toContainText("one document together. The writer drafts");
  await page.getByRole("button", { name: "About Charter" }).hover();
  await expect(page.locator(".help-text").first()).toContainText("Every agent reads it when it joins");
  await page.mouse.move(0, 0);
  await page.getByRole("button", { name: "About Rules Aboard enforces" }).focus();
  await expect(page.locator(".help-text").first()).toContainText("checked by the server on every message");

  // The message box is one field, marked focused while any part of it has focus.
  const field = page.locator(".composer-field");
  await expect(field).not.toHaveAttribute("data-focused", /./);
  await page.getByRole("combobox", { name: "Message everyone" }).focus();
  await expect(field).toHaveAttribute("data-focused", "true");
  await page.getByRole("combobox", { name: "Message everyone" }).blur();
  await expect(field).not.toHaveAttribute("data-focused", /./);

  // Joins show inline as board events; the Filter panel hides them, a chip says so, and
  // removing the chip shows them again.
  const joins = page.locator(".board-event", { hasText: "reviewer joined as reviewer" });
  await expect(joins).toBeVisible();
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("menuitemcheckbox", { name: "Show board events" }).click();
  await page.keyboard.press("Escape");
  await expect(joins).toHaveCount(0);
  await page.getByRole("button", { name: "Remove filter: Board events hidden" }).click();
  await expect(joins).toBeVisible();

  // A message from the CLI arrives live, as text, never HTML.
  aboard("say", "--as", "writer", "--to", "@reviewer", "Draft is in notes.md. <b>not bold</b>");
  await expect(page.getByText("Draft is in notes.md. <b>not bold</b>")).toBeVisible();
  await expect(page.locator(".message b")).toHaveCount(0);

  // Presence reported for an agent shows in who's here, without a reload.
  const resp = await fetch(`http://${env.ABOARD_LOCAL_ADDR}/v1/me/presence`, {
    method: "PUT",
    headers: { Authorization: `Bearer ${agentToken("reviewer")}`, "Content-Type": "application/json" },
    body: JSON.stringify({ presence: "working" }),
  });
  expect(resp.status).toBe(200);
  await expect(crew.locator('[data-agent="reviewer"] .presence')).toHaveText("working");

  // The browser posts as the person; the CLI reads it back.
  await page.getByRole("combobox", { name: "Message everyone" }).fill("Thanks both. Ship it after the review.");
  await page.getByRole("button", { name: "Post" }).click();
  await expect(page.locator(".message", { hasText: "Ship it after the review." })).toContainText("You");
  const read = JSON.parse(aboard("read", "--as", "writer", "--json"));
  const mine = read.messages.find((m: { body: string }) => m.body === "Thanks both. Ship it after the review.");
  expect(mine.from.kind).toBe("human");
  expect(mine.to).toEqual(["all"]);
  await expect(page.locator(".record")).toContainText(/Record verified · \d+ events/);

  // Messages from one sender in a row share one header; another sender starts a new one.
  aboard("say", "--as", "writer", "--to", "@reviewer", "Second thought: the intro needs a diagram.");
  aboard("say", "--as", "writer", "--to", "@reviewer", "And the glossary link is broken.");
  aboard("say", "--as", "reviewer", "--to", "@writer", "Agreed, I'll sketch one.");
  await expect(page.locator(".message", { hasText: "Second thought" })).not.toHaveAttribute("data-grouped", "true");
  await expect(page.locator(".message", { hasText: "And the glossary link" })).toHaveAttribute("data-grouped", "true");
  await expect(page.locator(".message", { hasText: "Agreed, I'll sketch one." })).not.toHaveAttribute("data-grouped", "true");

  // Clicking a member in Who's here filters the timeline to them; the chip removes it.
  const writerSays = page.locator(".message", { hasText: "Draft is in notes.md." });
  const reviewerSays = page.locator(".message", { hasText: "Agreed, I'll sketch one." });
  await crew.locator('[data-agent="reviewer"] .member-filter').click();
  await expect(page.getByRole("button", { name: "Remove filter: From reviewer" })).toBeVisible();
  await expect(writerSays).toHaveCount(0);
  await expect(reviewerSays).toBeVisible();
  await page.getByRole("button", { name: "Remove filter: From reviewer" }).click();
  await expect(writerSays).toBeVisible();

  // The Filter panel does the same, with the API's filters.
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("menuitem", { name: /^From/ }).click();
  await page.getByRole("menuitemradio", { name: "writer" }).click();
  await page.keyboard.press("Escape");
  const chip = page.getByRole("button", { name: "Remove filter: From writer" });
  await expect(chip).toBeVisible();
  await expect(reviewerSays).toHaveCount(0);
  await expect(writerSays).toBeVisible();
  await chip.click();
  await expect(chip).toHaveCount(0);
  await expect(reviewerSays).toBeVisible();

  // The selected board shows only messages the person has not seen, not its total.
  await expect.poll(() => unreadOn("writer-reviewer")).toBe(0);
  await expect(nav.locator(".unread-count")).toHaveCount(0);

  // A closed section and a side panel collapsed to its strip stay that way after a
  // reload, and open again.
  await page.setViewportSize({ width: 1280, height: 800 });
  await crew.getByRole("button", { name: "Charter", exact: true }).click();
  await expect(charter).toBeHidden();
  await page.getByRole("button", { name: "Hide board panel" }).click();
  await expect(crew.locator('[data-agent="writer"]')).toBeHidden();
  await page.reload();
  await expect(page.getByRole("button", { name: "Show board panel" })).toBeVisible();
  await expect(crew.locator('[data-agent="writer"]')).toBeHidden();
  await page.getByRole("button", { name: "Show board panel" }).click();
  await expect(crew.locator('[data-agent="writer"]')).toBeVisible();
  await expect(charter).toBeHidden();
  await crew.getByRole("button", { name: "Charter", exact: true }).click();
  await expect(charter).toBeVisible();

  // The board's title in the header opens the board panel, even when hidden, at Details,
  // and opens that section if it was closed.
  await crew.getByRole("button", { name: "Details", exact: true }).click();
  await expect(crew.locator(".board-facts")).toBeHidden();
  await page.getByRole("button", { name: "Hide board panel" }).click();
  await page.getByRole("button", { name: /Docs review.*board details/ }).hover();
  await expect(page.getByRole("tooltip")).toHaveText("Board details");
  await page.getByRole("button", { name: /Docs review.*board details/ }).click();
  await expect(crew.locator(".board-facts")).toBeInViewport();
  await expect(crew.getByRole("button", { name: "Details", exact: true })).toBeFocused();
  await expect(page.getByRole("button", { name: "Hide board panel" })).toBeVisible();

  await page.screenshot({ path: process.env.ABOARD_SCREENSHOT ?? "test-results/board-view.png", fullPage: true });

  // The list of boards shows the title, the name beside it, and the board's facts.
  await page.getByRole("link", { name: "aboard" }).click();
  const row = page.locator(".board-row", { hasText: "Docs review" });
  await expect(row.getByRole("link", { name: "Docs review" })).toBeVisible();
  await expect(row).toContainText("writer-reviewer");
  await expect(row).toContainText("2 agents");
  await expect(row).toContainText("Starter policy");
  // The count comes from the board itself: five messages were posted above.
  await expect(row.locator(".messages")).toContainText("5");
  aboard("say", "--as", "reviewer", "--to", "@writer", "One more.");
  await expect(row.locator(".messages")).toContainText("6");

  // The session outlasts a restart of the server; once the person ends it, the page shows
  // the login page.
  aboard("down");
  aboard("up");
  await page.reload();
  await expect(page.locator(".board-row", { hasText: "Docs review" })).toBeVisible();
  aboard("logout", "--browsers");
  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();
});

test("the board panel shows the board's details and adds an agent with a prompt the CLI can join with", async ({ page, context }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Invite check", "--json"));
  const board: string = pair.board.name;
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(open.url).origin });
  await openLink(page, open.url);

  // The board's title opens the board panel at Details, with the board's facts.
  await page.getByRole("button", { name: /Invite check.*board details/ }).click();
  const panel = page.getByRole("complementary", { name: "Invite check" });
  const details = panel.locator(".board-facts");
  await expect(details).toBeInViewport();
  await expect(details).toContainText(board);
  await expect(details).toContainText(pair.board.id);
  await expect(details).toContainText(new URL(open.url).origin);
  await expect(details).toContainText("Starter policy");

  // Copy details puts the facts on the clipboard as plain text.
  await details.getByRole("button", { name: "Copy details" }).click();
  await expect(details.locator(".copy-status")).toContainText("Copied");
  const facts = await page.evaluate(() => navigator.clipboard.readText());
  expect(facts).toContain(`Board: Invite check (${board})`);
  expect(facts).toContain(`ID: ${pair.board.id}`);
  expect(facts).toContain("Agents: 1\nPeople: 1");

  // Add an agent, at the top of the agents section, makes a code for the member role and
  // shows the prompt; picking another role (the board has several) makes a new one.
  await panel.getByRole("button", { name: "Add an agent" }).click();
  const add = panel.locator(".add-agent");
  await expect(add.locator(".invite-prompt")).toContainText(`Join Aboard board ${board} on `);
  await expect(add.locator(".invite-prompt")).toContainText(" as member with code ");
  await add.getByLabel("Joins as").selectOption("reviewer");
  await expect(add.locator(".invite-prompt")).toContainText(" as reviewer with code ");
  await expect(add).toContainText("Joins as reviewer. Works until");
  await add.getByRole("button", { name: "Copy prompt" }).click();
  await expect(add.locator(".copy-status")).toContainText("Copied");
  const prompt = await page.evaluate(() => navigator.clipboard.readText());
  const [line, sentence] = prompt.split("\n");
  expect(line).toMatch(new RegExp(`^Join Aboard board ${board} on localhost:\\d+ as reviewer with code [0-9A-Z]{3}-[0-9A-Z]{3}$`));
  expect(sentence).toBe("You have the Aboard skill. Join with this line, read the charter in the join output, then say hello on the board.");
  await expect(add.locator(".copy-status")).toBeEmpty();

  // Closing it puts focus back on the button.
  await add.getByRole("button", { name: "Close Add an agent" }).click();
  await expect(panel.getByRole("button", { name: "Add an agent" })).toBeFocused();

  // The copied prompt joins a new agent from the CLI, and it shows up on the board.
  const joined = JSON.parse(aboard("join", line, "--name", "invited", "--json"));
  expect(joined.board.name).toBe(board);
  expect(joined.agent.role).toBe("reviewer");
  await expect(panel.locator('[data-agent="invited"]')).toBeVisible();
});

// This machine's server is a local one, so the page is told it is on a team server
// (GET /v1/info) to show what a team server's people see; the rest is the real server.
test("on a team server, Add an agent gives the join command for the person's own agents, with no code", async ({ page, context }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Team invite", "--json"));
  const board: string = pair.board.name;
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(open.url).origin });
  await page.route("**/v1/info", async (route) => {
    const resp = await route.fetch();
    await route.fulfill({ response: resp, json: { ...(await resp.json()), mode: "team" } });
  });
  const codes: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/join-codes")) codes.push(r.url());
  });
  await openLink(page, open.url);

  await page.getByRole("button", { name: /Team invite.*board details/ }).click();
  const panel = page.getByRole("complementary", { name: "Team invite" });
  await panel.getByRole("button", { name: "Add an agent" }).click();
  const add = panel.locator(".add-agent");
  const server = new URL(open.url).origin;
  await expect(add.locator(".invite-prompt")).toContainText(`aboard join --board ${board} --server ${server}`);
  await expect(add).toContainText("no code needed");
  await add.getByLabel("Joins as").selectOption("reviewer");
  await expect(add.locator(".invite-prompt")).toContainText(`aboard join --board ${board} --role reviewer --server ${server}`);
  await add.getByRole("button", { name: "Copy prompt" }).click();
  const prompt = await page.evaluate(() => navigator.clipboard.readText());
  expect(prompt.split("\n")).toEqual([
    `aboard join --board ${board} --role reviewer --server ${server}`,
    "You have the Aboard skill. Run this command to join, read the charter in the join output, then say hello on the board.",
  ]);
  expect(codes).toEqual([]);
});

// A guest's agents come only from guest codes, so the board view offers a guest no
// Add an agent.
test("a guest of a team server sees no Add an agent", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Guest view", "--json"));
  const board: string = pair.board.name;
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.route("**/v1/me", async (route) => {
    const resp = await route.fetch();
    await route.fulfill({ response: resp, json: { ...(await resp.json()), server_role: "guest" } });
  });
  await openLink(page, open.url);
  await page.getByRole("button", { name: /Guest view.*board details/ }).click();
  const panel = page.getByRole("complementary", { name: "Guest view" });
  await expect(panel.locator(".board-facts")).toBeVisible();
  await expect(panel.locator('[data-agent="writer"]')).toBeVisible();
  await expect(panel.getByRole("button", { name: "Add an agent" })).toHaveCount(0);
});

test("replies form threads that open in place, remember how they were left and surface what is new", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Threads", "--json"));
  const board: string = pair.board.name;
  aboard("join", pair.join.line);
  const say = (...args: string[]) => JSON.parse(aboard("say", "--board", board, "--json", ...args)).message;
  const ask = say("--as", "writer", "--to", "@reviewer", "The retry design is in design.md. Can you review it?");
  const first = say("--as", "reviewer", "--reply", ask.id, "--to", "@writer", "The jitter range is too narrow.");
  say("--as", "writer", "--reply", first.id, "--to", "@reviewer", "Fair, switching to full jitter.");
  say("--as", "reviewer", "Tests pass locally.");

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);

  // The thread's first message says how many replies and when the last came; a reply to
  // a reply is in the same thread, and closed, the replies stay out of the timeline.
  const thread = page.locator(`[data-thread="${ask.id}"]`);
  const toggle = thread.locator(".thread-toggle");
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  await expect(toggle).toContainText("2 replies");
  await expect(toggle).toContainText("last just now");
  await expect(page.getByText("The jitter range is too narrow.")).toHaveCount(0);
  await expect(page.locator(".message", { hasText: "Tests pass locally." })).toBeVisible();

  // Opened, the replies show in order, one level deep, and stay open after a reload.
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(thread.locator(".reply .body")).toHaveText(["The jitter range is too narrow.", "Fair, switching to full jitter."]);
  await page.reload();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await toggle.click();
  await page.reload();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");

  // A reply that arrives while the thread is closed shows as new, there and in the "Now:"
  // line, which opens the thread at it.
  say("--as", "reviewer", "--reply", ask.id, "--to", "@writer", "One more: cap the delay.");
  await expect(thread.locator(".thread-new")).toHaveText("1 new");
  await expect(toggle).toContainText("3 replies");
  const fresh = page.locator(".now").getByRole("button", { name: "1 new reply in a thread" });
  await fresh.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "true");
  await expect(thread.locator(".message", { hasText: "One more: cap the delay." })).toBeInViewport();
  await expect(thread.locator(".thread-new")).toHaveCount(0);
  await expect(fresh).toHaveCount(0);

  // Replying in the thread posts a reply to its first message, shown in the thread.
  await thread.getByRole("button", { name: "Reply in thread" }).click();
  await expect(page.locator(".composer")).toContainText("Replying to writer");
  await page.getByRole("combobox", { name: "Message writer and reviewer" }).fill("Looks good once the cap is in.");
  await page.getByRole("button", { name: "Post" }).click();
  await expect(thread.locator(".message", { hasText: "Looks good once the cap is in." })).toContainText("You");
  expect(aboard("read", "--as", "writer", "--board", board, "--thread", String(ask.seq))).toContain("Looks good once the cap is in.");

  // A question to the person inside a closed thread still waits for them: in the thread,
  // and in the "Now:" line, which opens the thread at the question.
  await toggle.click();
  await expect(toggle).toHaveAttribute("aria-expanded", "false");
  say("--as", "reviewer", "--reply", ask.id, "--to", "@alex", "--expect-reply", "Alex, can I merge after the cap?");
  await expect(thread).toContainText("reviewer is waiting for your reply in this thread.");
  await page.locator(".now").getByRole("button", { name: "reviewer asked you a question" }).click();
  const asked = thread.locator(".message", { hasText: "Alex, can I merge after the cap?" });
  await expect(asked).toBeInViewport();
  await expect(asked).toContainText("reviewer is waiting for your reply.");
  await asked.getByRole("button", { name: "Reply", exact: true }).click();
  await page.getByRole("combobox", { name: "Message reviewer and writer" }).fill("Yes, merge it.");
  await page.getByRole("button", { name: "Post" }).click();
  await expect(asked).not.toContainText("waiting for your reply");
  await expect(page.locator(".now")).toContainText("nothing waiting on you");

  // A filter finds replies inside threads: the thread shows its first message with only
  // the replies that match, open.
  await page.getByRole("button", { name: "Filter" }).click();
  await page.getByRole("menuitem", { name: /^From/ }).click();
  await page.getByRole("menuitemradio", { name: "writer" }).click();
  await page.keyboard.press("Escape");
  await expect(thread.locator(".thread-toggle")).toContainText("1 of 6 replies match");
  await expect(thread.locator(".reply .body")).toHaveText(["Fair, switching to full jitter."]);
  await expect(page.locator(".message", { hasText: "Tests pass locally." })).toHaveCount(0);
  await page.getByRole("button", { name: "Remove filter: From writer" }).click();
  await expect(thread.locator(".thread-toggle")).toContainText("6 replies");
});

test("reactions show under messages, toggle as the person and follow the board live, in threads too", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Reactions", "--json"));
  const board: string = pair.board.name;
  aboard("join", pair.join.line);
  const say = (...args: string[]) => JSON.parse(aboard("say", "--board", board, "--json", ...args)).message;
  const react = (...args: string[]) => aboard("react", "--board", board, ...args);
  const ask = say("--as", "writer", "--to", "@reviewer", "The draft is ready. Can you look?");
  const answer = say("--as", "reviewer", "--reply", ask.id, "--to", "@writer", "Looking now.");
  react("--as", "reviewer", String(ask.seq), "👍");

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);

  // A reaction shows under the message with how many, and who on hover.
  const msg = page.locator(`.message[data-id="${ask.id}"]`);
  const thumbs = msg.locator('[data-reaction="thumbsup"]');
  await expect(thumbs).toHaveText("👍1");
  await expect(thumbs).toHaveAttribute("aria-pressed", "false");
  await thumbs.hover();
  await expect(page.getByRole("tooltip")).toHaveText("reviewer reacted with 👍");

  // Clicking it adds the person's own, which the CLI then reads, and clicking again takes it back.
  await thumbs.click();
  await expect(thumbs).toHaveAttribute("aria-pressed", "true");
  await expect(thumbs).toHaveText("👍2");
  await expect(thumbs).toHaveAccessibleName("thumbs up 👍, 2: reviewer and You. Take yours back");
  expect(aboard("read", "--as", "writer", "--board", board)).toContain(`#${ask.seq}  @writer → @reviewer · 1 reply · 👍 2`);
  await thumbs.click();
  await expect(thumbs).toHaveAttribute("aria-pressed", "false");
  await expect(thumbs).toHaveText("👍1");

  // The React button beside Reply offers the whole set.
  await msg.hover();
  await msg.getByRole("button", { name: "React to writer" }).click();
  await expect(page.getByRole("menuitem")).toHaveCount(6);
  await page.getByRole("menuitem", { name: "React with celebrate" }).click();
  await expect(msg.locator('[data-reaction="tada"]')).toHaveAttribute("aria-pressed", "true");

  // An agent's reaction appears live, and goes when it is taken back.
  react("--as", "writer", String(ask.seq), "eyes");
  await expect(msg.locator('[data-reaction="eyes"]')).toHaveText("👀1");
  react("--as", "writer", String(ask.seq), "eyes", "--remove");
  await expect(msg.locator('[data-reaction="eyes"]')).toHaveCount(0);

  // Replies in a thread take reactions the same way, live and from the person.
  const thread = page.locator(`[data-thread="${ask.id}"]`);
  await thread.locator(".thread-toggle").click();
  const reply = thread.locator(`.message[data-id="${answer.id}"]`);
  react("--as", "writer", String(answer.seq), "heart");
  await expect(reply.locator('[data-reaction="heart"]')).toHaveText("❤️1");
  await reply.hover();
  await reply.getByRole("button", { name: "React to reviewer" }).click();
  await page.getByRole("menuitem", { name: "React with done" }).click();
  await expect(reply.locator('[data-reaction="check"]')).toHaveAttribute("aria-pressed", "true");
  expect(aboard("read", "--as", "reviewer", "--board", board, "--thread", String(ask.seq))).toContain(
    `#${answer.seq}  @reviewer → @writer · reply to #${ask.seq} · ✅ 1 ❤️ 1`,
  );

  // Reactions are part of the record, and it still verifies; they never show as board events.
  await page.reload();
  await expect(page.locator(".record")).toContainText(/Record verified · \d+ events/);
  await expect(page.locator(".board-event", { hasText: "react" })).toHaveCount(0);
  await expect(msg.locator('[data-reaction="tada"]')).toHaveAttribute("aria-pressed", "true");
});

test("the message box addresses by mention, and a reply adds anyone to the thread's people", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Mentions", "--json"));
  const board: string = pair.board.name;
  aboard("join", pair.join.line);
  const invite = JSON.parse(aboard("invite", "--board", board, "--json"));
  aboard("join", invite.join_line, "--name", "scout");
  const say = (...args: string[]) => JSON.parse(aboard("say", "--board", board, "--json", ...args)).message;
  type Sent = { body: string; to: string[]; reply_to: string | null };
  const posted = (body: string) =>
    (JSON.parse(aboard("read", "--as", "writer", "--board", board, "--json")).messages as Sent[]).find((m) => m.body === body);

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);

  const field = page.getByRole("combobox", { name: /^Message / });
  const list = page.getByRole("listbox", { name: "People, agents and roles to mention" });
  const to = page.locator(".to-label");
  await expect(to).toHaveText("To everyone");

  // Typing "@" offers the board's agents, then its roles; what follows filters them,
  // names that start with it first.
  await field.pressSequentially("Hi @");
  await expect(list).toBeVisible();
  await expect(field).toHaveAttribute("aria-expanded", "true");
  await expect(list.getByRole("option", { name: /^scout/ })).toBeVisible();
  await expect(list.getByRole("option", { name: /^role:reviewer/ })).toBeVisible();
  await field.pressSequentially("rev");
  await expect(list.locator(".mention-name")).toHaveText(["reviewer", "role:reviewer"]);

  // Arrow keys move the choice and Enter picks it: the name goes into the text, marked,
  // and the "To" label follows.
  await field.press("ArrowDown");
  await expect(list.getByRole("option").nth(1)).toHaveAttribute("aria-selected", "true");
  await field.press("ArrowUp");
  await field.press("Enter");
  await expect(list).toBeHidden();
  await expect(field).toHaveValue("Hi @reviewer ");
  await expect(to).toHaveText("To reviewer");
  await expect(page.locator(".mention-mark")).toHaveText(["@reviewer"]);

  // A click picks too, and so does Tab; past two names the label counts the others.
  await field.pressSequentially("and @");
  await list.getByRole("option", { name: /^scout/ }).click();
  await expect(to).toHaveText("To reviewer, scout");
  await field.pressSequentially("and @wri");
  await field.press("Tab");
  await expect(to).toHaveText("To reviewer and 2 others");
  await expect(field).toHaveAccessibleName("Message reviewer, scout and writer");

  // Escape closes the suggestions and leaves the text alone.
  await field.pressSequentially("@");
  await expect(list).toBeVisible();
  await field.press("Escape");
  await expect(list).toBeHidden();
  await expect(field).toBeFocused();

  // Taking a mention out of the text takes it out of the recipients; the post sends
  // exactly the names left, and keeps the text as written.
  await field.fill("@reviewer and @writer, please check the intro.");
  await expect(to).toHaveText("To reviewer, writer");
  await page.getByRole("button", { name: "Post" }).click();
  const sent = page.locator(".message", { hasText: "please check the intro." });
  await expect(sent).toContainText("You");
  expect(posted("@reviewer and @writer, please check the intro.")?.to).toEqual(["@reviewer", "@writer"]);
  await expect(to).toHaveText("To everyone");
  // The post is one message on the board, and its body, mentions and all, shows once.
  const all = JSON.parse(aboard("read", "--as", "writer", "--board", board, "--json")).messages as Sent[];
  expect(all.filter((m) => m.body === "@reviewer and @writer, please check the intro.")).toHaveLength(1);
  await expect(sent).toHaveCount(1);
  await expect(sent.locator(".body")).toHaveCount(1);
  await expect(sent.locator(".body")).toHaveText("@reviewer and @writer, please check the intro.");
  expect((await sent.innerText()).split("please check the intro.").length - 1).toBe(1);

  // In the timeline, mentions show as names, in people's and agents' messages alike;
  // one shows its agent in the board panel.
  say("--as", "writer", "--to", "@scout", "@scout can you check role:reviewer's notes, ask @role:reviewer, or mail a@b.dev?");
  const fromAgent = page.locator(".message", { hasText: "can you check" });
  await expect(fromAgent.locator(".mention")).toHaveText(["@scout", "@role:reviewer"]);
  await expect(sent.locator(".mention")).toHaveText(["@reviewer", "@writer"]);
  await sent.locator(".mention", { hasText: "@writer" }).click();
  await expect(page.locator('[data-agent="writer"]')).toBeInViewport();

  // The timeline marks the mentions the server recorded when the message was posted:
  // never a name in code, and never someone who joined after it.
  say("--as", "writer", "Run `aboard say --to @scout` when @scout is free, and @late too.");
  aboard("join", invite.join_line, "--name", "late");
  await page.reload();
  const coded = page.locator(".message", { hasText: "when @scout is free" });
  await expect(coded.locator(".mention")).toHaveText(["@scout"]);

  // The "To" menu is the other way to pick: a name ticked there becomes a chip, and the
  // chip removes it.
  await to.click();
  await page.getByRole("menuitemcheckbox", { name: "scout" }).click();
  await page.keyboard.press("Escape");
  await expect(to).toHaveText("To scout");
  await page.getByRole("button", { name: "Remove scout from the recipients" }).click();
  await expect(to).toHaveText("To everyone");

  // A reply starts from the asker and the thread's people, as chips; a mention adds an
  // agent who isn't in the thread, a chip comes off, and the post sends that set.
  const ask = say("--as", "writer", "--to", "@reviewer", "--expect-reply", "Is the retry section clear?");
  say("--as", "reviewer", "--reply", ask.id, "--to", "@writer", "Mostly; the cap is missing.");
  const thread = page.locator(`[data-thread="${ask.id}"]`);
  await thread.locator(".thread-toggle").click();
  await thread.getByRole("button", { name: "Reply in thread" }).click();
  await expect(page.locator(".recipient-chip")).toHaveText(["writer", "reviewer"]);
  await expect(to).toHaveText("To writer, reviewer");
  await field.pressSequentially("Adding @sc");
  await field.press("Enter");
  await expect(to).toHaveText("To writer and 2 others");
  await page.getByRole("button", { name: "Remove reviewer from the recipients" }).click();
  await expect(to).toHaveText("To writer, scout");
  await field.pressSequentially("to look at the cap.");
  await field.press("Enter");
  await expect(thread.locator(".message", { hasText: "to look at the cap." })).toContainText("You");
  const reply = posted("Adding @scout to look at the cap.");
  expect(reply?.to).toEqual(["@writer", "@scout"]);
  expect(reply?.reply_to).toBe(ask.id);
  await expect(page.locator(".recipient-chip")).toHaveCount(0);
});

test("a browser without a session signs in with a pasted key it never keeps, and signs out", async ({ page }) => {
  aboard("up");
  const key: string = JSON.parse(aboard("keys", "create", "web-browser", "--json")).key.token;
  await page.goto(`${base()}/`);
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();
  const field = page.getByLabel("Access key");
  await expect(field).toHaveAttribute("type", "password");

  // A wrong key says so, and nothing is signed in.
  await field.fill("abh_not-a-real-key");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator(".problem")).toContainText("That access key doesn't work");
  await expect(field).toHaveValue("");
  expect(await page.context().cookies()).toEqual([]);

  await field.fill(key);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  // The page says plainly who it signed in as.
  await expect(page.locator(".signed-in-as")).toHaveText(/Signed in as @alex with the key web-browser\./);
  // The address bar never had the key, and the page keeps it nowhere: the session is in
  // a cookie its scripts can't read.
  expect(page.url()).toBe(`${base()}/`);
  const secret = key.slice(4);
  const kept = await page.evaluate(
    () => JSON.stringify({ ...localStorage }) + JSON.stringify({ ...sessionStorage }) + document.cookie + document.documentElement.outerHTML,
  );
  expect(kept).not.toContain(secret);
  const cookies = await page.context().cookies();
  expect(cookies).toHaveLength(1);
  expect(cookies[0].httpOnly).toBe(true);
  expect(cookies[0].value).not.toContain(secret);

  // The menu says which key the session came from, and signs this browser out.
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await expect(page.locator(".session-key")).toHaveText("web-browser");
  await page.getByRole("menuitem", { name: "Sign out of this browser" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();
  await expect(page.locator(".signed-out")).toContainText("You signed out of this browser");
  expect(await page.context().cookies()).toEqual([]);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();

  // Signing out ended that one session; the key keeps working.
  expect(JSON.parse(aboard("keys", "sessions", "web-browser", "--json")).sessions).toEqual([]);
  const keys = JSON.parse(aboard("keys", "--json")).keys as { name: string; state: string }[];
  expect(keys.find((k) => k.name === "web-browser")?.state).toBe("working");
  aboard("keys", "revoke", "web-browser");
});

test("a login an older page kept in storage moves into a cookie once", async ({ page }) => {
  const upgrade = await newBoard("Upgrade check");
  const open = JSON.parse(aboard("open", "--board", upgrade.name, "--json"));
  const code = new URLSearchParams(new URL(open.url).hash.slice(1)).get("code");
  // An older page traded the code for a token it kept in localStorage.
  const resp = await fetch(`${base()}/v1/browser-tokens`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  const { token } = (await resp.json()) as { token: string };
  expect(token).toMatch(/^abb_/);
  await page.goto(`${base()}/icon.svg`);
  await page.evaluate((t) => localStorage.setItem("aboard.browserToken", t), token);

  await page.goto(`${base()}/`);
  await expect(page.locator(".board-row", { hasText: "Upgrade check" })).toBeVisible();
  expect(await page.evaluate(() => localStorage.getItem("aboard.browserToken"))).toBeNull();
  const cookies = await page.context().cookies();
  expect(cookies.map((c) => [c.value === token, c.httpOnly])).toEqual([[true, true]]);
  await page.reload();
  await expect(page.locator(".board-row", { hasText: "Upgrade check" })).toBeVisible();
});

test("a hostile message is shown as text and runs nothing", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Hostile", "--json"));
  const board: string = pair.board.name;
  aboard("join", pair.join.line);
  const hostile = `<img src=x onerror="window.__xss=1"><script>window.__xss=2</script><a href="javascript:window.__xss=3">link</a>`;
  aboard("say", "--as", "writer", "--board", board, "--to", "@reviewer", hostile);

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  const resp = await openLink(page, open.url);
  // The page allows only its own scripts and the inline ones it was built with, by hash.
  const policy = resp?.headers()["content-security-policy"] ?? "";
  const scripts = policy.split(";").find((d) => d.trim().startsWith("script-src")) ?? "";
  expect(scripts).toMatch(/^\s*script-src 'self'( 'sha256-[A-Za-z0-9+/=]+')+$/);
  expect(resp?.headers()["referrer-policy"]).toBe("no-referrer");
  const message = page.locator(".message", { hasText: "window.__xss=2" });
  await expect(message.locator(".body")).toHaveText(hostile);
  await expect(message.locator("img, script, a[href^='javascript:']")).toHaveCount(0);
  expect(await page.evaluate(() => (window as unknown as { __xss?: number }).__xss)).toBeUndefined();
});

let samKey = "";

// linkFor makes an aboard open link for sam, a second person on the server, the way sam
// would and could then send it to someone else.
async function linkFor(): Promise<string> {
  if (samKey === "") {
    const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
    const admin = readFileSync(found, "utf8").trim();
    const invite = (await api(admin, "POST", "/v1/invites", {})) as { invite: string };
    const resp = await fetch(`${base()}/v1/connect`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ invite: invite.invite, handle: "sam", key_name: "sam-laptop" }),
    });
    samKey = ((await resp.json()) as { key: { token: string } }).key.token;
  }
  const { code } = (await api(samKey, "POST", "/v1/login-codes")) as { code: string };
  return `${base()}/#code=${encodeURIComponent(code)}`;
}

// signIns records every request that would sign the page in or look up a code.
function signIns(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    const path = new URL(r.url()).pathname;
    if (path === "/v1/browser-sessions" || path === "/v1/browser-tokens" || path === "/v1/login-codes/preview") {
      seen.push(`${r.method()} ${path}`);
    }
  });
  return seen;
}

test("another person's login link never signs in or switches the browser without a click", async ({ page }) => {
  aboard("up");
  const sent = signIns(page);

  // With no session, the link names sam and waits; Cancel leaves the browser signed out.
  await page.goto(await linkFor());
  await expect(page.getByRole("heading", { name: /^Sign in to .* as @sam\?$/ })).toBeVisible();
  expect(page.url()).toBe(`${base()}/`);
  expect(sent).toEqual(["POST /v1/login-codes/preview"]);
  expect(await page.context().cookies()).toEqual([]);
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();
  expect(await page.context().cookies()).toEqual([]);

  // Signed in as alex, sam's link says it would switch, names both, and changes nothing
  // until Continue.
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  sent.length = 0;
  await page.goto(await linkFor());
  await expect(page.getByRole("heading", { name: /^Sign in to .* as @sam\?$/ })).toBeVisible();
  await expect(page.locator(".switch-warning")).toContainText("You're signed in as @alex. This link would sign you in as @sam instead.");
  expect(sent).toEqual(["POST /v1/login-codes/preview"]);
  const me = async () => ((await (await page.request.get(`${base()}/v1/me`)).json()) as { name: string }).name;
  expect(await me()).toBe("alex");
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  expect(await me()).toBe("alex");

  // Continue is the only way to switch.
  await page.goto(await linkFor());
  await page.getByRole("button", { name: "Continue" }).click();
  await expect(page.getByRole("button", { name: /^You are sam/ })).toBeVisible();
  await expect(page.locator(".signed-in-as")).toContainText("Signed in as @sam with the key sam-laptop.");
  expect(await me()).toBe("sam");
});

test("aboard open for the person already signed in refreshes without asking", async ({ page }) => {
  aboard("up");
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  const sent = signIns(page);
  await page.goto(JSON.parse(aboard("open", "--json")).url);
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  await expect(page.getByRole("heading", { name: /^Sign in to / })).toHaveCount(0);
  expect(sent).toEqual(["POST /v1/login-codes/preview", "POST /v1/browser-sessions"]);
});

test("over plain HTTP away from this computer, the page sends no key, code or token", async ({ page }) => {
  aboard("up");
  // The test's server is on 127.0.0.1, which browsers count as secure; this makes the
  // page see what it would on http://team.example.com.
  await page.addInitScript(() => Object.defineProperty(window, "isSecureContext", { get: () => false }));
  const sent = signIns(page);
  await page.goto(`${base()}/icon.svg`);
  await page.evaluate(() => localStorage.setItem("aboard.browserToken", "abb_stored"));

  await page.goto(JSON.parse(aboard("open", "--json")).url);
  await expect(page.locator(".insecure")).toContainText("Signing in with a key needs https.");
  expect(page.url()).not.toContain("code");

  await page.goto(`${base()}/`);
  await expect(page.locator(".insecure")).toContainText("Signing in with a key needs https.");
  await expect(page.getByLabel("Access key")).toBeDisabled();
  await expect(page.getByRole("button", { name: "Sign in" })).toBeDisabled();
  // Even a submit forced past the disabled button sends nothing.
  await page.evaluate(() => document.querySelector("form")?.requestSubmit());
  expect(sent).toEqual([]);
  expect(await page.evaluate(() => localStorage.getItem("aboard.browserToken"))).toBeNull();
});

test("a used login link leads to the login page with a note, or leaves a signed-in browser as it was", async ({ page }) => {
  aboard("up");
  const url: string = JSON.parse(aboard("open", "--json")).url;
  await openLink(page, url);
  await page.goto(`${base()}/icon.svg`);
  await page.goto(url);
  await expect(page.locator(".signed-in-as")).toContainText("That login link doesn't work");
  await expect(page.locator(".signed-in-as")).toContainText("You're still signed in as @alex");

  await page.context().clearCookies();
  await page.goto(`${base()}/icon.svg`);
  await page.goto(url);
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toBeVisible();
  await expect(page.locator(".login-note")).toContainText("That login link doesn't work: it is wrong, expired or already used.");
  await expect(page.getByLabel("Access key")).toBeEnabled();
});

test("a sign-out the server didn't confirm says so and keeps the browser signed in", async ({ page }) => {
  aboard("up");
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  await page.route("**/v1/me/browser-session", (route) =>
    route.request().method() === "DELETE" ? route.abort() : route.continue(),
  );
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await page.getByRole("menuitem", { name: "Sign out of this browser" }).click();
  await expect(page.locator(".sign-out-problem")).toContainText("Couldn't sign out");
  await expect(page.locator(".sign-out-problem")).toContainText("This browser is still signed in.");
  await expect(page.getByRole("heading", { name: "Sign in to Aboard" })).toHaveCount(0);
  await page.unroute("**/v1/me/browser-session");
  await page.reload();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
});

// The security review's regression: a link to another person's login code, opened in a
// browser signed in with a pasted key, must leave that session in place. The review's
// version waited for the page to exchange the code; the page now asks first, so this
// waits for the question instead and declines it.
test("review: another persons fragment cannot silently replace an existing session", async ({ page }) => {
  aboard("up");
  const victimKey = JSON.parse(aboard("keys", "create", "victim-review", "--json")).key.token;
  const invite = JSON.parse(aboard("invite", "--server", "--json"));
  const connected = await fetch(`${base()}/v1/connect`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ invite: new URL(invite.link).hash.slice(1), handle: "attacker", key_name: "review-machine" }),
  });
  const person = (await connected.json()) as { key: { token: string } };
  const { code } = (await api(person.key.token, "POST", "/v1/login-codes")) as { code: string };
  await page.goto(`${base()}/`);
  await page.getByLabel("Access key").fill(victimKey);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  await page.goto(`${base()}/icon.svg`);
  const sent = signIns(page);
  await page.goto(`${base()}/#code=${encodeURIComponent(code)}`);
  await expect(page.locator(".switch-warning")).toContainText("You're signed in as @alex. This link would sign you in as @attacker instead.");
  expect(sent).toEqual(["POST /v1/login-codes/preview"]);
  await page.getByRole("button", { name: "Cancel" }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible({ timeout: 3000 });
  aboard("keys", "revoke", "victim-review");
});

// api calls the local server with a token, as a client of the public API.
async function api(token: string, method: string, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const resp = await fetch(`http://${env.ABOARD_LOCAL_ADDR}${path}`, {
    method,
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const out = (await resp.json()) as Record<string, unknown>;
  if (!resp.ok) throw new Error(`${method} ${path}: ${resp.status} ${JSON.stringify(out)}`);
  return out;
}

test("the header, the board list and the people on a board show open, private and owners", async ({ page }) => {
  const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  const owner = readFileSync(found, "utf8").trim();
  // A second person, maya, comes onto the server, and alex makes a private board with her on it.
  const invite = await api(owner, "POST", "/v1/invites", {});
  await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "maya", key_name: "laptop" });
  const created = await api(owner, "POST", "/v1/boards", { template: "general", name: "secret-plans", title: "Secret plans", visibility: "private" });
  expect(created.visibility).toBe("private");
  await api(owner, "POST", "/v1/boards/secret-plans/people", { handle: "maya" });

  const open = JSON.parse(aboard("open", "--board", "secret-plans", "--json"));
  await openLink(page, open.url);
  const banner = page.getByRole("banner");
  await expect(banner.locator('[data-visibility="private"]')).toHaveText("Private");
  const people = page.locator('section[aria-labelledby="people"]');
  await expect(people.locator('[data-person="alex"] .board-role')).toHaveText("Owner");
  await expect(people.locator('[data-person="maya"] .board-role')).toHaveText("Member");

  // lee, from outside the server, comes on through a guest code and shows as a guest.
  const code = await api(owner, "POST", "/v1/boards/secret-plans/join-codes", { role: "member", guest: "lee" });
  await api("", "POST", "/v1/guest-join", { code: code.code, key_name: "lees-laptop", harness: "codex" });
  await page.reload();
  await expect(people.locator('[data-person="lee"] .board-role')).toHaveText("Guest");

  for (const theme of ["Dark", "Light"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme }).click();
    await page.keyboard.press("Escape");
    await expect(banner.locator('[data-visibility="private"]')).toBeVisible();
  }

  // Turned open, the header says so, since others are on the board.
  await api(owner, "POST", "/v1/boards/secret-plans/visibility", { visibility: "open" });
  await page.reload();
  await expect(banner.locator('[data-visibility="open"]')).toHaveText("Open");
  await api(owner, "POST", "/v1/boards/secret-plans/visibility", { visibility: "private" });

  // The board list marks the private board, and says nothing on a board only alex is on.
  const solo = await newBoard("Only alex here");
  await page.goto(`http://${env.ABOARD_LOCAL_ADDR}/`);
  const row = page.locator(".board-row", { hasText: "Secret plans" });
  await expect(row.locator('[data-visibility="private"]')).toHaveText("Private");
  const alone = page.locator(".board-row", { has: page.locator(`a[href="/?board=${solo.name}"]`) });
  await expect(alone).toBeVisible();
  await expect(alone.locator(".visibility")).toHaveCount(0);
});

// unreadOn reads the person's unread count on a board from the CLI, as another machine of
// theirs would see it.
for (const theme of ["light", "dark"] as const) {
  test(`sidebar separates questions from unread and sorts recent conversations (${theme})`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: theme });
    const older = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Sidebar earlier", "--json"));
    const newer = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Sidebar recent", "--json"));
    const a: string = older.board.name;
    const b: string = newer.board.name;
    // Empty boards can share a creation timestamp; establish conversation activity.
    aboard("say", "--as", "writer", "--board", b, "Start the recent conversation.");
    const earlier = await api(ownerKey(), "GET", `/v1/boards/${a}`);
    const recent = await api(ownerKey(), "GET", `/v1/boards/${b}`);
    expect(Date.parse(String(recent.last_message_at))).toBeGreaterThan(Date.parse(String(earlier.created_at)));
    const open = JSON.parse(aboard("open", "--board", b, "--json"));
    await openLink(page, open.url);
    const nav = page.getByRole("navigation", { name: "Boards" });
    const relevant = nav.locator(`a[href="/?board=${a}"], a[href="/?board=${b}"]`);
    await expect(relevant.first()).toHaveAttribute("href", `/?board=${b}`);
    const question = JSON.parse(aboard("say", "--as", "writer", "--board", a, "--to", "@alex", "--expect-reply", "Which wording should we use?", "--json"));
    const row = nav.locator(`a[href="/?board=${a}"]`);
    await expect(nav.getByRole("region", { name: "Needs you" }).locator(`a[href="/?board=${a}"]`)).toBeVisible();
    await expect(row.locator(".needs-reply-count [aria-hidden]")).toHaveText("1");
    await expect(row.locator(".unread-count [aria-hidden]")).toHaveText("1");
    if (process.env.ABOARD_SIDEBAR_REVIEW) {
      const dir = process.env.ABOARD_SIDEBAR_REVIEW;
      mkdirSync(dir, { recursive: true });
      for (const [viewport, width, height] of [["desktop", 1280, 800], ["mobile", 390, 844]] as const) {
        await page.setViewportSize({ width, height });
        await page.emulateMedia({ reducedMotion: "reduce" });
        await expect(row).toBeVisible();
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
        await page.screenshot({ path: join(dir, `${viewport}-${theme}.png`), fullPage: true, animations: "disabled" });
      }
      await page.setViewportSize({ width: 1280, height: 800 });
    }
    aboard("read", "--mark-read", "--board", a);
    await expect(row.locator(".unread-count")).toHaveCount(0);
    await expect(row.locator(".needs-reply-count [aria-hidden]")).toHaveText("1");
    const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
    const owner = readFileSync(found, "utf8").trim();
    await api(owner, "POST", `/v1/boards/${a}/messages`, { body: "Use the shorter wording.", reply_to: question.message.id, to: ["all"] });
    await expect(row.locator(".needs-reply-count")).toHaveCount(0);
    await expect(nav.getByRole("region", { name: "Needs you" }).locator(`a[href="/?board=${a}"]`)).toHaveCount(0);
    await expect(relevant.first()).toHaveAttribute("href", `/?board=${a}`);
    aboard("say", "--as", "writer", "--board", b, "A newer conversation.");
    await expect(relevant.first()).toHaveAttribute("href", `/?board=${b}`);
    await expect(nav.locator(`a[href="/?board=${b}"]`)).toHaveAttribute("aria-current", "page");
  });
}

function unreadOn(board: string): number {
  const out = JSON.parse(aboard("boards", "--json")) as { boards: { name: string; unread: number | null }[] };
  return out.boards.find((b) => b.name === board)?.unread ?? -1;
}

test("the desktop room scrolls its timeline and panels without moving the page", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 720 });
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Scroll room", "--json"));
  aboard("join", pair.join.line);
  for (let i = 1; i <= 30; i++) aboard("say", "--as", "writer", "--board", pair.board.name, `Scroll message ${i}.`);
  const open = JSON.parse(aboard("open", "--board", pair.board.name, "--json"));
  await openLink(page, open.url);
  await expect(page.getByText("Scroll message 30.", { exact: true })).toBeVisible();
  const timeline = page.getByRole("log", { name: "Timeline" });
  expect(await timeline.evaluate((el) => el.scrollHeight > el.clientHeight)).toBe(true);
  await page.getByRole("button", { name: /Scroll room.*board details/ }).click();
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  expect(await page.evaluate(() => document.documentElement.scrollHeight)).toBeLessThanOrEqual(720);
  const composer = await page.getByRole("button", { name: "Post", exact: true }).boundingBox();
  expect(composer!.y + composer!.height).toBeLessThanOrEqual(720);
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await timeline.evaluate((el) => { el.scrollTop = 0; });
    await expect(page.getByText("Scroll message 1.", { exact: true })).toBeVisible();
    await timeline.evaluate((el) => { el.scrollTop = el.scrollHeight; });
    await expect(page.getByText("Scroll message 30.", { exact: true })).toBeVisible();
    await page.screenshot({ path: join(tmpdir(), `aboard-scroll-desktop-${theme}.png`) });
    await page.setViewportSize({ width: 390, height: 844 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
    await page.getByRole("complementary", { name: "Scroll room" }).scrollIntoViewIfNeeded();
    await expect(page.getByRole("complementary", { name: "Scroll room" })).toBeVisible();
    await page.screenshot({ path: join(tmpdir(), `aboard-scroll-mobile-${theme}.png`) });
    await page.setViewportSize({ width: 1440, height: 720 });
    await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0);
  }
  await page.getByRole("button", { name: "Dismiss", exact: true }).click();
  await expect(page.locator(".signed-in-as")).toHaveCount(0);
  await page.setViewportSize({ width: 1100, height: 500 });
  await page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  expect(await page.evaluate(() => window.scrollY)).toBe(0);
  const shortComposer = await page.getByRole("button", { name: "Post", exact: true }).boundingBox();
  expect(shortComposer!.y + shortComposer!.height).toBeLessThanOrEqual(500);
});

test("the person's read position moves only with what they saw, and receipts say who has a message", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Attention", "--json"));
  const board: string = pair.board.name;
  aboard("join", pair.join.line);
  for (let i = 1; i <= 30; i++) aboard("say", "--as", "writer", "--board", board, `Step ${i} of the plan, written out so the timeline scrolls.`);
  expect(unreadOn(board)).toBe(30);

  // Opening at the newest message does not read the older rows above the viewport.
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  await expect(page.getByText("Step 30 of the plan")).toBeVisible();
  expect(unreadOn(board)).toBeGreaterThan(0);
  const rows = page.locator(".message");
  for (let i = (await rows.count()) - 1; i >= 0; i--) await rows.nth(i).scrollIntoViewIfNeeded();
  await expect.poll(() => unreadOn(board)).toBe(0);
  await page.getByText("Step 30 of the plan").scrollIntoViewIfNeeded();

  // Reading further back shows nothing new, so what arrives meanwhile stays unread until
  // the person comes back down to it.
  await page.locator(".timeline").evaluate((el) => {
    el.scrollTop = 0;
    el.dispatchEvent(new Event("scroll"));
  });
  await expect(page.getByRole("button", { name: /Jump to newest/ })).toBeVisible();
  aboard("say", "--as", "writer", "--board", board, "Something new while you read back.");
  await expect(page.getByRole("button", { name: /Jump to newest · 1 new/ })).toBeVisible();
  expect(unreadOn(board)).toBe(1);
  await expect(page.getByRole("navigation", { name: "Boards" }).getByRole("link", { name: /Attention/ }).locator(".unread-count [aria-hidden]")).toHaveText("1");
  await page.getByRole("button", { name: /Jump to newest/ }).click();
  await expect.poll(() => unreadOn(board)).toBe(0);

  // Another board with something unread shows how many in the board list, and marking it
  // read from the CLI clears it here too. The test makes that board itself, with pat to
  // write on it, so it never depends on what an earlier test left.
  const second = await newBoard("Second look");
  const pat = await person("pat");
  await api(ownerKey(), "POST", `/v1/boards/${second.name}/people`, { handle: "pat" });
  const nav = page.getByRole("navigation", { name: "Boards" });
  const docs = nav.locator(`a[href="/?board=${second.name}"]`);
  await expect(docs).toBeVisible();
  await expect(docs.locator(".unread-count")).toHaveCount(0);
  await api(pat, "POST", `/v1/boards/${second.name}/messages`, { body: "A note on the other board.", to: ["all"] });
  await expect(docs.locator(".unread-count [aria-hidden]")).toHaveText("1");
  aboard("read", "--mark-read", "--board", second.name);
  await expect(docs.locator(".unread-count")).toHaveCount(0);
  await expect(docs.locator(".message-count")).toHaveCount(0);

  // A message from the person's agent to the reviewer is pending until the reviewer's
  // inbox takes it, then received; the mark lists who, on hover.
  aboard("say", "--as", "writer", "--board", board, "--to", "@reviewer", "Please check the intro.");
  const toReviewer = page.locator(".message", { hasText: "Please check the intro." }).locator(".receipt-mark");
  await expect(toReviewer).toHaveText("Pending");
  aboard("inbox", "--as", "reviewer", "--board", board);
  await expect(toReviewer).toHaveText("Received");
  await toReviewer.hover();
  await expect(page.locator(".receipt-list")).toHaveText("reviewer: received");
  await page.mouse.move(0, 0);

  // A message to the person is read once the page has shown it; one to everyone has no mark.
  aboard("say", "--as", "writer", "--board", board, "--to", "@alex", "Can you decide on the title?");
  await expect(page.locator(".message", { hasText: "Can you decide on the title?" }).locator(".receipt-mark")).toHaveText("Read");
  await expect(page.locator(".message", { hasText: "Step 30 of the plan" }).locator(".receipt-mark")).toHaveCount(0);

  // The other board opens with "New since you last looked" where the server's position
  // says, which the CLI moved above, and showing it marks it read.
  await api(pat, "POST", `/v1/boards/${second.name}/messages`, { body: "Back on the first board.", to: ["all"] });
  await expect(docs.locator(".unread-count [aria-hidden]")).toHaveText("1");
  await docs.click();
  await expect(page.locator(".new-divider + .message")).toContainText("Back on the first board.");
  await expect.poll(() => unreadOn(second.name)).toBe(0);
});

test("the newest page does not acknowledge older unloaded messages", async ({ page }) => {
 const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--json"));
 const board = pair.board.name;
 for (let i=1; i<=51; i++) aboard("say", "--as", "writer", "--board", board, `review unseen item ${i}`);
 expect(unreadOn(board)).toBe(51);
 const open = JSON.parse(aboard("open", "--board", board, "--json"));
 await openLink(page, open.url);
 await expect(page.getByText("review unseen item 51", {exact:true})).toBeVisible();
 await expect(page.getByText("review unseen item 1", {exact:true})).toHaveCount(0);
 await expect.poll(() => unreadOn(board)).toBeGreaterThan(0);
});


test("a collapsed reply is not read by showing its thread summary", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--json"));
  const board = pair.board.name;
  const root = JSON.parse(aboard("say", "--as", "writer", "--board", board, "Read this root", "--json")).message;
  // A reaction consumes a sequence without becoming an unread message.
  aboard("react", "--as", "writer", "--board", board, String(root.seq), "👍");
  aboard("say", "--as", "writer", "--board", board, "--reply", root.id, "--to", "all", "This reply is still hidden");
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  await expect(page.getByText("Read this root", { exact: true })).toBeVisible();
  await expect(page.getByText("This reply is still hidden", { exact: true })).toHaveCount(0);
  await expect.poll(() => unreadOn(board)).toBe(1);
  await page.getByRole("button", { name: /^Show 1 reply/ }).click();
  await expect(page.getByText("This reply is still hidden", { exact: true })).toBeVisible();
  await expect.poll(() => unreadOn(board)).toBe(0);
});

test("each agent shows its delivery mode, and its person changes it from a menu with each mode's rule", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Delivery check", "--json"));
  const board: string = pair.board.name;
  const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  const owner = readFileSync(found, "utf8").trim();
  // kim, another person on the server, has an agent on alex's board too.
  const invite = await api(owner, "POST", "/v1/invites", {});
  const kim = await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "kim", key_name: "laptop" });
  const kimKey = (kim.key as { token: string }).token;
  await api(kimKey, "POST", `/v1/boards/${board}/people`, { handle: "kim" });
  const kimAgent = ((await api(kimKey, "POST", "/v1/join", { board, role: "reviewer" })).agent as { name: string }).name;

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const panel = page.getByRole("complementary", { name: "Delivery check" });
  const writer = panel.locator('[data-agent="writer"]');

  // alex's own agent's mode is a menu; kim's agent shows its mode as a label only.
  const theirs = panel.locator(`[data-agent="${kimAgent}"]`);
  await expect(theirs.locator(".delivery-mode")).toHaveText("focused");
  await expect(theirs.locator(".delivery-mode")).toHaveAttribute("title", modeRules.focused);
  await expect(theirs.getByRole("button", { name: /^Delivery mode of/ })).toHaveCount(0);

  for (const theme of ["Dark", "Light"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme }).click();
    await page.keyboard.press("Escape");
    await writer.getByRole("button", { name: "Delivery mode of writer: focused. Change it" }).click();
    // Each mode comes with the rule the agent is told, in the same words as the CLI's.
    const menu = page.locator(".delivery-modes");
    for (const mode of settableModes) {
      await expect(menu.getByRole("menuitemradio", { name: new RegExp(`^${mode}`) })).toContainText(modeRules[mode]);
    }
    await expect(menu.getByRole("menuitemradio", { name: /^focused/ })).toHaveAttribute("aria-checked", "true");
    await page.keyboard.press("Escape");
  }

  await writer.getByRole("button", { name: /^Delivery mode of writer/ }).click();
  await page.getByRole("menuitemradio", { name: /^off/ }).click();
  await expect(writer.getByRole("button", { name: "Delivery mode of writer: off. Change it" })).toBeVisible();

  // The server holds it: the CLI reads the same mode, and the record says who changed it.
  expect(JSON.parse(aboard("delivery", "--as", "writer", "--board", board, "--json")).mode).toBe("off");
  await expect(page.locator(".board-event", { hasText: "alex set writer's delivery mode to off" })).toBeVisible();

  // A change made elsewhere shows live.
  aboard("delivery", "humans", "--as", "writer", "--board", board);
  await expect(writer.getByRole("button", { name: "Delivery mode of writer: humans. Change it" })).toBeVisible();

  // kim's agent is kim's to set: alex is refused, through the API too.
  const refused = await fetch(`http://${env.ABOARD_LOCAL_ADDR}/v1/boards/${board}/members/${kimAgent}/delivery`, {
    method: "PUT",
    headers: { Authorization: `Bearer ${owner}`, "Content-Type": "application/json" },
    body: JSON.stringify({ mode: "off" }),
  });
  expect(refused.status).toBe(403);
  expect(((await refused.json()) as { error: { code: string } }).error.code).toBe("agent_owner_required");
});

// ownerKey is alex's key on the local server, started if it isn't running.
function ownerKey(): string {
  aboard("up");
  const found = execFileSync("find", [env.HOME ?? home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  return readFileSync(found, "utf8").trim();
}

// newBoard makes a board as alex through the public API, with no agent seats. The
// server limits joins per client, and the whole spec shares one client, so tests that
// need no agent make their boards this way rather than with aboard pair.
async function newBoard(title: string): Promise<{ name: string; id: string }> {
  const b = await api(ownerKey(), "POST", "/v1/boards", { template: "general", title });
  return { name: b.name as string, id: b.id as string };
}

// person brings another person onto the server once per server, and returns their key.
// Tests that need someone other than alex to write use them instead of more agent seats.
const people = new Map<string, string>();
async function person(handle: string): Promise<string> {
  const known = people.get(`${base()} ${handle}`);
  if (known) return known;
  const invite = await api(ownerKey(), "POST", "/v1/invites", {});
  const made = await api("", "POST", "/v1/connect", { invite: invite.invite, handle, key_name: "laptop" });
  const key = (made.key as { token: string }).token;
  people.set(`${base()} ${handle}`, key);
  return key;
}

function archivedNames(): string[] {
  return (JSON.parse(aboard("boards", "--archived", "--json")) as { boards: { name: string }[] }).boards.map((b) => b.name);
}

test("an archived board is read-only, groups under Archived, restores and deletes behind its typed name", async ({ page, browser }) => {
  const { name: board } = await newBoard("Lifecycle check");
  const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  const owner = readFileSync(found, "utf8").trim();
  // rae, another person on the server and not an admin, is on alex's board.
  const invite = await api(owner, "POST", "/v1/invites", {});
  const rae = await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "rae", key_name: "laptop" });
  const raeKey = (rae.key as { token: string }).token;
  await api(owner, "POST", `/v1/boards/${board}/people`, { handle: "rae" });

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const panel = page.getByRole("complementary", { name: "Lifecycle check" });
  const composer = page.getByRole("form", { name: "Post a message" });
  await expect(composer).toBeVisible();
  await expect(panel.getByRole("button", { name: "Delete board" })).toHaveCount(0);

  // alex made the board, so Details offers Archive; archiving swaps the message box for
  // a calm notice, and the record says who archived it.
  await panel.getByRole("button", { name: "Archive board" }).click();
  const notice = page.getByRole("region", { name: "Archived board", exact: true });
  await expect(notice).toContainText("This board is archived. It's read-only.");
  await expect(composer).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Archive board" })).toHaveCount(0);
  await expect(panel.getByRole("button", { name: "Add an agent" })).toHaveCount(0);
  await expect(page.locator(".board-event", { hasText: "alex archived the board" })).toBeVisible();
  expect(archivedNames()).toContain(board);

  // The board list keeps it in an Archived group at the bottom, open here since it is
  // the board on screen.
  const nav = page.getByRole("navigation", { name: "Boards" });
  await expect(nav.getByRole("region", { name: "Archived boards" }).locator(`a[href="/?board=${board}"]`)).toBeVisible();
  for (const theme of ["Dark", "Light"]) {
    await page.getByRole("button", { name: /^You are alex/ }).click();
    await page.getByRole("menuitemradio", { name: theme }).click();
    await page.keyboard.press("Escape");
    await expect(notice).toBeVisible();
  }

  // rae is on the board but neither made it nor runs the server: her board list keeps
  // the group closed, and she reads the notice with no Restore, Archive or Delete.
  const raeContext = await browser.newContext();
  const raePage = await raeContext.newPage();
  await raePage.goto(`${base()}/`);
  await raePage.getByLabel("Access key").fill(raeKey);
  await raePage.getByRole("button", { name: "Sign in" }).click();
  await expect(raePage.getByRole("button", { name: /^You are rae/ })).toBeVisible();
  const raeGroup = raePage.getByRole("region", { name: "Archived boards" });
  await expect(raeGroup.locator(`a[href="/?board=${board}"]`)).toBeHidden();
  await raeGroup.getByRole("button", { name: /Archived/ }).click();
  await raeGroup.locator(`a[href="/?board=${board}"]`).click();
  const raeNotice = raePage.getByRole("region", { name: "Archived board", exact: true });
  await expect(raeNotice).toContainText("This board is archived. It's read-only.");
  await expect(raeNotice.getByRole("button", { name: "Restore" })).toHaveCount(0);
  const raePanel = raePage.getByRole("complementary", { name: "Lifecycle check" });
  await expect(raePanel.locator(".board-facts")).toBeVisible();
  await expect(raePanel.getByRole("button", { name: /Archive board|Delete board/ })).toHaveCount(0);
  await raeContext.close();

  // Restore brings the message box back.
  await notice.getByRole("button", { name: "Restore" }).click();
  await expect(composer).toBeVisible();
  await expect(notice).toHaveCount(0);
  await expect(page.locator(".board-event", { hasText: "alex restored the board" })).toBeVisible();

  // Delete shows only on an archived board, and needs the board's name typed exactly.
  await panel.getByRole("button", { name: "Archive board" }).click();
  await expect(notice).toBeVisible();
  const opener = panel.getByRole("button", { name: "Delete board" });
  await opener.click();
  const dialog = page.getByRole("alertdialog", { name: `Delete ${board}?` });
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText("Nobody can open it again. Its record is kept.");
  const confirm = dialog.getByRole("button", { name: "Delete", exact: true });
  await expect(confirm).toBeDisabled();
  await dialog.getByLabel(`Type ${board} to confirm`).fill(board.slice(0, -1));
  await expect(confirm).toBeDisabled();
  await page.keyboard.press("Escape");
  await expect(dialog).toHaveCount(0);
  await expect(opener).toBeFocused();
  expect(archivedNames()).toContain(board);

  // Cancel closes it too; the field starts empty when it opens again.
  await opener.click();
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(dialog).toHaveCount(0);
  await opener.click();
  await expect(dialog.getByLabel(`Type ${board} to confirm`)).toHaveValue("");
  for (const theme of ["dark", "light"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await expect(dialog).toBeVisible();
  }
  await dialog.getByLabel(`Type ${board} to confirm`).fill(board);
  await confirm.click();
  await expect(page).toHaveURL(`${base()}/`);
  await expect(page.locator(`a[href="/?board=${board}"]`)).toHaveCount(0);
  expect(archivedNames()).not.toContain(board);
});

test("archiving the board on screen opens a collapsed Archived group, and a later collapse stays", async ({ page }) => {
  const { name: done } = await newBoard("Already done");
  const { name: board } = await newBoard("Still going");
  aboard("board", "archive", done);
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const nav = page.getByRole("navigation", { name: "Boards" });
  const group = nav.getByRole("region", { name: "Archived boards" });
  const toggle = group.getByRole("button", { name: /Archived/ });
  await expect(toggle).toHaveAttribute("aria-expanded", "false");

  // The board on screen moves into the collapsed group, which opens so its link stays.
  await page.getByRole("complementary", { name: "Still going" }).getByRole("button", { name: "Archive board" }).click();
  await expect(group.locator(`a[href="/?board=${board}"]`)).toBeVisible();

  // Closed by hand, it stays closed as the list is read again.
  await toggle.click();
  await expect(group.locator(`a[href="/?board=${board}"]`)).toBeHidden();
  aboard("board", "restore", done); // the list is read again with the head it moves
  await expect(nav.locator(`a[href="/?board=${done}"]`)).toBeVisible();
  await expect(group.locator(`a[href="/?board=${board}"]`)).toBeHidden();
  aboard("board", "restore", board);
});

test("an archived board offers no replies or reactions, but shows its reactions and threads", async ({ page }) => {
  const { name: board } = await newBoard("Read only");
  const owner = ownerKey();
  const root = await api(owner, "POST", `/v1/boards/${board}/messages`, { body: "Finished the draft", to: ["all"] });
  await api(owner, "PUT", `/v1/messages/${root.id as string}/reactions/thumbsup`);
  await api(owner, "POST", `/v1/boards/${board}/messages`, { body: "Looks right to me", to: ["all"], reply_to: root.id });
  aboard("board", "archive", board);
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const timeline = page.locator(".message", { hasText: "Finished the draft" });
  await expect(timeline).toBeVisible();
  await timeline.hover();
  await expect(page.getByRole("button", { name: /^Reply to / })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^React to / })).toHaveCount(0);
  const reaction = timeline.locator('[data-reaction="thumbsup"]');
  await expect(reaction).toContainText("1");
  await expect(timeline.getByRole("button", { name: /Add yours|Take yours back/ })).toHaveCount(0);
  await page.getByRole("button", { name: /^Show 1 reply/ }).click();
  await expect(page.getByText("Looks right to me", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reply in thread" })).toHaveCount(0);
});

test("a board another person deletes while it is open says it is no longer available", async ({ page }) => {
  aboard("up");
  const found = execFileSync("find", [home, "-name", "local-owner-token"], { encoding: "utf8" }).trim().split("\n")[0];
  const owner = readFileSync(found, "utf8").trim();
  // sol makes a board and puts alex on it; sol, its creator, deletes it later.
  const invite = await api(owner, "POST", "/v1/invites", {});
  const sol = await api("", "POST", "/v1/connect", { invite: invite.invite, handle: "sol", key_name: "laptop" });
  const solKey = (sol.key as { token: string }).token;
  const made = await api(solKey, "POST", "/v1/boards", { template: "general", title: "Sol's board" });
  const board = made.name as string;
  await api(solKey, "POST", `/v1/boards/${board}/people`, { handle: "alex" });
  const { name: other } = await newBoard("Untouched");

  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const nav = page.getByRole("navigation", { name: "Boards" });
  await expect(nav.locator(`a[href="/?board=${board}"]`)).toBeVisible();
  await api(solKey, "POST", `/v1/boards/${board}/archive`, {});
  await expect(page.getByRole("region", { name: "Archived board", exact: true })).toBeVisible();
  await api(solKey, "POST", `/v1/boards/${board}/delete`, {});

  const gone = page.getByRole("region", { name: "Board unavailable" });
  await expect(gone).toContainText("This board is no longer available.");
  await expect(page.getByRole("form", { name: "Post a message" })).toHaveCount(0);
  await expect(page.getByText("Sol's board")).toHaveCount(0);
  await gone.getByRole("link", { name: "your boards" }).click();
  await expect(page.locator(`a[href="/?board=${board}"]`)).toHaveCount(0);
  await expect(page.locator(`a[href="/?board=${other}"]`)).toBeVisible();
});

// holdStream keeps the page's event stream connections waiting, so a test decides what
// arrives: hint(id) answers the waiting connection with one board_unavailable for id,
// and the page connects again, to wait for the next. Nothing else reaches the stream.
async function holdStream(page: Page): Promise<{ hint: (id: string) => Promise<void> }> {
  const waiting: Route[] = [];
  await page.route("**/v1/stream", (route) => {
    waiting.push(route);
  });
  return {
    hint: async (id: string) => {
      await expect.poll(() => waiting.length).toBeGreaterThan(0);
      await waiting.shift()!.fulfill({
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
        body: `event: board_unavailable\ndata: ${JSON.stringify({ board_id: id })}\n\n`,
      });
    },
  };
}

test("a board_unavailable hint for a board never seen, or for another board still open, changes nothing", async ({ page }) => {
  const { name: board } = await newBoard("Hint check");
  const other = await newBoard("Hint neighbour");
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  const stream = await holdStream(page);
  await openLink(page, open.url);
  const composer = page.getByRole("form", { name: "Post a message" });
  const nav = page.getByRole("navigation", { name: "Boards" });
  await expect(composer).toBeVisible();
  await expect(nav.locator(`a[href="/?board=${other.name}"]`)).toBeVisible();

  // The list is read again only for a board the page has seen, and then shows it still.
  let listed = 0;
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/v1/boards") listed++;
  });
  for (const [id, seen] of [
    ["brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7Z", false],
    [other.id, true],
  ] as const) {
    const before = listed;
    await stream.hint(id);
    if (seen) await expect.poll(() => listed).toBeGreaterThan(before);
    await expect(nav.locator(`a[href="/?board=${other.name}"]`)).toBeVisible();
    await expect(page.getByRole("heading", { level: 1 })).toContainText("Hint check");
    await expect(composer).toBeVisible();
    await expect(page.getByRole("region", { name: "Board unavailable" })).toHaveCount(0);
    if (!seen) expect(listed).toBe(before);
  }
});

test("a board_unavailable hint whose fresh read fails for a while keeps the board shown", async ({ page }) => {
  const flaky = await newBoard("Flaky read");
  const board = flaky.name;
  await api(ownerKey(), "POST", `/v1/boards/${board}/messages`, { body: "Finished the plan", to: ["all"] });
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  const stream = await holdStream(page);
  await openLink(page, open.url);
  const composer = page.getByRole("form", { name: "Post a message" });
  await expect(composer).toBeVisible();
  // The board has loaded (the message box shows before it does) before any read fails.
  await expect(page.getByText("Finished the plan", { exact: true })).toBeVisible();
  await expect(page.getByRole("heading", { level: 1 })).toContainText("Flaky read");

  for (const fail of ["5xx", "network"] as const) {
    let failed = 0;
    await page.route(`**/v1/boards/${board}`, (route) => {
      failed++;
      return fail === "5xx"
        ? route.fulfill({ status: 503, contentType: "application/json", body: JSON.stringify({ error: { code: "internal", message: "Try again.", hint: "" } }) })
        : route.abort("connectionreset");
    });
    await stream.hint(flaky.id);
    await expect.poll(() => failed).toBeGreaterThan(0);
    await page.unroute(`**/v1/boards/${board}`);
    await expect(page.getByRole("region", { name: "Board unavailable" })).toHaveCount(0);
    await expect(page.getByRole("heading", { level: 1 })).toContainText("Flaky read");
    await expect(composer).toBeVisible();
    await expect(page.getByText("Finished the plan", { exact: true })).toBeVisible();
  }
});

// holdLists answers every board list the page asks for from the server at the moment it
// asks, then holds that answer until release(i), so a test can deliver an old list after
// newer news. edit may change the held answer, to mark it.
type ListBody = { boards: { id: string; title: string | null }[] };
async function holdLists(page: Page, edit: (body: ListBody) => void = () => {}) {
  const held: { route: Route; body: string; status: number }[] = [];
  await page.route(
    (url) => url.pathname === "/v1/boards",
    async (route) => {
      const resp = await route.fetch();
      const body = (await resp.json()) as ListBody;
      edit(body);
      held.push({ route, body: JSON.stringify(body), status: resp.status() });
    },
  );
  return {
    count: () => held.length,
    release: (i: number) => held[i].route.fulfill({ status: held[i].status, contentType: "application/json", body: held[i].body }),
  };
}

// retitle marks a held list's copy of one board, so a test sees when that list is shown.
function retitle(id: string, title: string) {
  return (body: ListBody) => {
    for (const b of body.boards) if (b.id === id) b.title = title;
  };
}

// otherWithUnread makes a board pat writes one message on, unread for alex.
async function otherWithUnread(title: string): Promise<{ name: string; id: string; seq: number }> {
  const pat = await person("pat");
  const b = await newBoard(title);
  await api(ownerKey(), "POST", `/v1/boards/${b.name}/people`, { handle: "pat" });
  const m = await api(pat, "POST", `/v1/boards/${b.name}/messages`, { body: `A note on ${title}.`, to: ["all"] });
  return { ...b, seq: m.seq as number };
}

// archivedInList is how many archived boards the board list's Archived group counts; 0
// when it has none.
async function archivedInList(page: Page): Promise<number> {
  const group = page.getByRole("region", { name: "Archived boards" });
  if ((await group.count()) === 0) return 0;
  return Number((await group.getByRole("button", { name: /Archived/ }).innerText()).replace(/\D/g, ""));
}

// frames waits for the page to render twice, so work its scripts queued has shown.
async function frames(page: Page) {
  await page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));
}

test("a board list read before an acknowledgement never brings back the unread count (board view)", async ({ page }) => {
  const here = await newBoard("Stale list here");
  const other = await otherWithUnread("Stale list other");
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const link = page.getByRole("navigation", { name: "Boards" }).locator(`a[href="/?board=${other.name}"]`);
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText("1");

  const lists = await holdLists(page, retitle(other.id, "Stale snapshot"));
  // A message here makes the page read the list again; the server answers while the
  // other board still has one unread, and the answer is held.
  await api(ownerKey(), "POST", `/v1/boards/${here.name}/messages`, { body: "Moving on.", to: ["all"] });
  await expect.poll(lists.count).toBe(1);
  await api(ownerKey(), "POST", `/v1/boards/${other.name}/ack`, { up_to: other.seq });
  await expect(link.locator(".unread-count")).toHaveCount(0);
  await lists.release(0);
  await expect(link).toContainText("Stale snapshot");
  await expect(link.locator(".unread-count")).toHaveCount(0);
});

test("a board list read before an acknowledgement never brings back the unread count (board list)", async ({ page }) => {
  const here = await newBoard("Home stale here");
  const other = await otherWithUnread("Home stale other");
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  await page.goto(`${base()}/`);
  const row = page.locator(".board-row", { has: page.locator(`a[href="/?board=${other.name}"]`) });
  await expect(row.locator(".unread")).toHaveText("1 unread");

  const lists = await holdLists(page, retitle(other.id, "Home snapshot"));
  await api(ownerKey(), "POST", `/v1/boards/${here.name}/messages`, { body: "Moving on.", to: ["all"] });
  await expect.poll(lists.count).toBe(1);
  await api(ownerKey(), "POST", `/v1/boards/${other.name}/ack`, { up_to: other.seq });
  await expect(row.locator(".unread")).toHaveCount(0);
  await lists.release(0);
  await expect(row).toContainText("Home snapshot");
  await expect(row.locator(".unread")).toHaveCount(0);
});

test("an acknowledgement answered after a newer message never hides that message's unread", async ({ page }) => {
  const pat = await person("pat");
  const here = await otherWithUnread("Held ack");
  // The page acknowledges the message it shows; the server takes it, and its answer is held.
  const acks: { route: Route; status: number; body: string }[] = [];
  await page.route(
    (url) => url.pathname === `/v1/boards/${here.name}/ack`,
    async (route) => {
      const resp = await route.fetch();
      acks.push({ route, status: resp.status(), body: await resp.text() });
    },
  );
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const link = page.getByRole("navigation", { name: "Boards" }).locator(`a[href="/?board=${here.name}"]`);
  await expect.poll(() => acks.length).toBe(1);
  await expect.poll(() => unreadOn(here.name)).toBe(0);
  await expect(link.locator(".unread-count")).toHaveCount(0);
  // A reply in a closed thread isn't shown, so it stays unread, at the same read position.
  const page1 = await api(pat, "GET", `/v1/boards/${here.name}/messages?newest=true&limit=1`);
  const root = (page1.messages as { id: string }[])[0];
  // The reply's head makes the page read the board and then the list again; both finish
  // before the old answer is let through, so nothing read later can mend it.
  const reread = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/boards");
  await api(pat, "POST", `/v1/boards/${here.name}/messages`, { body: "A reply alex hasn't opened.", to: ["all"], reply_to: root.id });
  await (await reread).finished();
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText("1");
  const answered = page.waitForResponse((r) => new URL(r.url()).pathname === `/v1/boards/${here.name}/ack`);
  await acks[0].route.fulfill({ status: acks[0].status, contentType: "application/json", body: acks[0].body });
  await (await answered).finished();
  await frames(page);
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText("1");
  expect(unreadOn(here.name)).toBe(1);
});

test("a board gone from a fresh list leaves the board view, and the others keep their newer read counts", async ({ page }) => {
  const here = await newBoard("Fresh view here");
  const keep = await otherWithUnread("Fresh view keep");
  const gone = await newBoard("Fresh view gone");
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const nav = page.getByRole("navigation", { name: "Boards" });
  const keepLink = nav.locator(`a[href="/?board=${keep.name}"]`);
  const goneLink = nav.locator(`a[href="/?board=${gone.name}"]`);
  await expect(keepLink.locator(".unread-count [aria-hidden]")).toHaveText("1");
  await expect(goneLink).toBeVisible();

  const lists = await holdLists(page);
  await api(ownerKey(), "POST", `/v1/boards/${here.name}/messages`, { body: "Board view.", to: ["all"] });
  await expect.poll(lists.count).toBe(1);
  await api(ownerKey(), "POST", `/v1/boards/${keep.name}/ack`, { up_to: keep.seq });
  await expect(keepLink.locator(".unread-count")).toHaveCount(0);
  await api(ownerKey(), "POST", `/v1/boards/${gone.name}/archive`, {});
  await api(ownerKey(), "POST", `/v1/boards/${gone.name}/delete`, {});
  // The board view reads lists one at a time: the old one first, then a fresh one
  // without the deleted board.
  await lists.release(0);
  await expect(keepLink.locator(".unread-count")).toHaveCount(0);
  await expect.poll(lists.count).toBe(2);
  await lists.release(1);
  await expect(goneLink).toHaveCount(0);
  await expect(keepLink.locator(".unread-count")).toHaveCount(0);
});

test("a board gone from a fresh list leaves the board list, and an older list can't bring it back", async ({ page }) => {
  const here = await newBoard("Fresh home here");
  const keep = await otherWithUnread("Fresh home keep");
  const gone = await newBoard("Fresh home gone");
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  await page.goto(`${base()}/`);
  const keepRow = page.locator(".board-row", { has: page.locator(`a[href="/?board=${keep.name}"]`) });
  const goneRow = page.locator(".board-row", { has: page.locator(`a[href="/?board=${gone.name}"]`) });
  await expect(keepRow.locator(".unread")).toHaveText("1 unread");
  await expect(goneRow).toBeVisible();

  const lists = await holdLists(page);
  await api(ownerKey(), "POST", `/v1/boards/${here.name}/messages`, { body: "List page.", to: ["all"] });
  await expect.poll(lists.count).toBe(1);
  await api(ownerKey(), "POST", `/v1/boards/${keep.name}/ack`, { up_to: keep.seq });
  await expect(keepRow.locator(".unread")).toHaveCount(0);
  await api(ownerKey(), "POST", `/v1/boards/${gone.name}/archive`, {});
  await expect.poll(lists.count).toBe(2);
  await api(ownerKey(), "POST", `/v1/boards/${gone.name}/delete`, {});
  await expect.poll(lists.count).toBe(3);
  // The newest list, read after the delete, arrives first; the older two after it.
  await lists.release(2);
  await expect(goneRow).toHaveCount(0);
  // Earlier tests may have left archived boards; the deleted one must not join them.
  const archivedNow = await archivedInList(page);
  for (const older of [0, 1]) {
    const shown = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/boards");
    await lists.release(older);
    await (await shown).finished();
    await frames(page);
    await expect(goneRow).toHaveCount(0);
    expect(await archivedInList(page)).toBe(archivedNow);
    await expect(keepRow).toBeVisible();
    await expect(keepRow.locator(".unread")).toHaveCount(0);
  }
});

// holdEvents keeps the page's event stream connections waiting, like holdStream, and
// send answers the waiting one with any one event, so a test can deliver a stream event
// late. The page connects again after each, to wait for the next.
async function holdEvents(page: Page): Promise<{ send: (event: string, data: unknown) => Promise<void> }> {
  const waiting: Route[] = [];
  await page.route("**/v1/stream", (route) => {
    waiting.push(route);
  });
  return {
    send: async (event: string, data: unknown) => {
      await expect.poll(() => waiting.length).toBeGreaterThan(0);
      await waiting.shift()!.fulfill({
        status: 200,
        headers: { "Content-Type": "text/event-stream" },
        body: `event: ${event}\ndata: ${JSON.stringify(data)}\n\n`,
      });
    },
  };
}

test("a board list read while an acknowledgement waits never moves the read position back", async ({ page }) => {
  const here = await otherWithUnread("Cursor ack held");
  const neighbour = await newBoard("Cursor neighbour");
  // The stream is held, so only the reads and the acknowledgement say how far alex read.
  const events = await holdEvents(page);
  const acks: Route[] = [];
  await page.route(
    (url) => url.pathname === `/v1/boards/${here.name}/ack`,
    (route) => {
      acks.push(route);
    },
  );
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const link = page.getByRole("navigation", { name: "Boards" }).locator(`a[href="/?board=${here.name}"]`);
  await expect(page.getByText(`A note on Cursor ack held.`, { exact: true })).toBeVisible();
  // The page asks to acknowledge the message it shows; the server hasn't taken it yet.
  await expect.poll(() => acks.length).toBe(1);
  expect(unreadOn(here.name)).toBe(1);

  // A list read now still has the old read position, and is held.
  const lists = await holdLists(page);
  await events.send("board_unavailable", { board_id: neighbour.id });
  await expect.poll(lists.count).toBe(1);

  // The server takes the acknowledgement and the page gets its answer.
  const answered = page.waitForResponse((r) => new URL(r.url()).pathname === `/v1/boards/${here.name}/ack`);
  await acks[0].continue();
  await (await answered).finished();
  expect(unreadOn(here.name)).toBe(0);
  await expect(link.locator(".unread-count")).toHaveCount(0);

  // The old list arrives last, read later than the acknowledgement started.
  const shown = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/boards");
  await lists.release(0);
  await (await shown).finished();
  await frames(page);
  await expect(link.locator(".unread-count")).toHaveCount(0);
});

test("a stream event delayed from before an acknowledgement never moves the read position back", async ({ page }) => {
  const here = await otherWithUnread("Cursor late event");
  const before = await api(ownerKey(), "GET", `/v1/boards/${here.name}`);
  const events = await holdEvents(page);
  const acked = page.waitForResponse((r) => new URL(r.url()).pathname === `/v1/boards/${here.name}/ack`);
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const link = page.getByRole("navigation", { name: "Boards" }).locator(`a[href="/?board=${here.name}"]`);
  await (await acked).finished();
  expect(unreadOn(here.name)).toBe(0);
  await expect(link.locator(".unread-count")).toHaveCount(0);

  // The unread event the server sent before the acknowledgement arrives only now.
  await events.send("unread", { board: here.name, board_id: here.id, read_up_to: before.read_up_to, unread: 1 });
  await frames(page);
  await expect(link.locator(".unread-count")).toHaveCount(0);
});

test("leaving a board clears its read count, a late event can't bring it back, and rejoining starts afresh", async ({ page }) => {
  const pat = await person("pat");
  const alexKey = ownerKey();
  // pat's open board, with alex on it and one message alex hasn't read.
  const made = await api(pat, "POST", "/v1/boards", { template: "general", title: "Pat's open board" });
  const board = { name: made.name as string, id: made.id as string };
  await api(pat, "POST", `/v1/boards/${board.name}/people`, { handle: "alex" });
  await api(pat, "POST", `/v1/boards/${board.name}/messages`, { body: "Before alex left.", to: ["all"] });
  const before = await api(alexKey, "GET", `/v1/boards/${board.name}`);
  const here = await newBoard("Leave check here");

  // The page's board lists also show open boards alex isn't on (all=true), as the
  // server gives them: on_board false, with no read position or unread count.
  await page.route(
    (url) => url.pathname === "/v1/boards",
    (route) => {
      const url = new URL(route.request().url());
      url.searchParams.set("all", "true");
      return route.continue({ url: url.toString() });
    },
  );
  const events = await holdEvents(page);
  await openLink(page, JSON.parse(aboard("open", "--board", here.name, "--json")).url);
  const link = page.getByRole("navigation", { name: "Boards" }).locator(`a[href="/?board=${board.name}"]`);
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText("1");

  // alex leaves; the list read after it still shows the open board, without bookkeeping.
  await api(alexKey, "POST", `/v1/boards/${board.name}/leave`, {});
  const fresh = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/boards");
  await events.send("board_unavailable", { board_id: board.id });
  const listed = (await (await fresh).json()) as { boards: { id: string; on_board: boolean; unread?: number }[] };
  const row = listed.boards.find((b) => b.id === board.id);
  expect(row?.on_board).toBe(false);
  expect(row?.unread).toBeUndefined();
  await expect(link).toBeVisible();
  await expect(link.locator(".unread-count")).toHaveCount(0);

  // An unread event from before the leave, arriving late, changes nothing.
  await events.send("unread", { board: board.name, board_id: board.id, read_up_to: before.read_up_to, unread: 1 });
  await frames(page);
  await expect(link.locator(".unread-count")).toHaveCount(0);

  // alex comes back and pat writes again: the fresh list's bookkeeping shows.
  await api(alexKey, "POST", `/v1/boards/${board.name}/people`, { handle: "alex" });
  await api(pat, "POST", `/v1/boards/${board.name}/messages`, { body: "After alex came back.", to: ["all"] });
  const now = unreadOn(board.name);
  expect(now).toBeGreaterThan(0);
  const again = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/boards");
  await events.send("board_unavailable", { board_id: board.id });
  await (await again).finished();
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText(String(now));
});

// fakeBoard is a board as a fresh read returns it, for ReadProgress on its own: only the
// fields it reads matter.
function fakeBoard(id: string, read: { read_up_to: number; unread: number } | null): Board {
  return { id, name: "general", on_board: read !== null, ...(read ?? {}) } as unknown as Board;
}

test("a not-on-board read that started before a rejoin read never clears the rejoined bookkeeping", () => {
  const p = new ReadProgress();
  const id = "brd_01JB8Z2Y5X4W3V2T1S0R9Q8P7N";
  p.board(fakeBoard(id, { read_up_to: 3, unread: 1 }), p.next());
  // A read starts while the person is off the board; its answer is held (generation 10).
  const offRead = p.next();
  // They rejoin, and a read that started later is taken first (generation 11).
  const onRead = p.next();
  expect(p.board(fakeBoard(id, { read_up_to: 5, unread: 2 }), onRead)).toMatchObject({ read_up_to: 5, unread: 2 });
  // The older not-on-board answer arrives last and changes nothing.
  p.board(fakeBoard(id, null), offRead);
  expect(p.apply(fakeBoard(id, null))).toMatchObject({ read_up_to: 5, unread: 2 });
  // Bookkeeping carries on: a new message at the same position raises the count.
  p.note(id, p.next(), 5, 3);
  expect(p.apply(fakeBoard(id, null))).toMatchObject({ read_up_to: 5, unread: 3 });

  // The other way round: an older on-board read never undoes a newer leave.
  const onAgain = p.next();
  const offAgain = p.next();
  expect(p.board(fakeBoard(id, null), offAgain)).toMatchObject({ read_up_to: undefined, unread: undefined });
  p.board(fakeBoard(id, { read_up_to: 6, unread: 4 }), onAgain);
  expect(p.apply(fakeBoard(id, null))).toMatchObject({ read_up_to: undefined, unread: undefined });
});

test("an agent's reply to the person opens its thread, so the person reads it and its receipt says so", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Replies to me", "--json"));
  const board: string = pair.board.name;
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  await page.getByRole("combobox", { name: "Message everyone" }).fill("What's the plan for the intro?");
  await page.getByRole("button", { name: "Post" }).click();
  const ask = page.locator(".message", { hasText: "What's the plan for the intro?" });
  await expect(ask).toContainText("You");

  // The writer replies in the thread, to the person by default; it arrives live.
  const read = JSON.parse(aboard("read", "--as", "writer", "--board", board, "--json"));
  const root = read.messages.find((m: { body: string }) => m.body === "What's the plan for the intro?");
  const reply = JSON.parse(aboard("say", "--as", "writer", "--board", board, "--reply", root.id, "Diagram first, then the text.", "--json")).message;
  expect(reply.to).toEqual(["@alex"]);
  const shown = page.locator(`[data-thread="${root.id}"] .message`, { hasText: "Diagram first, then the text." });
  await expect(shown).toBeVisible();
  await expect(shown.locator(".receipt-mark")).toHaveText("Read");
  await expect.poll(() => unreadOn(board)).toBe(0);
  expect(aboard("read", "--receipts", String(reply.seq), "--as", "writer", "--board", board)).toMatch(/alex\s+read/);
});

test("Mark all as read moves the person's read position to the newest message, and what arrives after stays unread", async ({ page }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Catch up", "--json"));
  const board: string = pair.board.name;
  const say = (...args: string[]) => JSON.parse(aboard("say", "--as", "writer", "--board", board, ...args, "--json")).message as { seq: number };
  say("Before you left.");
  aboard("read", "--mark-read", "--board", board);
  const first = say("--to", "@alex", "First new one, for you.");
  for (let i = 1; i <= 30; i++) say(`Catch-up step ${i}, written out so the timeline scrolls.`);
  const last = say("--to", "@alex", "Last new one, also for you.");
  expect(unreadOn(board)).toBe(32);

  // Opening at the newest message reads nothing above it, so the board stays unread, with
  // the divider above the first new message and the control in the header.
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await openLink(page, open.url);
  const newest = page.locator(".message", { hasText: "Last new one, also for you." });
  await expect(newest).toBeVisible();
  await expect(page.locator(".new-divider + .message")).toContainText("First new one, for you.");
  await expect(newest.locator(".receipt-mark")).toHaveText("Pending");
  const nav = page.getByRole("navigation", { name: "Boards" });
  const link = nav.locator(`a[href="/?board=${board}"]`);
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText(/\d+/);
  expect(unreadOn(board)).toBeGreaterThan(0);

  // Mark all as read clears the count, the divider and the control, and the person's
  // receipts read "read", here and from the CLI.
  await page.getByRole("button", { name: "Mark all as read", exact: true }).click();
  await expect(link.locator(".unread-count")).toHaveCount(0);
  await expect(page.locator(".new-divider")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Mark all as read", exact: true })).toHaveCount(0);
  await expect(newest.locator(".receipt-mark")).toHaveText("Read");
  expect(unreadOn(board)).toBe(0);
  for (const m of [first, last]) {
    expect(aboard("read", "--receipts", String(m.seq), "--as", "writer", "--board", board)).toMatch(/alex\s+read/);
  }

  // Reading further back, a message that arrives afterwards stays unread.
  await page.locator(".timeline").evaluate((el) => {
    el.scrollTop = 0;
    el.dispatchEvent(new Event("scroll"));
  });
  await expect(page.getByRole("button", { name: /Jump to newest/ })).toBeVisible();
  say("Arrived after you caught up.");
  await expect(link.locator(".unread-count [aria-hidden]")).toHaveText("1");
  await expect(page.getByRole("button", { name: "Mark all as read", exact: true })).toBeVisible();
  expect(unreadOn(board)).toBe(1);

  // Another board offers it in the board list, on hover. A message posted while the
  // request is on its way is newer than the click, and stays unread.
  const other = await newBoard("Other catch up");
  const pat = await person("pat");
  await api(ownerKey(), "POST", `/v1/boards/${other.name}/people`, { handle: "pat" });
  const otherLink = nav.locator(`a[href="/?board=${other.name}"]`);
  await expect(otherLink).toBeVisible();
  for (const body of ["One on the other board.", "Two on the other board."]) {
    await api(pat, "POST", `/v1/boards/${other.name}/messages`, { body, to: ["all"] });
  }
  await expect(otherLink.locator(".unread-count [aria-hidden]")).toHaveText("2");

  let release: () => void = () => {};
  const held = new Promise<void>((done) => (release = done));
  let sent: () => void = () => {};
  const asked = new Promise<void>((done) => (sent = done));
  await page.route(`**/v1/boards/${other.name}/ack`, async (route: Route) => {
    sent();
    await held;
    await route.continue();
  });
  await otherLink.hover();
  await nav.getByRole("button", { name: "Mark all as read on Other catch up" }).click();
  await asked;
  await api(pat, "POST", `/v1/boards/${other.name}/messages`, { body: "Three, after the click.", to: ["all"] });
  await expect(otherLink.locator(".unread-count [aria-hidden]")).toHaveText("3");
  release();
  await expect(otherLink.locator(".unread-count [aria-hidden]")).toHaveText("1");
  await expect.poll(() => unreadOn(other.name)).toBe(1);
});

test("a board owner removes another person's agent from the panel, and Show removed lists it", async ({ page }) => {
  const b = await newBoard("Removal check");
  const rowan = await person("rowan");
  await api(ownerKey(), "POST", `/v1/boards/${b.name}/people`, { handle: "rowan" });
  const joined = await api(rowan, "POST", "/v1/join", { board: b.name, role: "member" });
  const agent = (joined.agent as { name: string }).name;
  const token = joined.token as string;

  const open = JSON.parse(aboard("open", "--board", b.name, "--json"));
  await openLink(page, open.url);
  const panel = page.getByRole("complementary", { name: "Removal check" });
  const item = panel.locator(`[data-agent="${agent}"]`);
  await expect(item).toBeVisible();

  // Cancel changes nothing; Remove asks first, then removes the agent for good.
  await item.getByRole("button", { name: `Remove ${agent}` }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText(`Remove ${agent}?`);
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await expect(item).toBeVisible();
  await item.getByRole("button", { name: `Remove ${agent}` }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Remove", exact: true }).click();
  await expect(item).toHaveCount(0);

  // The record says who removed whose agent, and the agent's token is refused.
  await expect(page.locator(".board-event", { hasText: `alex removed rowan's agent ${agent}` })).toBeVisible();
  const refused = await fetch(`${base()}/v1/me/inbox`, { headers: { Authorization: `Bearer ${token}` } });
  expect(refused.status).toBe(403);
  expect(((await refused.json()) as { error: { code: string } }).error.code).toBe("agent_removed");

  await panel.getByRole("button", { name: "Show removed" }).click();
  await expect(panel.locator(`[data-removed-agent="${agent}"]`)).toContainText("removed by an owner");
});

test("tasks connect the Work panel, task view and conversation", async ({ page }) => {
  const board = await newBoard("Task UI check");
  const task = await api(ownerKey(), "POST", `/v1/boards/${board.name}/tasks`, { title: "Review the quickstart", about: "Check the fresh-machine path." });
  await api(ownerKey(), "POST", `/v1/boards/${board.name}/messages`, { body: "The fresh setup needs a review.", to: ["all"], about: [task.ref] });
  await api(ownerKey(), "POST", `/v1/boards/${board.name}/messages`, { body: "An unrelated note.", to: ["all"] });
  await openLink(page, JSON.parse(aboard("open", "--board", board.name, "--json")).url);
  await expect(page.getByRole("button", { name: `Open task ${task.ref}`, exact: true }).first()).toBeVisible();
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  await expect(page.getByRole("region", { name: "Not picked up", exact: true })).toContainText("Review the quickstart");
  await page.getByRole("button", { name: `Open task ${task.ref}`, exact: true }).first().click();
  const detail = page.getByRole("region", { name: `Task ${task.ref}`, exact: true });
  await expect(detail).toContainText("Check the fresh-machine path.");
  await expect(detail).toContainText("Where it stands");
  await api(ownerKey(), "POST", `/v1/boards/${board.name}/tasks/${task.ref}/start`, {});
  await api(ownerKey(), "PATCH", `/v1/boards/${board.name}/tasks/${task.ref}`, { stands: "Checking the clean setup next.", stands_base: 0 });
  await expect(detail).toContainText("Checking the clean setup next.");
  if (process.env.TASK_UI_QA) {
    for (const theme of ["Light", "Dark"]) {
      await page.getByRole("button", { name: /^You are alex/ }).click();
      await page.getByRole("menuitemradio", { name: theme, exact: true }).click();
      for (const [device, size] of [["desktop", { width: 1440, height: 1000 }], ["mobile", { width: 390, height: 844 }]] as const) {
        await page.setViewportSize(size);
        await page.screenshot({ path: `${process.env.TASK_UI_QA}/tasks-${device}-${theme.toLowerCase()}.png`, fullPage: true, animations: "disabled" });
      }
    }
    await page.setViewportSize({ width: 1440, height: 1000 });
  }
  await detail.getByRole("button", { name: "Show conversation" }).click();
  await expect(page.getByText(`Narrowed to ${task.ref}`, { exact: false })).toBeVisible();
  await expect(page.getByText("The fresh setup needs a review.", { exact: true })).toBeVisible();
  await expect(page.getByText("An unrelated note.", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Show everything" }).click();
  await expect(page.getByText("An unrelated note.", { exact: true })).toBeVisible();
});


test("messages in a hidden conversation stay unread while the person looks at tasks", async ({ page }) => {
  const board = await newBoard("Hidden task conversation");
  const pat = await person("pat");
  await api(ownerKey(), "POST", `/v1/boards/${board.name}/people`, { handle: "pat" });
  const task = await api(ownerKey(), "POST", `/v1/boards/${board.name}/tasks`, { title: "Check the record" });
  await openLink(page, JSON.parse(aboard("open", "--board", board.name, "--json")).url);
  await page.getByRole("tab", { name: /^Tasks/ }).click();
  await api(pat, "POST", `/v1/boards/${board.name}/messages`, { body: "A new note while you look at tasks.", to: ["all"], about: [task.ref] });
  await expect(page.getByRole("tabpanel").getByText("1 message · 1 thread", { exact: true })).toBeVisible();
  expect(unreadOn(board.name)).toBe(1);
  await expect(page.getByRole("log", { name: "Timeline" })).toBeHidden();
  await page.getByRole("tab", { name: "Conversation", exact: true }).click();
  await expect(page.getByText("A new note while you look at tasks.", { exact: true })).toBeVisible();
  await expect.poll(() => unreadOn(board.name)).toBe(0);
});

test.describe("team admin", () => {
  let previousEnv: typeof env;
  let adminHome: string;
  test.beforeAll(async () => {
    previousEnv = env;
    adminHome = mkdtempSync(join(tmpdir(), "aboard-web-admin-"));
    env = { ...env, HOME: adminHome, XDG_CONFIG_HOME: join(adminHome, ".config"), XDG_DATA_HOME: join(adminHome, ".local", "share"), XDG_STATE_HOME: join(adminHome, ".local", "state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}` };
  });
  test.afterAll(() => {
    try { aboard("down"); } finally { env = previousEnv; rmSync(adminHome, { recursive: true, force: true }); }
  });

test("People shows shared boards and terminal-only server administration", async ({ page }) => {
  const b = await newBoard("People shared room");
  const key = await person("people-check");
  await api(ownerKey(), "POST", `/v1/boards/${b.name}/people`, { handle: "people-check" });
  await api(key, "POST", "/v1/join", { board: b.name, role: "member" });
  const unshared = await api(key, "POST", "/v1/boards", { template: "general", title: "Not a shared room" });
  await api(key, "POST", "/v1/join", { board: unshared.name, role: "member" });
  const hidden = await api(key, "POST", "/v1/boards", { template: "general", title: "Private admin must not discover", visibility: "private" });
  const hiddenSeat = await api(key, "POST", "/v1/join", { board: hidden.name, role: "member" });
  const boardReads: string[] = [];
  page.on("request", (r) => { if (r.url().includes("/v1/boards/")) boardReads.push(r.url()); });
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await page.getByRole("menuitem", { name: "People", exact: true }).click();
  await expect(page.getByRole("heading", { name: "People", exact: true })).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "@people-check" });
  await expect(row).toContainText("Member");
  await expect(row).toContainText("People shared room");
  await expect(row.getByRole("cell").nth(2)).toHaveText("Agents on shared boards: 1");
  await expect(row).not.toContainText("Not a shared room");
  await expect(page.locator("body")).not.toContainText(String(hidden.title));
  await expect(page.locator("body")).not.toContainText(String(hidden.name));
  await expect(page.locator("body")).not.toContainText((hiddenSeat.agent as { name: string }).name);
  expect(boardReads.some((url) => url.includes(`/v1/boards/${hidden.name}/`))).toBe(false);
  await expect(page.getByRole("columnheader", { name: "Agents on shared boards" })).toBeVisible();
  await expect(page.getByText("Last active", { exact: true })).toHaveCount(0);
  let writes = 0;
  page.on("request", (r) => { if (/\/v1\/(people|invites)/.test(r.url()) && r.method() !== "GET") writes++; });
  await row.getByRole("button", { name: "Role command" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("Run this in your terminal");
  await expect(dialog).toContainText(`aboard people role @people-check admin --server ${base()}`);
  await dialog.getByRole("button", { name: "Close", exact: true }).click();
  await page.getByRole("button", { name: "Invite command" }).click();
  await expect(page.getByRole("alertdialog")).toContainText(`aboard invite --server ${base()}`);
  await page.getByRole("button", { name: "Close", exact: true }).click();
  expect(writes).toBe(0);
  await captureAdmin(page, "people");
  await page.route(`**/v1/boards/${b.name}/members`, (route) => route.fulfill({ status: 503, json: { error: { code: "server_unreachable", message: "Unavailable", hint: "Try again." } } }));
  await page.reload();
  await expect(page.getByRole("row").filter({ hasText: "@people-check" })).toContainText("Unavailable");
});

test("a server member sees People without admin commands or owner controls", async ({ page }) => {
  const b = await newBoard("Member visibility room");
  const key = await person("people-member");
  await api(ownerKey(), "POST", `/v1/boards/${b.name}/people`, { handle: "people-member" });
  await page.goto(base());
  await page.getByLabel("Access key").fill(key);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are people-member/ })).toBeVisible();
  await page.getByRole("button", { name: /^You are people-member/ }).click();
  await page.getByRole("menuitem", { name: "People", exact: true }).click();
  await expect(page.getByRole("heading", { name: "People", exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: /Invite command|Role command|Remove command/ })).toHaveCount(0);
  await page.goto(`${base()}/?board=${b.name}`);
  await page.getByRole("button", { name: /Member visibility room.*board details/ }).click();
  await expect(page.getByText("Visibility: ", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "Change visibility" })).toHaveCount(0);
});

test("board owners confirm visibility and cancel without changing access", async ({ page }) => {
  const b = await newBoard("Visibility control room");
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  await page.goto(`${base()}/?board=${b.name}`);
  await page.getByRole("button", { name: /Visibility control room.*board details/ }).click();
  await page.getByRole("button", { name: "Change visibility" }).click();
  const dialog = page.getByRole("alertdialog");
  await expect(dialog).toContainText("People already on the board keep their access");
  await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
  expect((await api(ownerKey(), "GET", `/v1/boards/${b.name}`)).visibility).toBe("open");
  await page.getByRole("button", { name: "Change visibility" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Make private", exact: true }).click();
  await expect(page.locator(".visibility[data-visibility=private]").first()).toBeVisible();
  expect((await api(ownerKey(), "GET", `/v1/boards/${b.name}`)).visibility).toBe("private");
  await page.getByRole("button", { name: "Change visibility" }).click();
  await expect(page.getByRole("alertdialog")).toContainText("whole history and files");
  await captureAdmin(page, "visibility");
  await page.getByRole("alertdialog").getByRole("button", { name: "Make open", exact: true }).click();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  expect((await api(ownerKey(), "GET", `/v1/boards/${b.name}`)).visibility).toBe("open");
});

async function captureAdmin(page: Page, surface: string) {
  const directory = process.env.ABOARD_ADMIN_SCREENSHOTS;
  if (!directory) return;
  mkdirSync(directory, { recursive: true });
  for (const theme of ["light", "dark"]) {
    await page.evaluate((value) => { document.documentElement.dataset.theme = value; }, theme);
    for (const [device, width] of [["desktop", 1440], ["mobile", 390]] as const) {
      await page.setViewportSize({ width, height: 900 });
      expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
      await page.screenshot({ path: join(directory, `${surface}-${theme}-${device}.png`), fullPage: surface === "people", animations: "disabled" });
    }
  }
  await page.setViewportSize({ width: 1280, height: 720 });
}

test("visibility ignores a preview for the previous access state", async ({ page }) => {
  const b = await newBoard("Held visibility room");
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  await page.goto(`${base()}/?board=${b.name}`);
  await page.getByRole("button", { name: /Held visibility room.*board details/ }).click();
  let held!: Route;
  let entered!: () => void;
  const waiting = new Promise<void>((resolve) => { entered = resolve; });
  await page.route(`**/v1/boards/${b.name}/visibility`, async (route) => { held = route; entered(); });
  await page.getByRole("button", { name: "Change visibility" }).click();
  await waiting;
  await api(ownerKey(), "POST", `/v1/boards/${b.name}/visibility`, { visibility: "private" });
  await expect(page.locator(".visibility[data-visibility=private]").first()).toBeVisible();
  await expect(page.getByRole("alertdialog")).toHaveCount(0);
  const oldPreview = held;
  const second = new Promise<void>((resolve) => { entered = resolve; });
  await page.getByRole("button", { name: "Change visibility" }).click();
  await second;
  const oldAnswer = page.waitForResponse((r) => r.url().endsWith(`/v1/boards/${b.name}/visibility`));
  await oldPreview.fulfill({ json: { board: b.name, before: "open", after: "private", changed: true, dry_run: true, reveals: null, join_codes_canceled: 5 } });
  await oldAnswer;
  await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  await expect(page.getByRole("button", { name: "Make open", exact: true })).toBeDisabled();
  await held.fulfill({ json: { board: b.name, before: "private", after: "open", changed: true, dry_run: true, reveals: { messages: 0, files: 0 }, join_codes_canceled: 0 } });
  await expect(page.getByRole("button", { name: "Make open", exact: true })).toBeEnabled();
});

test("My agents records an owner target and receipts for its current seats", async ({ page }) => {
  const b = await newBoard("Owner target room");
  const key = ownerKey();
  const one = await api(key, "POST", "/v1/join", { board: b.name, role: "member", name: "owner-one" });
  const two = await api(key, "POST", "/v1/join", { board: b.name, role: "member", name: "owner-two" });
  await openLink(page, JSON.parse(aboard("open", "--json")).url);
  await page.goto(`${base()}/?board=${b.name}`);
  await page.getByRole("button", { name: "Recipients: everyone. Change", exact: true }).click();
  await page.getByRole("menuitemcheckbox", { name: "My agents", exact: true }).click();
  await page.keyboard.press("Escape");
  await page.getByLabel("Message alex’s agents", { exact: true }).fill("To both my seats");
  await page.getByRole("button", { name: "Post", exact: true }).click();
  await expect(page.locator("main")).toContainText("alex’s agents");
  const messages = await api(key, "GET", `/v1/boards/${b.name}/messages`);
  const posted = (messages.messages as { to: string[]; seq: number; body: string }[]).find((m) => m.body === "To both my seats");
  expect(posted?.to).toEqual(["owner:alex"]);
  const receipt = await api(key, "GET", `/v1/boards/${b.name}/messages/${posted?.seq}/receipts`);
  const recipients = receipt.recipients as { member: { name: string } }[];
  expect(recipients.map((r) => r.member.name).sort()).toEqual([(one.agent as {name: string}).name, (two.agent as {name: string}).name].sort());
});

});

test.describe("person rename", () => {
  let oldEnv: typeof env;
  let renameHome: string;
  test.beforeAll(async () => {
    oldEnv = env;
    renameHome = mkdtempSync(join(tmpdir(), "aboard-web-rename-"));
    env = { ...env, HOME: renameHome, XDG_CONFIG_HOME: join(renameHome, ".config"), XDG_DATA_HOME: join(renameHome, ".local", "share"), XDG_STATE_HOME: join(renameHome, ".local", "state"), ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}` };
  });
  test.afterAll(() => {
    try { aboard("down"); } finally { env = oldEnv; rmSync(renameHome, { recursive: true, force: true }); }
  });
  test("rename updates a loaded historical author and current account", async ({ page }) => {
    const b = await newBoard("Rename history");
    const key = ownerKey();
    const peer = await person("rename-peer");
    await api(key, "POST", `/v1/boards/${b.name}/people`, { handle: "rename-peer" });
    await api(peer, "POST", `/v1/boards/${b.name}/messages`, { body: "Original @rename-peer mention stays", to: ["all"] });
    await openLink(page, JSON.parse(aboard("open", "--board", b.name, "--json")).url);
    await expect(page.getByText("Original @rename-peer mention stays", { exact: true })).toBeVisible();
    await expect(page.getByRole("button", { name: "You are alex. Account and settings" })).toBeVisible();
    await expect(page.locator(".timeline").getByText("rename-peer", { exact: true }).first()).toBeVisible();
    await api(peer, "POST", "/v1/people/rename-peer/rename", { handle: "rename-sam" });
    await expect(page.locator(".timeline").getByText("rename-sam", { exact: true }).first()).toBeVisible();
    await api(key, "POST", "/v1/people/alex/rename", { handle: "leo" });
    await expect(page.getByRole("button", { name: "You are leo. Account and settings" })).toBeVisible();
    await expect(page.getByText("Original @rename-peer mention stays", { exact: true })).toBeVisible();
    const timeline = page.locator(".timeline");
    await expect(timeline.getByText("rename-sam", { exact: true }).first()).toBeVisible();
  });
});
