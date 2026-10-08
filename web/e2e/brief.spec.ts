import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer as httpServer } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Page, expect, test } from "@playwright/test";

// The board's brief in the board view, against a real server in an isolated home: an
// agent writes it through the public API, the page shows it with its freshness, a person
// writes and edits it as a new version, a save against a version someone replaced is
// refused with the person's text kept, an HTML brief shows only in the sandbox, and the
// format switches only on request.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-brief-web-"));
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

// The board view runs under a strict Content-Security-Policy; the HTML test expects the
// preview's own policy to refuse loads, and says so.
let violations: string[] = [];
let refusalsExpected = false;
test.beforeEach(({ page }) => {
  violations = [];
  refusalsExpected = false;
  page.on("console", (m) => {
    if (m.text().includes("Content Security Policy")) violations.push(m.text());
  });
});
test.afterEach(() => {
  if (!refusalsExpected) expect(violations).toEqual([]);
});

function base(): string {
  return `http://${env.ABOARD_LOCAL_ADDR}`;
}

function tokenFile(file: string): string {
  return execFileSync("find", [home, "-name", file], { encoding: "utf8" }).trim().split("\n")[0];
}

function seatToken(board: string): string {
  const saved = JSON.parse(readFileSync(tokenFile("credentials.json"), "utf8")) as { agents: { board: string; token: string }[] };
  const found = saved.agents.find((a) => a.board === board);
  if (!found) throw new Error("The fixture seat was not saved.");
  return found.token;
}

function ownerToken(): string {
  return readFileSync(tokenFile("local-owner-token"), "utf8").trim();
}

async function openBoard(page: Page, board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}

type Written = { id: string; name: string; latest: { version: number } };

