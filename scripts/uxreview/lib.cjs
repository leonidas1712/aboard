// The plumbing of scripts/ux-review that needs no browser: reading the people to sign
// in as, naming steps and writing the summary. The browser walk is in walk.cjs.
"use strict";

const fs = require("node:fs");
const path = require("node:path");
const { execFileSync } = require("node:child_process");

const VIEWPORTS = {
  desktop: { width: 1440, height: 900 },
  phone: { width: 390, height: 844 },
};

const USAGE = `usage: scripts/ux-review --eval DIR [--out DIR] [--people leo,maya] [--viewports desktop,phone] [--headed]
       scripts/ux-review --sandbox-team TEAM --people leo,maya [--out DIR]
       scripts/ux-review --url BASE --signin NAME=FILE ... [--out DIR]

Signs in to the board view as each person, walks the Inbox, every board, the account
menu and People at desktop (1440x900) and phone (390x844) sizes, and saves a
screenshot and the visible text of each step under OUT/ux/<person>/<viewport>/. It
also tries the keyboard shortcuts and writes OUT/ux/summary.json.

--eval DIR          A make flow-eval KEEP=1 output folder (its <person>-browser.json files).
--sandbox-team TEAM A make sandbox-team team; signs in through each person's sandbox.
--url BASE          The board view's address; give each person with --signin NAME=FILE.
--out DIR           Where ux/ goes. Default: the --eval folder, else a new /tmp folder.`;

/** parseArgs reads the command line into options, or throws an Error with the usage. */
function parseArgs(argv) {
  const opts = { people: null, viewports: ["desktop", "phone"], signins: {}, headed: false };
  const need = (i) => {
    if (i + 1 >= argv.length) throw new Error(`${argv[i]} needs a value.\n\n${USAGE}`);
    return argv[i + 1];
  };
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    switch (a) {
      case "--eval": opts.eval = need(i); i++; break;
      case "--sandbox-team": opts.team = need(i); i++; break;
      case "--url": opts.url = need(i).replace(/\/+$/, ""); i++; break;
      case "--out": opts.out = need(i); i++; break;
      case "--people": opts.people = need(i).split(",").map((s) => s.trim()).filter(Boolean); i++; break;
      case "--viewports": opts.viewports = need(i).split(",").map((s) => s.trim()).filter(Boolean); i++; break;
      case "--signin": {
        const v = need(i); i++;
        const eq = v.indexOf("=");
        if (eq < 1) throw new Error(`--signin takes NAME=FILE, not ${v}.\n\n${USAGE}`);
        opts.signins[v.slice(0, eq)] = v.slice(eq + 1);
        break;
      }
      case "--headed": opts.headed = true; break;
      case "-h": case "--help": opts.help = true; break;
      default: throw new Error(`Unknown argument ${a}.\n\n${USAGE}`);
    }
  }
  if (opts.help) return opts;
  const sources = [opts.eval, opts.team, opts.url].filter(Boolean).length;
  if (sources !== 1) throw new Error(`Give exactly one of --eval, --sandbox-team or --url.\n\n${USAGE}`);
  for (const v of opts.viewports) {
    if (!VIEWPORTS[v]) throw new Error(`Unknown viewport ${v}; use desktop or phone.`);
  }
  if (opts.team && !opts.people) throw new Error("--sandbox-team needs --people, for example --people leo,maya.");
  if (opts.url && Object.keys(opts.signins).length === 0) throw new Error("--url needs --signin NAME=FILE for each person.");
  return opts;
}

/**
 * readSignin turns a sign-in file's text into how to sign in:
 * - {kind:"state", state}: a Playwright storage state (cookies and origins);
 * - {kind:"command", argv}: a command that prints a fresh one-time link, as
 *   `aboard open --json` does, or the link alone;
 * - {kind:"link", url}: a one-time sign-in link, used once.
 */
