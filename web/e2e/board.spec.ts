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

  // A new board says what to do next, shows the starter policy and its verified record.
  await expect(page.getByText("Nothing has been said on this board yet.")).toBeVisible();
  await expect(page.getByRole("banner").getByText("Starter policy")).toBeVisible();
  await expect(page.locator(".record")).toContainText(/Record verified · \d+ events/);
  const crew = page.getByRole("complementary", { name: "Who's here" });
  await expect(crew.locator('[data-agent="writer"]')).toBeVisible();
  await expect(crew.locator('[data-agent="reviewer"]')).toContainText("no session");

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

  // A side panel collapsed to its strip stays collapsed after a reload, and opens again.
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.getByRole("button", { name: "Hide Who's here" }).click();
  await expect(crew.locator('[data-agent="writer"]')).toBeHidden();
  await page.reload();
  await expect(page.getByRole("button", { name: "Show Who's here" })).toBeVisible();
  await expect(crew.locator('[data-agent="writer"]')).toBeHidden();
  await page.getByRole("button", { name: "Show Who's here" }).click();
  await expect(crew.locator('[data-agent="writer"]')).toBeVisible();

  await page.screenshot({ path: process.env.ABOARD_SCREENSHOT ?? "test-results/board-view.png", fullPage: true });

  // The list of boards shows the title, the name beside it, and the board's facts.
  await page.getByRole("link", { name: "Aboard" }).click();
  const row = page.locator(".board-row", { hasText: "Docs review" });
  await expect(row.getByRole("link", { name: "Docs review" })).toBeVisible();
  await expect(row).toContainText("writer-reviewer");
  await expect(row).toContainText("2 agents");
  await expect(row).toContainText("Starter policy");

  // The token lasts until the server stops; after that the page says to log in again.
  aboard("down");
  aboard("up");
  await page.reload();
  await expect(page.locator(".problem")).toContainText("Run aboard open again");
});