// putBrief writes the brief as the board's agent, as aboard brief put does: brief=true,
// against the active brief's file and version.
async function putBrief(board: string, name: "brief.md" | "brief.html", body: string, over?: { id: string; version: number }, replaceFormat = false): Promise<Written> {
  const qs = new URLSearchParams({ name, brief: "true", base: String(over?.version ?? 0) });
  if (over) qs.set("file_id", over.id);
  if (replaceFormat) qs.set("replace_format", "true");
  const r = await fetch(`${base()}/v1/boards/${board}/files?${qs}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/octet-stream", "Idempotency-Key": crypto.randomUUID() },
    body,
  });
  if (r.status !== 201) throw new Error(`put ${name}: ${r.status} ${await r.text()}`);
  return (await r.json()) as Written;
}

async function post(board: string, text: string) {
  const r = await fetch(`${base()}/v1/boards/${board}/messages`, {
    method: "POST",
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ body: text, to: ["all"] }),
  });
  if (!r.ok) throw new Error(`post: ${r.status} ${await r.text()}`);
}

// hold keeps the page from reading the board until released, as a slow connection
// would, so a save goes against the version the person opened.
async function hold(page: Page, board: string): Promise<{ release: () => Promise<void> }> {
  let open: () => void = () => {};
  const gate = new Promise<void>((done) => (open = done));
  const url = `**/v1/boards/${board}`;
  await page.route(url, async (route) => {
    await gate;
    // A read held across the release may already have been let through by unroute.
    await route.continue().catch(() => {});
  });
  return {
    release: async () => {
      open();
      await page.unroute(url);
    },
  };
}

type Summary = { name: string; file_id: string; version: number; by: { name: string; kind: string } } | null;

async function boardBrief(board: string): Promise<Summary> {
  const r = await fetch(`${base()}/v1/boards/${board}`, { headers: { Authorization: `Bearer ${ownerToken()}` } });
  return ((await r.json()) as { brief: Summary }).brief;
}

async function bytes(board: string, file: string, version: number): Promise<string> {
  const r = await fetch(`${base()}/v1/boards/${board}/files/${encodeURIComponent(file)}/versions/${version}`, { headers: { Authorization: `Bearer ${ownerToken()}` } });
  if (!r.ok) throw new Error(`get ${file}@${version}: ${r.status}`);
  return Buffer.from(await r.arrayBuffer()).toString();
}

const v1 = "# Checkout v2\n\nMoving checkout to the v2 API, **behind a flag** until Friday.\n\n## Who's doing what\n\n- writer keeps this brief\n- Payments retries back off to `10 min`\n";
const v2 = "# Checkout v2\n\nStaging is on v2; production flips on Friday.\n\n## Next\n\n1. Turn off v1 webhooks\n2. Watch the error rate\n";

test("the brief shows under the Now line with its version, author and what happened since", async ({ page }) => {
  const board = "brief-room";
  await openBoard(page, board);
  const brief = page.getByRole("region", { name: "Brief" });
  // No brief yet: what it's for, how agents write it, and a way for the person to write it.
  await expect(brief.getByRole("heading", { name: "No brief yet" })).toBeVisible();
  await expect(brief).toContainText("aboard brief put brief.md");
  await expect(brief.getByRole("button", { name: "Write the brief" })).toBeVisible();

  const first = await putBrief(board, "brief.md", v1);
  await putBrief(board, "brief.md", v2, { id: first.id, version: 1 });
  await post(board, "Staging flipped.");
  await post(board, "Error rate flat.");

  await expect(brief.locator(".brief-byline")).toContainText("Updated by writer · just now · brief.md v2 · since then 2 messages");
  // Closed, it shows where things stand in a sentence or two.
  await expect(brief.locator(".brief-summary")).toHaveText("Staging is on v2; production flips on Friday.");
  await brief.getByRole("button", { name: "Show full brief" }).click();
  const body = brief.getByLabel("The brief, brief.md v2");
  await expect(body.getByRole("heading", { name: "Checkout v2" })).toBeVisible();
  await expect(body.getByRole("listitem")).toHaveText(["Turn off v1 webhooks", "Watch the error rate"]);
  // Markdown is drawn as elements; nothing in it becomes HTML.
  await expect(body.locator("script, iframe")).toHaveCount(0);

  // Every version is one click away, in the file's panel.
  await brief.getByRole("button", { name: "All 2 versions" }).click();
  const panel = page.getByRole("region", { name: "File brief.md" });
  await expect(panel.getByRole("region", { name: "Versions" }).getByRole("listitem")).toHaveCount(2);

  // Closed, the summary reads as text: inline Markdown marks don't show.
  await brief.getByRole("button", { name: "Show less" }).click();
  await putBrief(board, "brief.md", "# Checkout v2\n\n_Last updated 2026-10-08 by @claude-2._ **Staging** is on `v2`; see [the runbook](https://example.com/runbook), as in snake_case_notes.\n", { id: first.id, version: 2 });
  await expect(brief.locator(".brief-byline")).toContainText("brief.md v3");
  await expect(brief.locator(".brief-summary")).toHaveText("Last updated 2026-10-08 by @claude-2. Staging is on v2; see the runbook, as in snake_case_notes.");
});

test("a person writes the brief and edits it, each save a new version with the exact text", async ({ page }) => {
  const board = "brief-edit";
  await openBoard(page, board);
  const brief = page.getByRole("region", { name: "Brief" });
  await brief.getByRole("button", { name: "Write the brief" }).click();
  const text = page.getByLabel("The brief, in Markdown");
  await expect(text).toBeFocused();
  const mine = "# Launch\n\nShip the brief UI by Friday.\n\n- Tests first\n- Then screenshots\n";
  await text.fill(mine);
  // The preview draws what the person wrote.
  await brief.getByText("Preview", { exact: true }).click();
  await expect(brief.getByRole("region", { name: "Preview" }).getByRole("heading", { name: "Launch" })).toBeVisible();
  await brief.getByText("Write", { exact: true }).click();
  await brief.getByRole("button", { name: "Save version 1" }).click();

  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.md v1/);
  const made = await boardBrief(board);
  expect(made).toMatchObject({ name: "brief.md", version: 1, by: { name: "alex", kind: "human" } });
  expect(await bytes(board, made!.file_id, 1)).toBe(mine);

  // Editing starts from the version on screen and saves the next one against it.
  await brief.getByRole("button", { name: "Edit" }).click();
  await expect(text).toHaveValue(mine);
  const edited = `${mine}- Then the launch post\n`;
  await text.fill(edited);
  // Cmd or Ctrl and Enter saves.
  await text.press("ControlOrMeta+Enter");
  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.md v2/);
  expect(await bytes(board, made!.file_id, 2)).toBe(edited);
  expect(await bytes(board, made!.file_id, 1)).toBe(mine);

  // Cancelling with changes asks first, and keeping on editing keeps the text.
  await brief.getByRole("button", { name: "Edit" }).click();
  await text.fill("scratch");
  await brief.getByRole("button", { name: "Cancel" }).click();
  await brief.getByRole("button", { name: "Keep editing" }).click();
  await expect(text).toHaveValue("scratch");
  await brief.getByRole("button", { name: "Cancel" }).click();
  await brief.getByRole("button", { name: "Discard" }).click();
  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.md v2/);
});

test("a save against a version someone replaced is refused, and the person's text is kept", async ({ page }) => {
  const board = "brief-stale";
  await openBoard(page, board);
  const first = await putBrief(board, "brief.md", v1);
  const brief = page.getByRole("region", { name: "Brief" });
  await brief.getByRole("button", { name: "Show full brief" }).click();
  await brief.getByRole("button", { name: "Edit" }).click();
  const text = page.getByLabel("The brief, in Markdown");
  const mine = `${v1}- alex: the flag stays on in EU\n`;
  await text.fill(mine);

  // The page is held back from reading the board while the agent writes v2, so the save
  // goes against v1 just as it would on a slow connection, and the server refuses it.
  const held = await hold(page, board);
  await putBrief(board, "brief.md", v2, { id: first.id, version: 1 });
  await brief.getByRole("button", { name: "Save version 2" }).click();
  const alert = brief.getByRole("alert");
  await expect(alert).toContainText("writer wrote v2");
  await expect(alert).toContainText("Nothing was saved. Your text is still here.");
  await held.release();
  // Once the page reads the board again, it names the newer version and offers it.
  await expect(alert).toContainText("writer wrote brief.md v2 just now, after v1, which you opened.");
  await expect(text).toHaveValue(mine);
  expect(await boardBrief(board)).toMatchObject({ version: 2, by: { name: "writer" } });
  expect(await bytes(board, first.id, 2)).toBe(v2);

  // The newer version is there to read, and continuing from it keeps the person's text beside it.
  await alert.getByRole("button", { name: "Show v2" }).click();
  await expect(alert.getByLabel("brief.md v2")).toContainText("Staging is on v2");
  await alert.getByRole("button", { name: "Continue from v2" }).click();
  await expect(text).toHaveValue(v2);
  await expect(brief.getByRole("textbox", { name: "Your earlier text" })).toHaveValue(mine);
  await text.fill(`${v2}3. Keep the flag on in EU\n`);
  await brief.getByRole("button", { name: "Save version 3" }).click();
  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.md v3/);
  expect(await bytes(board, first.id, 3)).toBe(`${v2}3. Keep the flag on in EU\n`);
});

// probes watches every request the whole browser context makes, from the board view
// itself and from every frame, and answers any request for a probe path on this server
// without letting it through, counting it.
async function probes(page: Page): Promise<{ all: string[]; api: string[] }> {
  const all: string[] = [];
  const api: string[] = [];
  page.context().on("request", (r) => all.push(r.url()));
  await page.context().route("**/v1/brief-probe/**", async (route) => {
    api.push(route.request().url());
    await route.fulfill({ status: 204 });
  });
  return { all, api };
}

// hostile is markup that names the outside server and this server's API in every way
// markup can ask for a load.
function hostile(out: string, tag: string): string {
  return [`${out}/${tag}`, `/v1/brief-probe/${tag}`]
    .map(
      (at) => `<link rel="stylesheet" href="${at}/link"><link rel="preload" as="image" href="${at}/preload">
<style>@import url("${at}/import"); .bg { background: url("${at}/css"); }</style>
<img src="${at}/img"><img srcset="${at}/srcset 2x"><picture><source srcset="${at}/source"><img src="${at}/picture"></picture>
<iframe src="${at}/iframe"></iframe><video poster="${at}/poster"></video><object data="${at}/object"></object><embed src="${at}/embed">
<div class="bg" style="background-image: url('${at}/style')">bg</div><svg><image href="${at}/svg"></image></svg>
<input type="image" src="${at}/input"><audio src="${at}/audio"></audio><table background="${at}/background"><tr><td>t</td></tr></table>`,
    )
    .join("\n");
}

async function counting(): Promise<{ url: string; hits: string[]; close: () => Promise<void> }> {
  const hits: string[] = [];
  const server = httpServer((req, res) => {
    hits.push(req.url ?? "");
    res.end("ok");
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  return {
    url: `http://127.0.0.1:${(server.address() as { port: number }).port}`,
    hits,
    close: () => new Promise<void>((done) => server.close(() => done())),
  };
}