function readSignin(text) {
  const t = text.trim();
  if (t.startsWith("{")) {
    const v = JSON.parse(t);
    if (Array.isArray(v.cookies) || Array.isArray(v.origins)) return { kind: "state", state: v };
    if (Array.isArray(v.command) && v.command.length > 0) return { kind: "command", argv: v.command.map(String) };
    if (typeof v.url === "string") return { kind: "link", url: v.url };
    throw new Error("A sign-in file in JSON needs cookies (a storage state), command (an argv list) or url.");
  }
  if (/^https?:\/\//.test(t)) return { kind: "link", url: t.split(/\s+/)[0] };
  throw new Error("A sign-in file holds a one-time link, a storage state or {\"command\": [...]}.");
}

/** linkFromOutput finds the sign-in link in a command's output: JSON with url, or a bare link. */
function linkFromOutput(out) {
  const t = out.trim();
  // A sandbox prints its banner first, so the JSON starts on the first line that opens with {.
  const start = t.search(/^\{/m);
  if (start >= 0) {
    try {
      const v = JSON.parse(t.slice(start));
      if (typeof v.url === "string") return v.url;
    } catch { /* fall through to a bare link */ }
  }
  const m = t.match(/https?:\/\/\S+#\S+/);
  if (m) return m[0];
  throw new Error("The sign-in command printed no link.");
}

/** checkPrivate refuses a sign-in file that others on the machine can read. */
function checkPrivate(file) {
  const mode = fs.statSync(file).mode & 0o777;
  if (mode & 0o077) throw new Error(`${file} is readable by others (mode ${mode.toString(8)}); chmod 600 it first.`);
}

/**
 * loadPeople returns [{name, url, signin}] for the chosen source. url is the board
 * view's address to start from; signin is what readSignin returns.
 */
function loadPeople(opts, repo) {
  if (opts.eval) return evalPeople(opts.eval, opts.people, repo);
  if (opts.team) {
    const root = process.env.ABOARD_SANDBOXES || path.join(process.env.HOME || "", ".aboard-sandboxes");
    const urlFile = path.join(root, "teams", opts.team, "url");
    if (!fs.existsSync(urlFile)) throw new Error(`No team ${opts.team} at ${urlFile}. Start it with make sandbox-team TEAM=${opts.team}.`);
    const url = fs.readFileSync(urlFile, "utf8").trim();
    return opts.people.map((name) => ({ name, url, signin: { kind: "command", argv: sandboxOpen(repo, opts.team, name) } }));
  }
  return Object.entries(opts.signins)
    .filter(([name]) => !opts.people || opts.people.includes(name))
    .map(([name, file]) => {
      checkPrivate(file);
      return { name, url: opts.url, signin: readSignin(fs.readFileSync(file, "utf8")) };
    });
}

/**
 * evalPeople reads a make flow-eval KEEP=1 output folder: one <person>-browser.json per
 * person (the output of aboard open --json, private to its owner), with a one-time link
 * that lasts a minute. Each person signs in with {kind:"eval"}, which re-reads the file
 * and refreshes it through flow_eval.py --browser-signin when it has expired or was used.
 * A hand-written ux-people.json ([{name, url, signin}]) in the folder works too.
 */
function evalPeople(dir, only, repo) {
  if (!fs.existsSync(dir)) throw new Error(`${dir} does not exist. Give the folder make flow-eval KEEP=1 printed.`);
  const keep = (name) => !only || only.includes(name);
  const listed = path.join(dir, "ux-people.json");
  if (fs.existsSync(listed)) {
    const doc = JSON.parse(fs.readFileSync(listed, "utf8"));
    const rows = Array.isArray(doc) ? doc : doc.people;
    if (!Array.isArray(rows) || rows.length === 0) throw new Error(`${listed} lists no people.`);
    return rows.filter((p) => keep(p.name)).map((p) => {
      if (!p.name || !p.url || !p.signin) throw new Error(`Each person in ${listed} needs name, url and signin.`);
      const file = path.resolve(dir, p.signin);
      checkPrivate(file);
      return { name: p.name, url: p.url, signin: readSignin(fs.readFileSync(file, "utf8")) };
    });
  }
  const files = fs.readdirSync(dir).filter((f) => f.endsWith("-browser.json")).sort();
  const runtime = fs.existsSync(path.join(dir, "review-runtime.json"));
  if (files.length === 0 && !runtime) throw new Error(`${dir} has no <person>-browser.json. Run make flow-eval KEEP=1, or use --sandbox-team.`);
  let names = files.map((f) => f.slice(0, -"-browser.json".length));
  if (names.length === 0) names = Object.keys(JSON.parse(fs.readFileSync(path.join(dir, "review-runtime.json"), "utf8")).people || {});
  return names.filter(keep).map((name) => {
    const file = path.join(dir, `${name}-browser.json`);
    let url = null;
    if (fs.existsSync(file)) {
      checkPrivate(file);
      const v = JSON.parse(fs.readFileSync(file, "utf8"));
      url = v.ui_url || v.url;
    }
    return { name, url, signin: { kind: "eval", dir, file, refresh: runtime ? ["python3", path.join(repo, "scripts", "flow_eval.py"), "--browser-signin", dir] : null } };
  });
}

/**
 * evalLink returns a usable one-time link from a flow-eval sign-in file, refreshing the
 * files first when force is set, the file is missing, or the link expires within 10s.
 */
function evalLink(signin, force, now = Date.now()) {
  const read = () => (fs.existsSync(signin.file) ? JSON.parse(fs.readFileSync(signin.file, "utf8")) : null);
  let v = read();
  const stale = (x) => !x || !x.url || (x.expires_at && Date.parse(x.expires_at) - now < 10_000);
  if (force || stale(v)) {
    if (!signin.refresh) throw new Error(`${signin.file} has expired and ${signin.dir} has no review-runtime.json to refresh it.`);
    execFileSync(signin.refresh[0], signin.refresh.slice(1), { stdio: ["ignore", "pipe", "pipe"] });
    v = read();
    if (!v || !v.url) throw new Error(`Refreshing gave no sign-in in ${signin.file}; the person may have no account yet.`);
  }
  checkPrivate(signin.file);
  return { url: v.url, ui: v.ui_url || v.url };
}

/** sandboxOpen is the command that prints a fresh sign-in link for a sandbox-team person. */
function sandboxOpen(repo, team, name) {
  return ["env", `TEAM=${team}`, "FRESH=0", "SANDBOX_CMD=BROWSER=true aboard open --json", path.join(repo, "scripts", "sandbox"), "open", name];
}

/** freshLink runs a sign-in command and returns the link it printed. */
function freshLink(argv) {
  const out = execFileSync(argv[0], argv.slice(1), { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], env: { ...process.env, BROWSER: "true" } });
  return linkFromOutput(out);
}

/** slug makes a step name safe for a file name. */
function slug(s) {
  return String(s).toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "step";
}

/** Recorder numbers the steps of one person at one viewport and keeps their files. */
class Recorder {
  constructor(out, person, viewport) {
    this.dir = path.join(out, "ux", person, viewport);
    this.person = person;
    this.viewport = viewport;
    this.steps = [];
    fs.mkdirSync(this.dir, { recursive: true });
  }

  /** next returns the base path (without extension) and the name of the next step. */
  next(name) {
    const step = `${String(this.steps.length + 1).padStart(2, "0")}-${slug(name)}`;
    return { step, base: path.join(this.dir, step) };
  }

  add(entry) {
    this.steps.push(entry);
    return entry;
  }
}

/** summarize builds summary.json's object from the recorders and keyboard results. */
function summarize({ out, people, recorders, keyboard, errors, started }) {
  const rel = (p) => path.relative(out, p);
  return {
    started,
    finished: new Date().toISOString(),
    out,
    people,
    viewports: Object.fromEntries(Object.entries(VIEWPORTS)),
    steps: recorders.flatMap((r) => r.steps.map((s) => ({
      person: r.person,
      viewport: r.viewport,
      step: s.step,
      url: s.url,
      screenshot: s.screenshot ? rel(s.screenshot) : null,
      text: s.text ? rel(s.text) : null,
      overflow_x: s.overflow_x ?? null,
      note: s.note ?? null,
    }))),
    keyboard,
    keyboard_passed: keyboard.filter((k) => k.result === "pass").length,
    keyboard_failed: keyboard.filter((k) => k.result === "fail").length,
    errors,
  };
}

module.exports = { evalPeople, evalLink, VIEWPORTS, USAGE, parseArgs, readSignin, linkFromOutput, checkPrivate, loadPeople, sandboxOpen, freshLink, slug, Recorder, summarize };
