// The browser walk of scripts/ux-review: it signs in as each person, visits what a
// person would look at, tries the keyboard shortcuts, and records what it saw. It
// judges nothing itself beyond the keyboard checks; a reviewing agent reads the
// screenshots and text with the checklist in engineering/qa.md.
"use strict";

const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { createRequire } = require("node:module");
const lib = require("./lib.cjs");

const REPO = path.resolve(__dirname, "..", "..");
const DRAFT = "ux-review draft, not sent";

function playwright() {
  try {
    return createRequire(path.join(REPO, "web", "package.json"))("@playwright/test");
  } catch {
    throw new Error("Playwright is missing: run make web (it installs web/node_modules), then npx playwright install chromium in web/.");
  }
}

/** settle waits for the page to finish loading its data and animating. */
async function settle(page) {
  await page.waitForLoadState("domcontentloaded");
  await page.waitForFunction(() => !document.querySelector('[aria-label="Loading"]'), null, { timeout: 10_000 }).catch(() => {});
  // Cards fade in and lists animate; a screenshot mid-animation misleads the reviewer.
  await page.waitForTimeout(500);
}

/** capture saves the screenshot, the visible text and layout facts of the current screen. */
async function capture(page, rec, name, note) {
  const { step, base } = rec.next(name);
  await page.screenshot({ path: `${base}.png` });
  const facts = await page.evaluate(() => {
    const doc = document.documentElement;
    const wide = [];
    const clipped = (el) => {
      for (let p = el.parentElement; p; p = p.parentElement) {
        const o = getComputedStyle(p).overflowX;
        if (o === "auto" || o === "scroll" || o === "hidden" || o === "clip") return true;
      }
      return false;
    };
    for (const el of document.body.querySelectorAll("*")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0 || r.right <= window.innerWidth + 1) continue;
      if (getComputedStyle(el).visibility === "hidden" || clipped(el)) continue;
      wide.push(`${el.tagName.toLowerCase()}${el.className && typeof el.className === "string" ? "." + el.className.trim().split(/\s+/).slice(0, 3).join(".") : ""} (right edge ${Math.round(r.right)}px)`);
      if (wide.length >= 5) break;
    }
    return { text: document.body.innerText, overflow: doc.scrollWidth - doc.clientWidth, wide };
  });
  fs.writeFileSync(`${base}.txt`, facts.text);
  return rec.add({ step, url: page.url(), screenshot: `${base}.png`, text: `${base}.txt`, overflow_x: facts.overflow, note: [note, facts.wide.length ? `wider than the screen: ${facts.wide.join("; ")}` : null].filter(Boolean).join(" · ") || null });
}

/** signIn opens the person's sign-in once and returns the browser state to reuse. */
async function signIn(browser, person) {
  if (person.signin.kind === "state") return person.signin.state;
  if (person.signin.kind === "eval") {
    // A flow-eval link works once and for a minute: on a refusal, refresh and try again.
    try {
      const { url, ui } = lib.evalLink(person.signin, false);
      person.url = person.url || ui;
      return await openLink(browser, url);
    } catch {
      const { url, ui } = lib.evalLink(person.signin, true);
      person.url = person.url || ui;
      return await openLink(browser, url);
    }
  }
  const link = person.signin.kind === "link" ? person.signin.url : lib.freshLink(person.signin.argv);
  return openLink(browser, link);
}

async function openLink(browser, link) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: lib.VIEWPORTS.desktop });
  try {
    const page = await ctx.newPage();
    await page.goto(link);
    const proceed = page.getByRole("button", { name: "Continue", exact: true });
    const account = page.getByRole("button", { name: /^You are / }).first();
    await Promise.race([proceed.waitFor({ timeout: 20_000 }), account.waitFor({ timeout: 20_000 })]);
    if (await proceed.isVisible()) await proceed.click();
    await account.waitFor({ timeout: 20_000 });
    return await ctx.storageState();
  } finally {
    await ctx.close();
  }
}