test("an HTML brief shows only in the sandbox, and neither its summary nor its preview makes any request", async ({ page }) => {
  refusalsExpected = true;
  const board = "brief-html";
  await openBoard(page, board);
  const outside = await counting();
  const seen = await probes(page);
  const out = outside.url;
  // Any request from the page, the board view's own included, to the outside server or a probe path.
  const probed = () => seen.all.filter((u) => u.startsWith(out) || u.includes("brief-probe"));
  const none = async () => {
    // Let anything that slipped through arrive, then check that nothing did.
    await page.evaluate(() => new Promise((done) => setTimeout(done, 800)));
    expect(probed()).toEqual([]);
    expect(seen.api).toEqual([]);
    expect(outside.hits).toEqual([]);
  };

  // Controls: each counter counts a request made on purpose, so the zeros below mean none was made.
  await fetch(`${out}/control`);
  expect(outside.hits).toEqual(["/control"]);
  await page.evaluate(() => fetch("/v1/brief-probe/control"));
  expect(seen.api).toHaveLength(1);
  expect(probed()).toHaveLength(1);
  outside.hits.length = 0;
  seen.api.length = 0;
  seen.all.length = 0;

  const html = `<html><head><meta http-equiv="refresh" content="0; url=${out}/refresh">${hostile(out, "head")}
<style>h1 { color: rgb(160, 40, 40); }</style></head>
<body><h1>Status</h1><p id="state">Staging is on v2.</p>${hostile(out, "body")}
<a id="away" href="${out}/away" target="_top">away</a>
<script>document.getElementById("state").textContent = "script ran"; fetch("${out}/fetch"); fetch("/v1/brief-probe/script");</script></body></html>`;
  await putBrief(board, "brief.html", html);

  // Closed, the summary is read from the markup in the board view itself.
  const brief = page.getByRole("region", { name: "Brief" });
  await expect(brief.locator(".brief-byline")).toContainText(/Updated by writer · .+ · brief\.html v1/);
  await expect(brief.locator(".brief-summary")).toHaveText("Staging is on v2.");
  await none();

  // Open, the brief shows in the sandbox.
  await brief.getByRole("button", { name: "Show full brief" }).click();
  const iframe = brief.locator('iframe[title="The brief, brief.html v1"]');
  await expect(iframe).toHaveAttribute("sandbox", "");
  const frame = page.frameLocator('iframe[title="The brief, brief.html v1"]');
  await expect(frame.getByRole("heading", { name: "Status" })).toBeVisible();
  await expect(frame.locator("h1")).toHaveCSS("color", "rgb(160, 40, 40)");
  await expect(frame.locator("#state")).toHaveText("Staging is on v2.");
  await frame.locator("#away").click();
  await none();

  // Editing HTML is the source in the same box, previewed in the same sandbox.
  await brief.getByRole("button", { name: "Edit" }).click();
  const text = page.getByLabel("The brief, in HTML");
  await expect(text).toHaveValue(html);
  await text.fill(`<h1>Status</h1><p>Production on Friday.</p>${hostile(out, "edit")}<script>fetch("${out}/edit")</script>`);
  await brief.getByText("Preview", { exact: true }).click();
  const preview = page.frameLocator('iframe[title="Preview of the brief"]');
  await expect(preview.getByText("Production on Friday.")).toBeVisible();
  await none();
  await outside.close();
});

