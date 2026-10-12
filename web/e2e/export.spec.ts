import { execFileSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Page, expect, test } from "@playwright/test";

// The board's Export menu against a real server in an isolated home, with the export
// endpoint answered by the test: Copy as Markdown, Download .md and Download .zip, the
// server's filename or the page's fallback, the busy state, and the failure messages.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-export-web-"));
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

const markdown = "# general\n\n## Tasks\n\n- Done: ship the export\n";

async function openBoard(page: Page, board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}

async function choose(page: Page, item: string) {
  await page.getByRole("button", { name: "Export", exact: true }).click();
  await page.getByRole("menuitem", { name: item }).click();
}

test("Copy as Markdown puts the server's document on the clipboard and says so", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openBoard(page, "export-copy");
  const queries: string[] = [];
  await page.route("**/v1/boards/export-copy/export*", (route) => {
    queries.push(new URL(route.request().url()).search);
    return route.fulfill({ status: 200, contentType: "text/markdown; charset=utf-8", headers: { "Cache-Control": "no-store" }, body: markdown });
  });
  await choose(page, "Copy as Markdown");
  await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(markdown);
  expect(queries).toEqual(["?format=md"]);
});

test("Download .md saves under the server's filename", async ({ page }) => {
  await openBoard(page, "export-md");
  await page.route("**/v1/boards/export-md/export*", (route) =>
    route.fulfill({
      status: 200,
      contentType: "text/markdown; charset=utf-8",
      headers: { "Content-Disposition": 'attachment; filename="export-md-2026-10-11.md"', "Cache-Control": "no-store" },
      body: markdown,
    }),
  );
  const download = page.waitForEvent("download");
  await choose(page, "Download .md");
  const file = await download;
  expect(file.suggestedFilename()).toBe("export-md-2026-10-11.md");
  expect(readFileSync((await file.path()) as string, "utf8")).toBe(markdown);
  await expect(page.getByRole("status").filter({ hasText: "Saved export-md-2026-10-11.md" })).toBeVisible();
});

test("Download .zip saves the archive's bytes, and names it itself when the server gives no name", async ({ page }) => {
  await openBoard(page, "export-zip");
  const bytes = Buffer.from([0x50, 0x4b, 0x03, 0x04, 0x00, 0xff, 0x10, 0x80]);
  let query = "";
  await page.route("**/v1/boards/export-zip/export*", (route) => {
    query = new URL(route.request().url()).search;
    return route.fulfill({ status: 200, contentType: "application/zip", body: bytes });
  });
  const download = page.waitForEvent("download");
  await choose(page, "Download .zip");
  const file = await download;
  expect(query).toBe("?format=zip");
  expect(file.suggestedFilename()).toMatch(/^export-zip-\d{4}-\d{2}-\d{2}\.zip$/);
  expect(readFileSync((await file.path()) as string).equals(bytes)).toBe(true);
});

test("the menu shows a busy state while the export loads, and works from the keyboard", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openBoard(page, "export-busy");
  let release: () => void = () => {};
  const gate = new Promise<void>((done) => (release = done));
  await page.route("**/v1/boards/export-busy/export*", async (route) => {
    await gate;
    await route.fulfill({ status: 200, contentType: "text/markdown", body: markdown });
  });
  await page.getByRole("button", { name: "Export", exact: true }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("menuitem")).toHaveText(["Copy as Markdown", "Download .md", "Download .zip"]);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Exporting…" })).toBeDisabled();
  release();
  await expect(page.getByRole("status").filter({ hasText: "Copied" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Export", exact: true })).toBeEnabled();
  expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(markdown);
});

test("a failed export shows the API's message, and a 501 says the server can't export yet", async ({ page, context }) => {
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  await openBoard(page, "export-error");
  let reply: { status: number; body: unknown } = {
    status: 403,
    body: { error: { code: "forbidden", message: "You can't export this board.", hint: "Ask an admin." } },
  };
  await page.route("**/v1/boards/export-error/export*", (route) => route.fulfill({ status: reply.status, contentType: "application/json", body: JSON.stringify(reply.body) }));
  await choose(page, "Download .md");
  await expect(page.getByRole("status").filter({ hasText: "You can't export this board." })).toBeVisible();

  reply = { status: 501, body: { error: { code: "not_implemented", message: "Not implemented.", hint: "" } } };
  await choose(page, "Copy as Markdown");
  await expect(page.getByRole("status").filter({ hasText: "Export isn't available on this server yet" })).toBeVisible();
  await expect(page.getByRole("status").filter({ hasText: "Copied" })).toHaveCount(0);
});