/** boardsOf reads the board names from the sidebar or, on a phone, the Boards sheet. */
async function boardsOf(page, origin) {
  await page.goto(`${origin}/?inbox`);
  await settle(page);
  const read = () => page.evaluate(() => [...new Set([...document.querySelectorAll('aside a[href^="/?board="], nav a[href^="/?board="], [role=dialog] a[href^="/?board="]')]
    .map((a) => new URLSearchParams(a.getAttribute("href").slice(1)))
    .filter((p) => [...p.keys()].length === 1)
    .map((p) => p.get("board")))]);
  let boards = await read();
  if (boards.length === 0) {
    const open = page.getByRole("button", { name: "Boards", exact: true });
    if (await open.count()) {
      await open.first().click();
      await settle(page);
      boards = await read();
      await page.keyboard.press("Escape");
    }
  }
  return boards;
}

/** cardKind names the Inbox card on screen by its data attribute. */
function cardKind(page) {
  return page.evaluate(() => {
    for (const k of ["approval", "notice", "arrival", "board-add", "pairing"]) {
      if (document.querySelector(`[data-${k}]`)) return k;
    }
    return "ask";
  });
}

async function walkInbox(page, origin, rec) {
  await page.goto(`${origin}/?inbox`);
  await settle(page);
  await capture(page, rec, "inbox");
  const n = Math.min(await page.locator("[data-item]").count(), 12);
  for (let i = 0; i < n; i++) {
    await page.goto(`${origin}/?inbox`);
    await settle(page);
    const item = page.locator("[data-item]").nth(i);
    const id = await item.getAttribute("data-item");
    await item.click();
    await settle(page);
    await capture(page, rec, `inbox-${await cardKind(page)}-${i + 1}`, `Inbox item ${id}`);
  }
}

async function walkBoard(page, origin, rec, board) {
  await page.goto(`${origin}/?board=${encodeURIComponent(board)}`);
  await settle(page);
  await capture(page, rec, `board-${board}-conversation`);

  const thread = page.locator('button.thread-toggle[aria-expanded="false"]').first();
  if (await thread.count()) {
    await thread.scrollIntoViewIfNeeded();
    await thread.click();
    await settle(page);
    await capture(page, rec, `board-${board}-thread`);
  }

  const tasks = page.getByRole("tab", { name: /^Tasks/ });
  if (await tasks.count()) {
    await tasks.click();
    await settle(page);
    const recent = page.getByRole("button", { name: "Recent", exact: true });
    const status = page.getByRole("button", { name: "By status", exact: true });
    if (await recent.count()) {
      await recent.click();
      await settle(page);
      await capture(page, rec, `board-${board}-tasks-recent`);
      await status.click();
      await settle(page);
      await capture(page, rec, `board-${board}-tasks-by-status`);
      await recent.click();
    } else {
      await capture(page, rec, `board-${board}-tasks`, "no Recent or By status switch");
    }
  }

  const files = page.getByRole("tab", { name: /^Files/ });
  if (await files.count()) {
    await files.click();
    await settle(page);
    await capture(page, rec, `board-${board}-files`);
  }

  const conversation = page.getByRole("tab", { name: "Conversation" });
  if (await conversation.count()) {
    await conversation.click();
    await settle(page);
  }
  const brief = page.locator('section[aria-label="Brief"] button[aria-expanded="false"]').first();
  if (await brief.count()) {
    await brief.click();
    await settle(page);
    await capture(page, rec, `board-${board}-brief`);
  }
}

async function walkAccount(page, origin, rec) {
  await page.goto(`${origin}/?inbox`);
  await settle(page);
  const account = page.getByRole("button", { name: /^You are / }).first();
  if (await account.count()) {
    await account.click();
    await settle(page);
    await capture(page, rec, "account-menu");
    await page.keyboard.press("Escape");
  }
  await page.goto(`${origin}/?view=people`);
  await settle(page);
  await capture(page, rec, "people");
}