test("the format switches only on request, and the old brief's history stays", async ({ page }) => {
  const board = "brief-format";
  await openBoard(page, board);
  const first = await putBrief(board, "brief.md", v1);
  const brief = page.getByRole("region", { name: "Brief" });
  await brief.getByRole("button", { name: "Show full brief" }).click();
  await brief.getByRole("button", { name: "Edit" }).click();
  await expect(brief.getByRole("button", { name: "Save version 2" })).toBeVisible();
  await brief.getByText("HTML", { exact: true }).click();
  await expect(brief).toContainText("Saving switches the brief to brief.html, v1. brief.md leaves the board, and its versions stay in the record.");
  const html = "<h1>Checkout v2</h1>\n<p>Now in HTML.</p>\n";
  await page.getByLabel("The brief, in HTML").fill(html);
  await brief.getByRole("button", { name: "Save as brief.html" }).click();

  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.html v1/);
  const now = await boardBrief(board);
  expect(now).toMatchObject({ name: "brief.html", version: 1 });
  expect(now!.file_id).not.toBe(first.id);
  expect(await bytes(board, now!.file_id, 1)).toBe(html);
  // The Markdown brief's versions stay readable by its id.
  expect(await bytes(board, first.id, 1)).toBe(v1);

  // Switching back is the same explicit choice.
  await brief.getByRole("button", { name: "Edit" }).click();
  await brief.getByText("Markdown", { exact: true }).click();
  await page.getByLabel("The brief, in Markdown").fill("# Checkout v2\n\nBack to Markdown.\n");
  await brief.getByRole("button", { name: "Save as brief.md" }).click();
  await expect(brief.locator(".brief-byline")).toContainText(/Updated by you · .+ · brief\.md v1/);
  // After a save the brief stays open on what was written.
  await expect(brief.getByLabel("The brief, brief.md v1")).toHaveText("Checkout v2Back to Markdown.");
});

