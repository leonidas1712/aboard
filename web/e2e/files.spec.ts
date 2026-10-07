import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer as httpServer } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Locator, type Page, expect, test } from "@playwright/test";

// The Files view and the file panel, against a real server in an isolated home: agents
// put files through the public API, and the page lists them, previews and downloads any
// version exactly, and uploads as the person without ever overwriting a newer version.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-files-web-"));
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

// The board view runs under a strict Content-Security-Policy: a preview that broke it
// would fail here.
let violations: string[] = [];
// The HTML preview test expects the preview's own policy to refuse loads, and says so.
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

type Put = { id: string; name: string; latest: { version: number; by: { name: string } } };

// put writes a version as the board's agent, through the public API, as aboard file put does.
async function put(board: string, name: string, over: number, body: string | Buffer): Promise<Put> {
  const qs = new URLSearchParams({ name, base: String(over) });
  const r = await fetch(`${base()}/v1/boards/${board}/files?${qs}`, {
    method: "POST",
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/octet-stream" },
    body: typeof body === "string" ? body : new Uint8Array(body),
  });
  if (r.status !== 201) throw new Error(`put ${name}: ${r.status} ${await r.text()}`);
  return (await r.json()) as Put;
}

async function bytes(board: string, file: string, version: number | "latest"): Promise<Buffer> {
  const r = await fetch(`${base()}/v1/boards/${board}/files/${encodeURIComponent(file)}/versions/${version}`, {
    headers: { Authorization: `Bearer ${ownerToken()}` },
  });
  if (!r.ok) throw new Error(`get ${file}@${version}: ${r.status}`);
  return Buffer.from(await r.arrayBuffer());
}

async function latest(board: string, file: string): Promise<{ version: number; by: { name: string; kind: string } }> {
  const r = await fetch(`${base()}/v1/boards/${board}/files/${encodeURIComponent(file)}`, { headers: { Authorization: `Bearer ${ownerToken()}` } });
  return ((await r.json()) as { latest: { version: number; by: { name: string; kind: string } } }).latest;
}

// A 2x2 PNG with four coloured pixels, so the preview has real bytes to draw.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8DAwMDAxMDAwMDAAAANBAEB3LmtjQAAAABJRU5ErkJggg==",
  "base64",
);

const v1 = "# API notes\n\nThe client retries **twice**.\n\n- Use `POST /v1/files`\n- Name the base version\n";
const v2 = "# API notes\n\nThe client retries **three times**.\n\n- Use `POST /v1/files`\n- Name the base version\n- Keep every version\n";

