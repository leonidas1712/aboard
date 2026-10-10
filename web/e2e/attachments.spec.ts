import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Locator, type Page, expect, test } from "@playwright/test";

// Files in the conversation, against a real server in an isolated home: an agent's
// `aboard say --attach` shows as file cards that open the file panel at the attached
// version, the person attaches board files or uploads from the message box, and a board
// file's path in a message's text links to the file.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-attachments-web-"));
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

let violations: string[] = [];
test.beforeEach(({ page }) => {
  violations = [];
  page.on("console", (m) => {
    if (m.text().includes("Content Security Policy")) violations.push(m.text());
  });
});
test.afterEach(() => {
  expect(violations).toEqual([]);
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
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}

type Put = { id: string; name: string; latest: { version: number } };

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

type Posted = { id: string; files?: { id: string; name: string; version: number }[] };

// agentSays posts a message as the board's agent, through the public API.
async function agentSays(board: string, body: Record<string, unknown>): Promise<Posted> {
  const r = await fetch(`${base()}/v1/boards/${board}/messages`, {
    method: "POST",
    headers: { Authorization: `Bearer ${seatToken(board)}`, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() },
    body: JSON.stringify({ to: ["all"], ...body }),
  });
  if (!r.ok) throw new Error(`say: ${r.status} ${await r.text()}`);
  return (await r.json()) as Posted;
}

// lastMessage reads the board's newest message as the person, as the API stores it.
async function lastMessage(board: string): Promise<Posted & { body: string }> {
  const r = await fetch(`${base()}/v1/boards/${board}/messages?limit=1`, { headers: { Authorization: `Bearer ${ownerToken()}` } });
  const page = (await r.json()) as { messages: (Posted & { body: string })[] };
  return page.messages.at(-1)!;
}

function local(name: string, body: string | Buffer): string {
  const path = join(home, "work", name);
  mkdirSync(join(home, "work"), { recursive: true });
  writeFileSync(path, body);
  return path;
}

function message(page: Page, text: string): Locator {
  return page.getByRole("log", { name: "Timeline" }).locator(".message").filter({ hasText: text });
}

// A 2x2 PNG with four coloured pixels.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8DAwMDAxMDAwMDAAAANBAEB3LmtjQAAAABJRU5ErkJggg==",
  "base64",
);

test("an agent's say --attach shows as file cards that open the file panel at the attached version", async ({ page }) => {
  const board = "attach-cards";
  await openBoard(page, board);

  // One file: a card with its name, version, type, size and who wrote that version.
  aboard("say", "--as", "writer", "--to", "all", "--attach", local("report.md", "# Report\n\nFirst draft.\n"), "The first draft is up.");
  const single = message(page, "The first draft is up.");
  const card = single.locator('[data-attachment="report.md"]');
  await expect(single.getByRole("list", { name: "Attached file" })).toBeVisible();
  await expect(card).toContainText("v1");
  await expect(card).toContainText("Markdown");
  await expect(card).toContainText("23 bytes");
  await expect(card).toContainText("writer");
  // A card is a file, never a person: no mention, no @.
  await expect(card).not.toContainText("@");
  await expect(single.locator(".mention")).toHaveCount(0);

  // Several files in one message, each its own card; an image shows its exact version as a thumbnail.
  aboard(
    "say", "--as", "writer", "--to", "all",
    "--attach", local("chart.png", png),
    "--attach", local("data.csv", "id,value\n1,10\n"),
    "--attach", local("report.md", "# Report\n\nSecond draft, with the chart.\n"),
    "The chart, its data and the second draft.",
  );
  const several = message(page, "The chart, its data and the second draft.");
  await expect(several.getByRole("list", { name: "3 attached files" }).getByRole("listitem")).toHaveCount(3);
  const thumb = several.locator('[data-attachment="chart.png"] img');
  await expect.poll(() => thumb.evaluate((i: HTMLImageElement) => i.naturalWidth)).toBe(2);
  await expect(several.locator('[data-attachment="report.md"]')).toContainText("v2");
  // The first message keeps its version, and says a newer one exists.
  await expect(card).toContainText("v1 of 2");

  // The card opens the panel at the attached version, not the latest.
  await card.getByRole("button", { name: "Open report.md, v1" }).click();
  const panel = page.getByRole("region", { name: "File report.md" });
  await expect(panel).toContainText("Showing v1, an older version.");
  await expect(panel.getByLabel("Preview of report.md, v1")).toContainText("First draft.");
  await several.locator('[data-attachment="report.md"]').getByRole("button", { name: "Open report.md, v2" }).click();
  await expect(panel.getByLabel("Preview of report.md, v2")).toContainText("Second draft");
  await expect(panel).not.toContainText("an older version");

  // The card downloads its version's exact bytes.
  const [download] = await Promise.all([page.waitForEvent("download"), card.getByRole("link", { name: "Download report.md, v1" }).click()]);
  expect(download.suggestedFilename()).toBe("report.md");
  expect(readFileSync((await download.path())!, "utf8")).toBe("# Report\n\nFirst draft.\n");

  // From the keyboard: the card's name is a button, and Enter opens it.
  await several.locator('[data-attachment="data.csv"]').getByRole("button", { name: "Open data.csv, v1" }).focus();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("region", { name: "File data.csv" })).toBeVisible();
});