// Screenshots for review, only when BRIEF_SHOTS names a folder: desktop and phone, light
// and dark; the brief shown, editing, and a refused save.
test("screenshots of the brief", async ({ page }) => {
  const dir = process.env.BRIEF_SHOTS;
  test.skip(!dir, "Set BRIEF_SHOTS to a folder to take screenshots.");
  mkdirSync(dir!, { recursive: true });
  const board = "brief-shots";
  await openBoard(page, board);
  const brief = page.getByRole("region", { name: "Brief" });
  const sizes = { desktop: { width: 1440, height: 900 }, phone: { width: 390, height: 844 } } as const;
  const shoot = async (name: string) => {
    for (const theme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: theme });
      for (const [size, viewport] of Object.entries(sizes)) {
        await page.setViewportSize(viewport);
        await brief.scrollIntoViewIfNeeded();
        await page.mouse.move(0, 0);
        await page.screenshot({ animations: "disabled", path: join(dir!, `brief-${name}-${size}-${theme}.png`) });
      }
    }
    await page.emulateMedia({ colorScheme: "light" });
    await page.setViewportSize(sizes.desktop);
  };
  await expect(brief.getByRole("heading", { name: "No brief yet" })).toBeVisible();
  await shoot("empty");

  const status =
    "# Checkout v2\n\nStaging runs on the v2 API behind a flag; production flips on **Friday** once the error rate holds for a day.\n\n## Who's doing what\n\n- writer keeps this brief and the rollout notes\n- Payments retries back off to `10 min`\n\n## Blocked on\n\n- The EU flag: waiting on legal\n\n## Next\n\n1. Turn off v1 webhooks\n2. Watch the error rate\n";
  const first = await putBrief(board, "brief.md", "# Checkout v2\n\nStarted.\n");
  await putBrief(board, "brief.md", status, { id: first.id, version: 1 });
  for (const t of ["Staging flipped.", "Error rate flat for an hour.", "EU flag still waiting on legal."]) await post(board, t);
  await expect(brief.locator(".brief-byline")).toContainText("since then 3 messages");
  await shoot("closed");
  await brief.getByRole("button", { name: "Show full brief" }).click();
  await shoot("open");

  await brief.getByRole("button", { name: "Edit" }).click();
  await page.getByLabel("The brief, in Markdown").fill(`${status}3. alex: keep the flag on in EU\n`);
  await shoot("editing");

  const held = await hold(page, board);
  await putBrief(board, "brief.md", `${status}3. Announce in #payments\n`, { id: first.id, version: 2 });
  await brief.getByRole("button", { name: "Save version 3" }).click();
  await expect(brief.getByRole("alert")).toContainText("Nothing was saved.");
  await held.release();
  await brief.getByRole("button", { name: "Show v3" }).click();
  await shoot("conflict");
});
