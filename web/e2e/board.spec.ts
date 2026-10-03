import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "@playwright/test";

// One isolated machine: its own home directory, local server port and aboard binary
// built with the UI embedded. Nothing touches the real home directory.
const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-web-e2e-"));
const bin = join(home, "aboard");
let env: Record<string, string | undefined> = {};

function aboard(...args: string[]): string {
  return execFileSync(bin, args, { cwd: home, env: env as NodeJS.ProcessEnv, encoding: "utf8" });
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
  await page.goto(open.url);

  // The page swaps the code for a token it keeps itself: the address bar loses the code
  // and no cookie is set.
  await expect(page).toHaveURL(/\/\?board=writer-reviewer$/);
  expect(page.url()).not.toContain("code");
  expect(await page.evaluate(() => document.cookie)).toBe("");
  expect((await page.context().cookies()).length).toBe(0);

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
  const mark = page.getByRole("banner").getByRole("link", { name: "Aboard" }).locator("svg");
  await expect(mark.locator("rect")).toHaveCount(3);

  // The left panel is navigation only: the boards, the current one selected, each with
  // its message count. Nothing about one board sits there.
  const nav = page.getByRole("complementary", { name: "Boards" });
  await expect(nav.getByRole("navigation", { name: "Boards" }).getByRole("link", { name: /Docs review/ })).toHaveAttribute(
    "aria-current",
    "page",
  );
  await expect(nav.locator(".message-count [aria-hidden]")).toHaveText("0");
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
  await page.getByRole("textbox", { name: "Message everyone" }).focus();
  await expect(field).toHaveAttribute("data-focused", "true");
  await page.getByRole("textbox", { name: "Message everyone" }).blur();
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
  await page.getByRole("textbox", { name: "Message everyone" }).fill("Thanks both. Ship it after the review.");
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

  // The board list's count follows the board live: five messages were posted above.
  await expect(nav.locator(".message-count [aria-hidden]")).toHaveText("5");

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
  await page.getByRole("link", { name: "Aboard" }).click();
  const row = page.locator(".board-row", { hasText: "Docs review" });
  await expect(row.getByRole("link", { name: "Docs review" })).toBeVisible();
  await expect(row).toContainText("writer-reviewer");
  await expect(row).toContainText("2 agents");
  await expect(row).toContainText("Starter policy");
  // The count comes from the board itself: five messages were posted above.
  await expect(row.locator(".messages")).toContainText("5");
  aboard("say", "--as", "reviewer", "--to", "@writer", "One more.");
  await expect(row.locator(".messages")).toContainText("6");

  // The login outlasts a restart of the server; once the person ends it, the page says to
  // log in again.
  aboard("down");
  aboard("up");
  await page.reload();
  await expect(page.locator(".board-row", { hasText: "Docs review" })).toBeVisible();
  aboard("logout", "--browsers");
  await page.reload();
  await expect(page.locator(".problem")).toContainText("Run aboard open again");
});

test("the board panel shows the board's details and adds an agent with a prompt the CLI can join with", async ({ page, context }) => {
  const pair = JSON.parse(aboard("pair", "writer-reviewer", "--new", "--title", "Invite check", "--json"));
  const board: string = pair.board.name;
  const open = JSON.parse(aboard("open", "--board", board, "--json"));
  await context.grantPermissions(["clipboard-read", "clipboard-write"], { origin: new URL(open.url).origin });
  await page.goto(open.url);

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
  await page.goto(open.url);

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
  await page.getByRole("textbox", { name: "Message writer" }).fill("Looks good once the cap is in.");
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
  await page.getByRole("textbox", { name: "Message reviewer" }).fill("Yes, merge it.");
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