test("a board file's path in a message links to the file, at the version written beside it", async ({ page }) => {
  const board = "attach-links";
  await openBoard(page, board);
  await put(board, "notes/api.md", 0, "# API\n\nTwice.\n");
  await put(board, "notes/api.md", 1, "# API\n\nThree times.\n");
  const plan = await put(board, "plan.md", 0, "# Plan\n");
  await agentSays(board, { body: "Read notes/api.md v1 against the latest notes/api.md, and see plan.md. Not notes/api.mdx or docs/notes/api.md." });
  const said = message(page, "Read notes/api.md v1");
  const links = said.locator("[data-file-link]");
  await expect(links).toHaveCount(3);
  await expect(links.nth(0)).toHaveText("notes/api.md v1");
  await expect(links.nth(1)).toHaveText("notes/api.md");
  await expect(links.nth(2)).toHaveText("plan.md");
  // The text is unchanged: the links are drawn over it.
  await expect(said.locator(".body")).toHaveText("Read notes/api.md v1 against the latest notes/api.md, and see plan.md. Not notes/api.mdx or docs/notes/api.md.");

  await said.getByRole("button", { name: "Open file notes/api.md, v1" }).click();
  const panel = page.getByRole("region", { name: "File notes/api.md" });
  await expect(panel.getByLabel("Preview of notes/api.md, v1")).toContainText("Twice.");
  await said.getByRole("button", { name: "Open file notes/api.md", exact: true }).click();
  await expect(panel.getByLabel("Preview of notes/api.md, v2")).toContainText("Three times.");

  // A file the message also attaches has its card, and its path stays plain text.
  await agentSays(board, { body: "Updated plan.md as asked.", files: [{ file: plan.id, version: 1 }] });
  const attached = message(page, "Updated plan.md as asked.");
  await expect(attached.locator('[data-attachment="plan.md"]')).toBeVisible();
  await expect(attached.locator("[data-file-link]")).toHaveCount(0);
});

