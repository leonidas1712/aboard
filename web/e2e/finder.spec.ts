import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Page, expect, test } from "@playwright/test";

// The finder in the board view, against a real server in an isolated home: ⌘K (Ctrl+K
// off a Mac) or the board list's "Find or do anything" opens it, typing narrows it to
// boards, the board's tasks and places, Enter goes to the one picked, and Escape closes
// it with focus back where it was.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-finder-web-"));
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
  env = {
    NODE_ENV: "test",
    PATH: process.env.PATH,
    HOME: home,
    USER: "alex",
    XDG_CONFIG_HOME: join(home, ".config"),
    XDG_DATA_HOME: join(home, ".local/share"),
    XDG_STATE_HOME: join(home, ".local/state"),
    ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`,
    BROWSER: "true",
  };
});

test.afterAll(() => {
  try {
    if (env) aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

function seatToken(board: string): string {
  const file = execFileSync("find", [home, "-name", "credentials.json"], { encoding: "utf8" }).trim().split("\n")[0];
  const saved = JSON.parse(readFileSync(file, "utf8")) as { agents: { board: string; token: string }[] };
  const found = saved.agents.find((a) => a.board === board);
  if (!found) throw new Error("The fixture seat was not saved.");
  return found.token;
}

async function openTask(board: string, title: string): Promise<{ ref: string }> {
  const r = await fetch(`http://${env.ABOARD_LOCAL_ADDR}/v1/boards/${board}/tasks`, {
    method: "POST",
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ title }),
  });
  if (!r.ok) throw new Error(`task: ${r.status} ${await r.text()}`);
  return (await r.json()) as { ref: string };
}

function finder(page: Page) {
  return page.getByRole("dialog", { name: "Find or do anything" });
}

test("the finder goes to a board and a task, and Escape closes it", async ({ page }) => {
  aboard("pair", "general", "--board", "atlas", "--name", "writer", "--new");
  aboard("pair", "general", "--board", "harbor", "--name", "reader", "--new");
  const task = await openTask("atlas", "Check the fresh-machine quickstart");
  const link = JSON.parse(aboard("open", "--board", "atlas", "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
  await expect(page.getByRole("tab", { name: /Tasks/ })).toBeVisible();

  // The key opens it with the field focused, and the keys sheet lists it.
  await page.keyboard.press("ControlOrMeta+k");
  const box = finder(page);
  await expect(box).toBeVisible();
  const field = box.getByRole("combobox", { name: "Find a board, a task or a place" });
  await expect(field).toBeFocused();
  await expect(box.getByRole("option", { name: /harbor/ })).toBeVisible();
  await expect(box.getByRole("option", { name: new RegExp(task.ref) })).toBeVisible();

  // Typing narrows the list; Enter goes to the board picked.
  await field.fill("harb");
  await expect(box.getByRole("option")).toHaveCount(1);
  await expect(box.getByRole("option", { name: /harbor/ })).toHaveAttribute("aria-selected", "true");
  await field.fill("no such thing");
  await expect(box.getByRole("option")).toHaveCount(0);
  await expect(box.getByRole("status")).toHaveText("Nothing matches “no such thing”.");
  await field.fill("harb");
  await field.press("Enter");
  await expect(page).toHaveURL(/[?&]board=harbor\b/);
  await expect(page.locator('nav[aria-label="Boards"] a[aria-current="page"]')).toContainText("harbor");

  // From another board, a board's name and the arrows reach it too.
  await page.keyboard.press("ControlOrMeta+k");
  await finder(page).getByRole("combobox").fill("atlas");
  await finder(page).getByRole("combobox").press("ArrowDown");
  await finder(page).getByRole("combobox").press("ArrowUp");
  await finder(page).getByRole("combobox").press("Enter");
  await expect(page).toHaveURL(/[?&]board=atlas\b/);
  await expect(page.getByRole("tab", { name: /Tasks/ })).toBeVisible();

  // A task's reference finds it on the board on screen, and Enter opens it.
  await page.keyboard.press("ControlOrMeta+k");
  await finder(page).getByRole("combobox").fill(task.ref.toLowerCase());
  await expect(finder(page).getByRole("option")).toHaveCount(1);
  await finder(page).getByRole("combobox").press("Enter");
  await expect(finder(page)).toBeHidden();
  await expect(page.getByRole("region", { name: `Task ${task.ref}`, exact: true })).toBeVisible();

  // The key works from the message box, and gives the focus back to it.
  const message = page.locator("#message-body");
  await message.click();
  await page.keyboard.press("ControlOrMeta+k");
  await expect(finder(page)).toBeVisible();
  await page.keyboard.press("ControlOrMeta+k");
  await expect(finder(page)).toBeHidden();
  await expect(message).toBeFocused();

  // A place goes to a tab of the board.
  await page.keyboard.press("ControlOrMeta+k");
  await finder(page).getByRole("combobox").fill("tasks");
  await finder(page).getByRole("option", { name: /^Tasks/ }).click();
  await expect(page.getByRole("tab", { name: /Tasks/ })).toHaveAttribute("aria-selected", "true");

  // The board list's row opens it too; Escape closes it and gives focus back to the row.
  const row = page.getByRole("button", { name: /Find or do anything/ });
  await row.click();
  await expect(finder(page)).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(finder(page)).toBeHidden();
  await expect(row).toBeFocused();
  // Escape in the finder is its own: the board stays on screen.
  await expect(page).toHaveURL(/[?&]board=atlas\b/);

  // The keys sheet lists the key.
  await page.keyboard.press("?");
  await expect(page.getByRole("dialog", { name: "Keys on a board" })).toContainText("Find a board, or a task on the board you are on");
});