test("files appear live, open in the panel with every version, preview, and download each version's exact bytes", async ({ page }) => {
  const board = "files-room";
  await openBoard(page, board);
  // Every board has a Files view, even before its first file.
  await expect(page.getByRole("tab", { name: "Files 0" })).toBeVisible();

  const notes = await put(board, "notes/api.md", 0, v1);
  await expect(page.getByRole("tab", { name: "Files 1" })).toBeVisible();
  await put(board, "notes/api.md", 1, v2);
  await put(board, "diagram.png", 0, png);
  await expect(page.getByRole("tab", { name: "Files 2" })).toBeVisible();

  // The tabs move with the arrow keys.
  await page.getByRole("tab", { name: "Conversation" }).focus();
  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Files 2" })).toHaveAttribute("aria-selected", "true");

  const list = page.getByRole("list", { name: "Files" });
  const row = list.locator('[data-file="notes/api.md"]');
  await expect(row).toContainText("v2");
  await expect(row).toContainText("writer");
  await expect(row).toContainText("Markdown");
  // Newest first: the image, written last, leads.
  await expect(list.locator("[data-file]").first()).toHaveAttribute("data-file", "diagram.png");
  await page.getByRole("button", { name: "By name", exact: true }).click();
  await expect(list.locator("[data-file]").first()).toHaveAttribute("data-file", "diagram.png");
  await expect(list.locator("[data-file]").nth(1)).toHaveAttribute("data-file", "notes/api.md");
  await page.getByRole("button", { name: "From people", exact: true }).click();
  await expect(page.getByText("No files match.")).toBeVisible();
  await page.getByRole("button", { name: "Show all files" }).click();

  // The panel: what the file is, a formatted preview of the latest version, and every version.
  await row.getByRole("button", { name: "notes/api.md" }).click();
  const panel = page.getByRole("region", { name: "File notes/api.md" });
  await expect(panel.getByRole("heading", { name: "notes/api.md" })).toBeFocused();
  await expect(panel).toContainText("One-off · Markdown · v2");
  const preview = panel.getByLabel("Preview of notes/api.md, v2");
  await expect(preview.getByRole("heading", { name: "API notes" })).toBeVisible();
  await expect(preview).toContainText("three times");
  const versions = panel.getByRole("region", { name: "Versions" });
  await expect(versions.getByRole("listitem")).toHaveCount(2);
  await expect(versions.getByRole("listitem").first()).toContainText("v2");
  await expect(versions.getByRole("listitem").first()).toContainText("writer");

  // An older version previews on request, and downloads with its exact bytes.
  await versions.getByRole("button", { name: "Show v1" }).click();
  await expect(panel.getByLabel("Preview of notes/api.md, v1")).toContainText("twice");
  await expect(panel).toContainText("Showing v1, an older version.");
  const [older] = await Promise.all([page.waitForEvent("download"), panel.getByRole("link", { name: "Download v1" }).first().click()]);
  expect(older.suggestedFilename()).toBe("api-v1.md");
  expect(readFileSync((await older.path())!, "utf8")).toBe(v1);
  const [newest] = await Promise.all([page.waitForEvent("download"), versions.getByRole("link", { name: "Download v2" }).click()]);
  expect(newest.suggestedFilename()).toBe("api.md");
  expect(readFileSync((await newest.path())!, "utf8")).toBe(v2);
  expect(notes.latest.version).toBe(1);

  // An image previews from the server's own bytes, and downloads unchanged.
  await page.getByRole("button", { name: "Work" }).click();
  await list.locator('[data-file="diagram.png"]').getByRole("button", { name: "diagram.png" }).click();
  const image = page.getByRole("region", { name: "File diagram.png" }).getByRole("img", { name: "diagram.png, v1" });
  await expect(image).toBeVisible();
  await expect.poll(() => image.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBe(2);
  const [picture] = await Promise.all([page.waitForEvent("download"), page.getByRole("region", { name: "File diagram.png" }).getByRole("link", { name: "Download v1" }).first().click()]);
  expect(readFileSync((await picture.path())!).equals(png)).toBe(true);
});

test("the person uploads a new version against the one they saw, and a newer version is never overwritten", async ({ page }) => {
  const board = "files-upload";
  await openBoard(page, board);
  await put(board, "plan.md", 0, "# Plan\n\nFirst draft.\n");
  await page.getByRole("tab", { name: "Files 1" }).click();
  await page.locator('[data-file="plan.md"]').getByRole("button", { name: "plan.md" }).click();
  const panel = page.getByRole("region", { name: "File plan.md" });
  await expect(panel).toContainText("v1");

  // The writer saves v2 after the person chose to upload against v1: the server refuses,
  // stores nothing, and the panel says who wrote what.
  const chooser = page.waitForEvent("filechooser");
  await panel.getByRole("button", { name: "Upload a new version (after v1)" }).click();
  const picker = await chooser;
  await put(board, "plan.md", 1, "# Plan\n\nThe writer's second draft.\n");
  await picker.setFiles({ name: "plan.md", mimeType: "text/markdown", buffer: Buffer.from("# Plan\n\nAlex's edit.\n") });
  await expect(panel.getByRole("alert")).toContainText("writer wrote v2");
  await expect(panel.getByRole("alert")).toContainText("Nothing was uploaded.");
  expect((await latest(board, "plan.md")).version).toBe(2);
  expect((await bytes(board, "plan.md", "latest")).toString()).toBe("# Plan\n\nThe writer's second draft.\n");

  // The panel now shows v2; uploading against it works and becomes v3, by the person.
  await expect(panel.getByLabel("Preview of plan.md, v2")).toContainText("second draft");
  const again = page.waitForEvent("filechooser");
  await panel.getByRole("button", { name: "Upload a new version (after v2)" }).click();
  await (await again).setFiles({ name: "plan.md", mimeType: "text/markdown", buffer: Buffer.from("# Plan\n\nAlex's edit, on v2.\n") });
  await expect(panel.getByRole("status").filter({ hasText: "Uploaded v3." })).toBeVisible();
  await expect(panel.getByLabel("Preview of plan.md, v3")).toContainText("Alex's edit, on v2.");
  const now = await latest(board, "plan.md");
  expect(now).toMatchObject({ version: 3, by: { name: "alex", kind: "human" } });

  // A new file from this computer: its name becomes a path the board takes, and a name
  // someone else took first is refused with nothing stored.
  const files = page.getByRole("tabpanel", { name: /Files/ });
  let pick = page.waitForEvent("filechooser");
  await files.getByRole("button", { name: "Upload a file" }).click();
  await (await pick).setFiles({ name: "release notes.txt", mimeType: "text/plain", buffer: Buffer.from("Ship on Friday.\n") });
  const nameField = files.getByLabel("Name on the board");
  await expect(nameField).toHaveValue("release-notes.txt");
  await nameField.fill("notes/release.txt");
  await files.getByRole("button", { name: "Upload", exact: true }).click();
  const added = page.getByRole("region", { name: "File notes/release.txt" });
  await expect(added).toContainText("v1");
  await expect(added.getByLabel("Preview of notes/release.txt, v1")).toContainText("Ship on Friday.");
  expect((await bytes(board, "notes/release.txt", 1)).toString()).toBe("Ship on Friday.\n");

  // The list follows the board live, so a name taken while the person chose it is caught
  // before sending: Upload waits, and the taken file is one click away.
  pick = page.waitForEvent("filechooser");
  await files.getByRole("button", { name: "Upload a file" }).click();
  await (await pick).setFiles({ name: "todo.md", mimeType: "text/markdown", buffer: Buffer.from("- mine\n") });
  await put(board, "todo.md", 0, "- the writer's\n");
  await expect(files.getByText("todo.md is already on the board. Open it to upload a new version, or choose another name.")).toBeVisible();
  await expect(files.getByRole("button", { name: "Upload", exact: true })).toBeDisabled();
  await files.getByRole("button", { name: "Cancel" }).click();

  // When the page hasn't heard yet, the server refuses the same write and stores nothing.
  await page.route(`**/v1/boards/${board}/files?limit=200`, () => {});
  pick = page.waitForEvent("filechooser");
  await files.getByRole("button", { name: "Upload a file" }).click();
  await (await pick).setFiles({ name: "later.md", mimeType: "text/markdown", buffer: Buffer.from("- mine\n") });
  await put(board, "later.md", 0, "- the writer's\n");
  await files.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(files.getByRole("alert")).toContainText("later.md is already on the board, at v1 by writer");
  await expect(files.getByRole("alert")).toContainText("Nothing was uploaded.");
  expect((await bytes(board, "later.md", "latest")).toString()).toBe("- the writer's\n");
  await page.unrouteAll({ behavior: "ignoreErrors" });
});

test("an empty board says how files get there, and the person uploads the first one with the button", async ({ page }) => {
  const board = "files-empty";
  await openBoard(page, board);
  await page.getByRole("tab", { name: "Files 0" }).click();
  const view = page.getByRole("tabpanel", { name: /Files/ });
  await expect(view.getByRole("heading", { name: "No files on this board yet." })).toBeVisible();
  await expect(view).toContainText("aboard file put report.md");
  const chooser = page.waitForEvent("filechooser");
  await view.getByRole("button", { name: "Upload a file" }).click();
  await (await chooser).setFiles({ name: "brief notes.md", mimeType: "text/markdown", buffer: Buffer.from("# First\n") });
  await expect(view.getByLabel("Name on the board")).toHaveValue("brief-notes.md");
  await view.getByRole("button", { name: "Upload", exact: true }).click();
  await expect(page.getByRole("tab", { name: "Files 1" })).toBeVisible();
  await expect(page.getByRole("region", { name: "File brief-notes.md" })).toContainText("v1");
  expect((await bytes(board, "brief-notes.md", 1)).toString()).toBe("# First\n");
});

// drag holds a file from this computer over target, as a browser does during a drag;
// drop then lets it go.
async function drag(page: Page, target: Locator, name: string, body: string) {
  const data = await page.evaluateHandle(([n, b]) => {
    const t = new DataTransfer();
    t.items.add(new File([b], n, { type: "text/plain" }));
    return t;
  }, [name, body] as const);
  await target.dispatchEvent("dragenter", { dataTransfer: data });
  await target.dispatchEvent("dragover", { dataTransfer: data });
  return { drop: () => target.dispatchEvent("drop", { dataTransfer: data }), leave: () => target.dispatchEvent("dragleave", { dataTransfer: data }) };
}

test("a file dropped on the Files view starts an upload, and one dropped on a file's panel becomes its next version", async ({ page }) => {
  const board = "files-drop";
  await openBoard(page, board);
  await page.getByRole("tab", { name: "Files 0" }).click();
  const view = page.getByRole("tabpanel", { name: /Files/ });

  // While a file is held over the view, it shows where it will go; leaving clears it.
  const zone = view.locator("[data-drop], .files-view").first();
  let held = await drag(page, zone, "draft.txt", "Dropped words.\n");
  await expect(page.locator(".drop-hint")).toContainText("Drop to upload it to this board");
  await held.leave();
  await expect(page.locator(".drop-hint")).toHaveCount(0);

  held = await drag(page, zone, "draft.txt", "Dropped words.\n");
  await held.drop();
  await expect(page.locator(".drop-hint")).toHaveCount(0);
  await expect(view.getByLabel("Name on the board")).toHaveValue("draft.txt");
  await view.getByRole("button", { name: "Upload", exact: true }).click();
  const panel = page.getByRole("region", { name: "File draft.txt" });
  await expect(panel).toContainText("v1");
  expect((await bytes(board, "draft.txt", 1)).toString()).toBe("Dropped words.\n");

  // Dropped on the open file, it is written against the version on screen.
  held = await drag(page, panel, "draft-2.txt", "Second words.\n");
  await expect(page.locator(".drop-hint")).toContainText("Drop to upload it as v2");
  await held.drop();
  await expect(panel.getByRole("status").filter({ hasText: "Uploaded v2." })).toBeVisible();
  expect(await latest(board, "draft.txt")).toMatchObject({ version: 2, by: { name: "alex", kind: "human" } });
  expect((await bytes(board, "draft.txt", 2)).toString()).toBe("Second words.\n");

  // A drop against a version someone has since replaced is refused, and nothing is stored.
  await page.route(`**/v1/boards/${board}/files/*`, () => {});
  await put(board, "draft.txt", 2, "The writer's v3.\n");
  held = await drag(page, panel, "late.txt", "Too late.\n");
  await held.drop();
  await expect(panel.getByRole("alert")).toContainText("writer wrote v3");
  expect((await bytes(board, "draft.txt", "latest")).toString()).toBe("The writer's v3.\n");
  await page.unrouteAll({ behavior: "ignoreErrors" });
});

// agent calls the public API as the board's agent.
async function agent(board: string, method: string, path: string, body?: unknown): Promise<Record<string, unknown>> {
  const r = await fetch(`${base()}${path}`, {
    method,
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  if (!r.ok) throw new Error(`${method} ${path}: ${r.status} ${await r.text()}`);
  return (await r.json()) as Record<string, unknown>;
}

test("the panel links to the messages that posted a file, and a file removed and made again at its path refuses a stale upload", async ({ page }) => {
  const board = "files-identity";
  await openBoard(page, board);
  const first = await put(board, "plan.md", 0, "# Plan\n\nThe first file.\n");
  const posted = await agent(board, "POST", `/v1/boards/${board}/messages`, { body: "The plan is up.", to: ["all"], files: [{ file: "plan.md", version: 1 }] });
  await page.getByRole("tab", { name: "Files 1" }).click();
  await page.locator('[data-file="plan.md"]').getByRole("button", { name: "plan.md" }).click();
  const panel = page.getByRole("region", { name: "File plan.md" });

  // Posted in leads to the message in the conversation.
  const link = panel.getByRole("region", { name: "Posted in" }).getByRole("button", { name: "A message, with v1" });
  await link.click();
  await expect(page.getByRole("tab", { name: "Conversation" })).toHaveAttribute("aria-selected", "true");
  await expect(page.locator(`[data-id="${posted.id}"]`)).toBeInViewport();
  await page.getByRole("tab", { name: /^Files/ }).click();

  // The writer takes the file off the board and puts a different one at the same path,
  // also at v1, while the panel still shows the first. An upload against what the panel
  // shows names that file, so it is refused, and the new file keeps its bytes.
  await page.route(`**/v1/boards/${board}/files/*`, () => {});
  await agent(board, "DELETE", `/v1/boards/${board}/files/${first.id}`);
  const second = await put(board, "plan.md", 0, "# Plan\n\nA different file.\n");
  expect(second.id).not.toBe(first.id);
  expect(second.latest.version).toBe(1);
  const chooser = page.waitForEvent("filechooser");
  await panel.getByRole("button", { name: "Upload a new version (after v1)" }).click();
  await (await chooser).setFiles({ name: "plan.md", mimeType: "text/markdown", buffer: Buffer.from("# Plan\n\nAlex's edit of the first.\n") });
  await expect(panel.getByRole("alert")).toContainText("plan.md was removed or replaced on the board since you opened it.");
  await expect(panel.getByRole("alert")).toContainText("Nothing was uploaded.");
  expect(await latest(board, second.id)).toMatchObject({ version: 1, by: { name: "writer" } });
  expect((await bytes(board, second.id, 1)).toString()).toBe("# Plan\n\nA different file.\n");
  await page.unrouteAll({ behavior: "ignoreErrors" });
});

test("an HTML file previews in a sandbox that runs no script, loads nothing and can't move the page", async ({ page }) => {
  refusalsExpected = true;
  const board = "files-html";
  await openBoard(page, board);
  // A server that counts every request the preview might make.
  const hits: string[] = [];
  const tracker = httpServer((req, res) => {
    hits.push(req.url ?? "");
    res.end("ok");
  });
  await new Promise<void>((done) => tracker.listen(0, "127.0.0.1", done));
  const out = `http://127.0.0.1:${(tracker.address() as { port: number }).port}`;
  const dot = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==";
  const html = `<html><head><link rel="stylesheet" href="${out}/style.css"><style>h1 { color: rgb(160, 40, 40); } body { background: url(${out}/bg.png); }</style></head>
<body><h1>Report</h1><p id="state">static</p>
<img id="inline" src="data:image/png;base64,${dot}"><img id="outside" src="${out}/pixel.png">
<a id="top" href="${out}/top" target="_top">leave the board</a> <a id="inside" href="${out}/inside">go elsewhere</a>
<form action="${out}/form" method="post"><button id="send">Send</button></form>
<script>document.getElementById("state").textContent = "script ran"; fetch("${out}/fetch"); top.location = "${out}/script";</script>
</body></html>`;
  await put(board, "report.html", 0, html);
  await page.getByRole("tab", { name: "Files 1" }).click();
  await page.locator('[data-file="report.html"]').getByRole("button", { name: "report.html" }).click();
  const panel = page.getByRole("region", { name: "File report.html" });
  const frame = page.frameLocator('iframe[title="Preview of report.html, v1"]');

  // The file's markup and its own styles and data: images render.
  await expect(frame.getByRole("heading", { name: "Report" })).toBeVisible();
  await expect(frame.locator("h1")).toHaveCSS("color", "rgb(160, 40, 40)");
  await expect.poll(() => frame.locator("#inline").evaluate((i: HTMLImageElement) => i.naturalWidth)).toBe(1);
  // The frame has no permissions at all.
  await expect(panel.locator("iframe")).toHaveAttribute("sandbox", "");
  // Its script never ran: no text change, no fetch, no navigation.
  await expect(frame.locator("#state")).toHaveText("static");
  expect(await frame.locator("#outside").evaluate((i: HTMLImageElement) => i.naturalWidth)).toBe(0);

  // Neither link nor the form moves the board view or reaches the outside server.
  const here = page.url();
  await frame.locator("#top").click();
  await frame.locator("#inside").click();
  await frame.locator("#send").click();
  await expect(page.getByRole("region", { name: "File report.html" })).toBeVisible();
  expect(page.url()).toBe(here);
  expect(page.context().pages()).toHaveLength(1);
  await expect(frame.locator("#state")).toHaveText("static");
  // Give anything that slipped through time to arrive, then check nothing did.
  await page.evaluate(() => new Promise((done) => setTimeout(done, 500)));
  expect(hits).toEqual([]);
  // The bytes are still offered as they are.
  await expect(panel.getByRole("link", { name: "Download v1" }).first()).toBeVisible();
  expect((await bytes(board, "report.html", 1)).toString()).toBe(html);
  await new Promise((done) => tracker.close(done));
});

// Screenshots for review, only when FILES_SHOTS names a folder: the list and the panel,
// light and dark, wide and narrow.
test("screenshots of the Files view and the file panel", async ({ page }) => {
  const dir = process.env.FILES_SHOTS;
  test.skip(!dir, "Set FILES_SHOTS to a folder to take screenshots.");
  mkdirSync(dir!, { recursive: true });
  const board = "files-shots";
  await openBoard(page, board);
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.getByRole("tab", { name: "Files 0" }).click();
  await expect(page.getByRole("heading", { name: "No files on this board yet." })).toBeVisible();
  await page.screenshot({ animations: "disabled", path: join(dir!, "files-empty-light.png") });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.screenshot({ animations: "disabled", path: join(dir!, "files-empty-dark.png") });
  await page.emulateMedia({ colorScheme: "light" });
  const task = (await (
    await fetch(`${base()}/v1/boards/${board}/tasks`, {
      method: "POST",
      headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/json" },
      body: JSON.stringify({ title: "Document the v2 webhooks", start: true }),
    })
  ).json()) as { ref: string };
  const status = `# Webhooks v2\n\nWhere the migration stands, kept current by the writer. See ${task.ref}.\n\n## Done\n\n- Signing secrets rotate on the **v2** endpoint\n- Retries back off to \`10 min\`\n\n## Next\n\n1. Move the staging hooks\n2. Turn off v1 on Friday\n`;
  await put(board, "status.md", 0, "# Webhooks v2\n\nStarted.\n");
  const qs = new URLSearchParams({ name: "status.md", base: "1", maintained: "true", about: task.ref });
  await fetch(`${base()}/v1/boards/${board}/files?${qs}`, { method: "POST", headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/octet-stream" }, body: status });
  await put(board, "notes/retry-policy.md", 0, "# Retry policy\n\nBack off exponentially, up to ten minutes.\n");
  await put(board, "exports/events.csv", 0, "id,type\n1,charge.succeeded\n2,charge.failed\n");
  await put(board, "preview/landing.html", 0, "<h1>Landing</h1>");
  for (const theme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.getByRole("tab", { name: /^Files/ }).click();
    const work = page.getByRole("button", { name: "Work", exact: true });
    if (await work.isVisible()) await work.click();
    await expect(page.locator('[data-file="status.md"]')).toBeVisible();
    await page.screenshot({ animations: "disabled", path: join(dir!, `files-list-${theme}.png`) });
    const held = await drag(page, page.locator(".files-view"), "draft.md", "x");
    await expect(page.locator(".drop-hint")).toBeVisible();
    await page.screenshot({ animations: "disabled", path: join(dir!, `files-drop-${theme}.png`) });
    await held.leave();
    await page.locator('[data-file="status.md"]').getByRole("button", { name: "status.md" }).click();
    await expect(page.getByLabel("Preview of status.md, v2")).toContainText("Retries back off");
    await page.mouse.move(0, 0);
    await page.screenshot({ animations: "disabled", path: join(dir!, `files-panel-${theme}.png`) });
    await page.setViewportSize({ width: 390, height: 844 });
    await page.getByRole("region", { name: "File status.md" }).scrollIntoViewIfNeeded();
    await page.screenshot({ animations: "disabled", path: join(dir!, `files-panel-phone-${theme}.png`) });
    await page.getByRole("tab", { name: /^Files/ }).scrollIntoViewIfNeeded();
    await page.screenshot({ animations: "disabled", path: join(dir!, `files-list-phone-${theme}.png`) });
  }
});