test("the person attaches a board file at a chosen version from the message box", async ({ page }) => {
  const board = "attach-compose";
  await openBoard(page, board);
  await put(board, "notes/api.md", 0, "# API\n\nv1\n");
  await put(board, "notes/api.md", 1, "# API\n\nv2\n");
  await put(board, "plan.md", 0, "# Plan\n");
  await expect(page.getByRole("tab", { name: "Files 2" })).toBeVisible();

  const box = page.getByRole("form", { name: "Post a message" });
  await box.getByRole("button", { name: "Attach files" }).click();
  const picker = page.getByRole("dialog", { name: "Attach files" });
  await picker.getByRole("combobox", { name: "Find a file on this board" }).fill("api");
  await expect(picker.getByRole("option")).toHaveCount(1);
  // From the keyboard: Enter attaches the highlighted file, Escape closes the list.
  await page.keyboard.press("Enter");
  await expect(picker.getByRole("option", { name: /notes\/api\.md/ })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("Escape");
  await expect(picker).toBeHidden();
  await expect(box.getByRole("button", { name: /^Attach files, 1 file attached/ })).toBeFocused();

  const chips = box.getByRole("list", { name: "Files to attach" });
  const chip = chips.locator('[data-draft="notes/api.md"]');
  await expect(chip).toBeVisible();
  // The chip attaches the latest version unless the person picks another.
  await chip.getByLabel("Version of notes/api.md to attach").selectOption("1");
  // Post waits for a line about the file.
  await expect(box.getByRole("button", { name: "Post" })).toBeDisabled();
  await expect(box).toContainText("Write a line about the file to post.");
  await page.getByRole("combobox", { name: /^Message/ }).fill("The API notes as they were on Monday.");
  await box.getByRole("button", { name: "Post" }).click();
  await expect(chips).toBeHidden();
  const posted = message(page, "The API notes as they were on Monday.");
  await expect(posted.locator('[data-attachment="notes/api.md"]')).toContainText("v1 of 2");
  const stored = await lastMessage(board);
  expect(stored.files).toEqual([expect.objectContaining({ name: "notes/api.md", version: 1 })]);

  // A chip comes off the message with its ×, and the file stays on the board.
  await box.getByRole("button", { name: "Attach files" }).click();
  await picker.getByRole("option", { name: /plan\.md/ }).click();
  await page.keyboard.press("Escape");
  await chips.getByRole("button", { name: "Remove plan.md from the message" }).click();
  await expect(chips).toBeHidden();
  await expect(page.getByRole("tab", { name: "Files 2" })).toBeVisible();
});

test("the person uploads new files from the message box, by the chooser and by a drop, and a taken name waits for their choice", async ({ page }) => {
  const board = "attach-upload";
  await openBoard(page, board);
  await put(board, "notes.md", 0, "# Notes by the writer\n");
  await expect(page.getByRole("tab", { name: "Files 1" })).toBeVisible();
  const box = page.getByRole("form", { name: "Post a message" });
  const chips = box.getByRole("list", { name: "Files to attach" });

  // A new file goes on the board as soon as it is picked, as v1.
  await box.getByRole("button", { name: "Attach files" }).click();
  const chooser = page.waitForEvent("filechooser");
  await page.getByRole("dialog", { name: "Attach files" }).getByRole("button", { name: "Upload from this computer" }).click();
  await (await chooser).setFiles([
    { name: "draft plan.md", mimeType: "text/markdown", buffer: Buffer.from("# Plan\n") },
    { name: "notes.md", mimeType: "text/markdown", buffer: Buffer.from("# Notes by alex\n") },
  ]);
  await expect(chips.locator('[data-draft="draft-plan.md"]')).toContainText("v1");
  await expect(page.getByRole("tab", { name: "Files 2" })).toBeVisible();

  // notes.md is taken: nothing is uploaded until the person chooses, and Post waits.
  const taken = chips.locator('[data-draft="notes.md"]');
  await expect(taken).toContainText("notes.md is already on the board, at v1 by writer.");
  await page.getByRole("combobox", { name: /^Message/ }).fill("My plan, and my take on the notes.");
  await expect(box.getByRole("button", { name: "Post" })).toBeDisabled();
  await taken.getByRole("button", { name: "Upload as v2" }).click();
  await expect(taken).toContainText("v2");

  // A file dropped on the message box attaches too; one at a taken name can sit beside it.
  const data = await page.evaluateHandle(() => {
    const t = new DataTransfer();
    t.items.add(new File(["# Notes, dropped\n"], "notes.md", { type: "text/markdown" }));
    return t;
  });
  await box.dispatchEvent("dragenter", { dataTransfer: data });
  await expect(box.locator(".drop-hint")).toContainText("Drop to attach to your message");
  await box.dispatchEvent("drop", { dataTransfer: data });
  const dropped = chips.locator('[data-draft="notes.md"]').filter({ hasText: "already on the board" });
  await dropped.getByRole("button", { name: "Keep both" }).click();
  await expect(chips.locator('[data-draft="notes-2.md"]')).toContainText("v1");

  await box.getByRole("button", { name: "Post" }).click();
  const posted = message(page, "My plan, and my take on the notes.");
  await expect(posted.locator("[data-attachment]")).toHaveCount(3);
  await expect(posted.locator('[data-attachment="notes.md"]')).toContainText("v2");
  await expect(posted.locator('[data-attachment="notes.md"]')).toContainText("you");
  const stored = await lastMessage(board);
  expect(stored.files?.map((f) => `${f.name}@${f.version}`)).toEqual(["draft-plan.md@1", "notes.md@2", "notes-2.md@1"]);
});

test("an ask's attachments show in the Inbox and open the file on its board", async ({ page }) => {
  const board = "attach-inbox";
  await openBoard(page, board);
  const spec = await put(board, "spec/webhooks.md", 0, "# Webhooks\n\nSigned and retried.\n");
  await agentSays(board, { body: "Ship the webhook spec?", to: ["@alex"], ask: { options: ["Ship it", "Hold it"] }, files: [{ file: spec.id, version: 1 }] });
  await page.getByRole("link", { name: /^Inbox/ }).click();
  await page.getByRole("group", { name: "Needs you asks" }).getByRole("button", { name: /Ship the webhook spec/ }).click();
  const card = page.getByRole("region", { name: "The selected ask" }).locator('[data-attachment="spec/webhooks.md"]');
  await expect(card).toContainText("v1");
  await card.getByRole("link", { name: "Open spec/webhooks.md, v1 on its board" }).click();
  await expect(page.getByRole("region", { name: "File spec/webhooks.md" }).getByLabel("Preview of spec/webhooks.md, v1")).toContainText("Signed and retried.");
});

test("screenshots of attachments in the conversation and the message box", async ({ page }) => {
  const dir = process.env.ATTACHMENT_SHOTS;
  test.skip(!dir, "Set ATTACHMENT_SHOTS to a folder to take screenshots.");
  mkdirSync(dir!, { recursive: true });
  const board = "attach-shots";
  await openBoard(page, board);
  await put(board, "notes/retry-policy.md", 0, "# Retry policy\n\nBack off exponentially.\n");
  await put(board, "notes/retry-policy.md", 1, "# Retry policy\n\nBack off exponentially, up to ten minutes.\n");
  aboard(
    "say", "--as", "writer", "--to", "all",
    "--attach", local("checkout-flow.png", png),
    "--attach", local("load-test.csv", "run,p95_ms,errors\n1,410,0\n2,398,0\n3,405,0\n"),
    "--attach", local("webhooks-v2.md", "# Webhooks v2\n\nSigned, retried, logged.\n"),
    "Ready for review: the new checkout flow, last night's load test and the webhook spec. The retry rules are in notes/retry-policy.md v1, now superseded.",
  );
  aboard("say", "--as", "writer", "--to", "all", "@alex start with webhooks-v2.md; notes/retry-policy.md is the current policy.");
  await expect(message(page, "Ready for review").locator("[data-attachment]")).toHaveCount(3);
  for (const [theme, width, height] of [["light", 1440, 900], ["dark", 1440, 900], ["light", 390, 844], ["dark", 390, 844]] as const) {
    await page.emulateMedia({ colorScheme: theme });
    await page.setViewportSize({ width, height });
    const box = page.getByRole("form", { name: "Post a message" });
    await box.getByRole("button", { name: /^Attach files/ }).click();
    const picker = page.getByRole("dialog", { name: "Attach files" });
    const option = picker.getByRole("option", { name: /retry-policy/ });
    if ((await option.getAttribute("aria-selected")) !== "true") await option.click();
    const phone = width < 600 ? "-phone" : "";
    await page.mouse.move(0, 0);
    await page.screenshot({ animations: "disabled", path: join(dir!, `attach-picker${phone}-${theme}.png`) });
    await page.getByRole("combobox", { name: /^Message/ }).fill("Here's the policy as it stood.");
    await page.screenshot({ animations: "disabled", path: join(dir!, `attach-chips${phone}-${theme}.png`) });
    // Moving to the text closes the list.
    await expect(picker).toBeHidden();
    await message(page, "Ready for review").locator(".attachments").evaluate((el) => el.scrollIntoView({ block: "center" }));
    await page.screenshot({ animations: "disabled", path: join(dir!, `attachments${phone}-${theme}.png`) });
  }
});