/** shown says whether an element is on screen and not faded out. */
function shown(locator) {
  return locator.evaluate((el) => {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) return false;
    let opacity = 1;
    for (let e = el; e; e = e.parentElement) {
      const s = getComputedStyle(e);
      if (s.visibility === "hidden" || s.display === "none") return false;
      opacity *= Number(s.opacity);
    }
    return opacity > 0.1;
  }).catch(() => false);
}

async function blur(page) {
  await page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());
}

function query(page) {
  return new URL(page.url()).search;
}

/** keyboardChecks tries each shortcut on the desktop view and returns one result per check. */
async function keyboardChecks(page, origin, rec, boards, person) {
  const results = [];
  const check = async (name, fn) => {
    try {
      const { pass, detail } = await fn();
      results.push({ person, viewport: "desktop", check: name, result: pass ? "pass" : "fail", detail });
    } catch (e) {
      results.push({ person, viewport: "desktop", check: name, result: "fail", detail: `error: ${e.message.split("\n")[0]}` });
    }
  };
  const a = boards[0];
  const b = boards[1];
  if (!a) return [{ person, viewport: "desktop", check: "all", result: "skipped", detail: "no boards to try the keys on" }];
  const boardURL = (name) => `${origin}/?board=${encodeURIComponent(name)}`;

  await check("inbox-hint-on-hover", async () => {
    await page.goto(boardURL(a));
    await settle(page);
    const hint = page.locator('a[href="/?inbox"] [aria-label^="Shortcut"]').first();
    await page.mouse.move(700, 450);
    await page.waitForTimeout(300);
    const before = (await hint.count()) > 0 && (await shown(hint));
    await page.locator('a[href="/?inbox"]').first().hover();
    await page.waitForTimeout(300);
    const after = (await hint.count()) > 0 && (await shown(hint));
    await capture(page, rec, "keys-inbox-hint-hover");
    return { pass: after, detail: `hint shown before hover: ${before}; on hover: ${after}` };
  });

  await check("i-opens-inbox", async () => {
    await page.goto(boardURL(a));
    await settle(page);
    const focused = await page.evaluate(() => document.activeElement?.tagName.toLowerCase() ?? "none");
    await blur(page);
    await page.keyboard.press("i");
    await page.waitForURL((u) => !u.search.includes("board="), { timeout: 3000 }).catch(() => {});
    const q = query(page);
    return { pass: q.includes("inbox") || q === "", detail: `focus on load: ${focused}; after I: ${q || "/"}` };
  });

  await check("brackets-move-through-sidebar", async () => {
    await page.goto(`${origin}/?inbox`);
    await settle(page);
    await blur(page);
    const seen = [query(page)];
    for (const key of ["]", "]", "["]) {
      const from = page.url();
      await page.keyboard.press(key);
      await page.waitForURL((u) => u.href !== from, { timeout: 3000 }).catch(() => {});
      await settle(page);
      await blur(page);
      seen.push(query(page));
    }
    const [, first, second, back] = seen;
    const moved = first.includes("board=") && (boards.length < 2 || second !== first) && back === first;
    return { pass: moved, detail: `inbox → ] ${first} → ] ${second} → [ ${back}` };
  });

  await check("esc-leaves-composer-keeping-text", async () => {
    await page.goto(boardURL(a));
    await settle(page);
    const box = page.locator('form[aria-label="Post a message"] textarea').first();
    await box.click();
    await box.fill("");
    await box.pressSequentially(DRAFT);
    await page.keyboard.press("Escape");
    const still = await box.evaluate((el) => document.activeElement === el);
    const value = await box.inputValue();
    return { pass: !still && value.includes(DRAFT), detail: `focused after Esc: ${still}; text kept: ${value.includes(DRAFT)}` };
  });

  await check("draft-kept-across-boards", async () => {
    // The draft from the check before is in board a's composer.
    const away = b ? `a[href="/?board=${b}"]` : 'a[href="/?inbox"]';
    await page.locator(away).first().click();
    await settle(page);
    await page.locator(`a[href="/?board=${a}"]`).first().click();
    await settle(page);
    const box = page.locator('form[aria-label="Post a message"] textarea').first();
    const value = await box.inputValue();
    const kept = value.includes(DRAFT);
    await capture(page, rec, "keys-draft-after-return");
    await box.fill("");
    await blur(page);
    return { pass: kept, detail: `went to ${b ?? "the Inbox"} and back to ${a}; draft kept: ${kept}` };
  });

  await check("question-mark-opens-keys", async () => {
    await page.goto(boardURL(a));
    await settle(page);
    await blur(page);
    await page.keyboard.press("?");
    const sheet = page.locator('[aria-labelledby="keys-title"]');
    const open = await sheet.waitFor({ timeout: 3000 }).then(() => true, () => false);
    if (open) {
      await settle(page);
      await capture(page, rec, "keys-sheet");
      await page.keyboard.press("Escape");
    }
    return { pass: open, detail: open ? "the keys sheet opened" : "nothing opened" };
  });

  return results;
}

async function main(argv) {
  let opts;
  try {
    opts = lib.parseArgs(argv);
  } catch (e) {
    console.error(e.message);
    process.exit(2);
  }
  if (opts.help) {
    console.log(lib.USAGE);
    return;
  }
  const out = path.resolve(opts.out || opts.eval || fs.mkdtempSync(path.join(os.tmpdir(), "aboard-ux-")));
  const people = lib.loadPeople(opts, REPO);
  if (people.length === 0) throw new Error("No people to review.");
  const { chromium } = playwright();
  const browser = await chromium.launch({ headless: !opts.headed });
  const started = new Date().toISOString();
  const recorders = [];
  const keyboard = [];
  const errors = [];
  try {
    for (const person of people) {
      let state;
      try {
        state = await signIn(browser, person);
      } catch (e) {
        errors.push({ person: person.name, step: "sign-in", message: e.message.split("\n")[0] });
        continue;
      }
      const origin = new URL(person.url).origin;
      let boards = null;
      for (const viewport of opts.viewports) {
        const rec = new lib.Recorder(out, person.name, viewport);
        recorders.push(rec);
        const ctx = await browser.newContext({ ignoreHTTPSErrors: true, storageState: state, viewport: lib.VIEWPORTS[viewport], hasTouch: viewport === "phone", isMobile: viewport === "phone", colorScheme: "light" });
        const page = await ctx.newPage();
        page.on("pageerror", (e) => errors.push({ person: person.name, viewport, step: "page", message: e.message }));
        const run = async (step, fn) => {
          try {
            await fn();
          } catch (e) {
            errors.push({ person: person.name, viewport, step, message: e.message.split("\n")[0] });
          }
        };
        if (boards === null) await run("boards", async () => { boards = await boardsOf(page, origin); });
        await run("inbox", () => walkInbox(page, origin, rec));
        for (const board of boards ?? []) await run(`board ${board}`, () => walkBoard(page, origin, rec, board));
        await run("account", () => walkAccount(page, origin, rec));
        if (viewport === "desktop") {
          keyboard.push(...await keyboardChecks(page, origin, rec, boards ?? [], person.name));
        }
        await ctx.close();
        console.log(`${person.name} ${viewport}: ${rec.steps.length} steps in ${rec.dir}`);
      }
    }
  } finally {
    await browser.close();
  }
  const summary = lib.summarize({ out, people: people.map((p) => p.name), recorders, keyboard, errors, started });
  fs.mkdirSync(path.join(out, "ux"), { recursive: true });
  fs.writeFileSync(path.join(out, "ux", "summary.json"), JSON.stringify(summary, null, 2) + "\n");
  for (const k of keyboard) console.log(`  ${k.result.padEnd(7)} ${k.person} ${k.check}: ${k.detail}`);
  if (errors.length) console.log(`${errors.length} errors; see summary.json.`);
  console.log(`Wrote ${path.join(out, "ux", "summary.json")}. Review the screenshots with the checklist in engineering/qa.md.`);
}

module.exports = { main };
