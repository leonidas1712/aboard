// Tests the plumbing of scripts/ux-review that needs no browser. Run with:
// node --test scripts/uxreview/lib.test.cjs
"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const lib = require("./lib.cjs");

function tmp() {
  return fs.mkdtempSync(path.join(os.tmpdir(), "ux-review-test-"));
}

function write(file, text, mode = 0o600) {
  fs.writeFileSync(file, text);
  fs.chmodSync(file, mode);
}

test("arguments need exactly one source", () => {
  assert.throws(() => lib.parseArgs([]), /exactly one/);
  assert.throws(() => lib.parseArgs(["--eval", "a", "--url", "b"]), /exactly one/);
  assert.throws(() => lib.parseArgs(["--sandbox-team", "qa"]), /--people/);
  assert.throws(() => lib.parseArgs(["--eval", "a", "--viewports", "tablet"]), /Unknown viewport/);
  const o = lib.parseArgs(["--sandbox-team", "qa", "--people", "leo, maya", "--viewports", "phone"]);
  assert.deepEqual(o.people, ["leo", "maya"]);
  assert.deepEqual(o.viewports, ["phone"]);
  const u = lib.parseArgs(["--url", "https://x/", "--signin", "leo=/tmp/f"]);
  assert.equal(u.url, "https://x");
  assert.deepEqual(u.signins, { leo: "/tmp/f" });
});

test("a sign-in file is a link, a storage state or a command", () => {
  assert.deepEqual(lib.readSignin("https://h/#t\n"), { kind: "link", url: "https://h/#t" });
  assert.equal(lib.readSignin('{"cookies":[],"origins":[]}').kind, "state");
  assert.deepEqual(lib.readSignin('{"command":["aboard","open","--json"]}'), { kind: "command", argv: ["aboard", "open", "--json"] });
  assert.deepEqual(lib.readSignin('{"url":"https://h/#t","ui_url":"https://h/"}'), { kind: "link", url: "https://h/#t" });
  assert.throws(() => lib.readSignin("hello"), /one-time link/);
});

test("the link is read past a sandbox banner", () => {
  const out = "Reopened sandbox leo\n  Board view: https://127.0.0.1:1\n{\n  \"url\": \"https://127.0.0.1:1/#secret\"\n}\n";
  assert.equal(lib.linkFromOutput(out), "https://127.0.0.1:1/#secret");
  assert.equal(lib.linkFromOutput("https://h/#abc\n"), "https://h/#abc");
  assert.throws(() => lib.linkFromOutput("Board view: https://h/"), /no link/);
});

test("a sign-in file others can read is refused", () => {
  const dir = tmp();
  write(path.join(dir, "leo-browser.json"), '{"url":"https://h/#t"}', 0o644);
  assert.throws(() => lib.evalPeople(dir, null, "/repo"), /readable by others/);
});

test("a flow-eval folder gives one person per browser file, refreshed when stale", () => {
  const dir = tmp();
  const soon = new Date(Date.now() + 60_000).toISOString();
  write(path.join(dir, "leo-browser.json"), JSON.stringify({ url: "https://h/#old", ui_url: "https://h/?board=qa", expires_at: soon }));
  write(path.join(dir, "maya-browser.json"), JSON.stringify({ url: "https://h/#m", ui_url: "https://h/?board=qa", expires_at: soon }));
  write(path.join(dir, "review-runtime.json"), JSON.stringify({ people: { leo: {}, maya: {} } }));
  const people = lib.evalPeople(dir, ["leo"], "/repo");
  assert.equal(people.length, 1);
  assert.equal(people[0].name, "leo");
  assert.equal(people[0].url, "https://h/?board=qa");
  assert.deepEqual(people[0].signin.refresh, ["python3", "/repo/scripts/flow_eval.py", "--browser-signin", dir]);

  // Fresh enough: used as it is.
  assert.equal(lib.evalLink(people[0].signin, false).url, "https://h/#old");

  // Expired: the refresh command writes a new file, which is used.
  const refresher = path.join(dir, "refresh.sh");
  write(refresher, `#!/bin/sh\nprintf '{"url":"https://h/#new","expires_at":"${soon}"}' > '${people[0].signin.file}'\nchmod 600 '${people[0].signin.file}'\n`, 0o700);
  const signin = { ...people[0].signin, refresh: [refresher] };
  assert.equal(lib.evalLink(signin, false, Date.now() + 120_000).url, "https://h/#new");
  write(signin.file, JSON.stringify({ url: "https://h/#used", expires_at: soon }));
  assert.equal(lib.evalLink(signin, true).url, "https://h/#new");
});

test("a folder without browser files says how to make them", () => {
  assert.throws(() => lib.evalPeople(tmp(), null, "/repo"), /flow-eval KEEP=1/);
});

test("steps are numbered per person and viewport and summarized with relative paths", () => {
  const out = tmp();
  const rec = new lib.Recorder(out, "leo", "phone");
  const a = rec.next("Board qa: Tasks / Recent");
  assert.equal(a.step, "01-board-qa-tasks-recent");
  rec.add({ step: a.step, url: "https://h/?board=qa", screenshot: `${a.base}.png`, text: `${a.base}.txt`, overflow_x: 0 });
  assert.equal(rec.next("x").step, "02-x");
  const s = lib.summarize({ out, people: ["leo"], recorders: [rec], keyboard: [{ check: "i", result: "pass" }, { check: "?", result: "fail" }], errors: [], started: "t" });
  assert.equal(s.steps[0].screenshot, path.join("ux", "leo", "phone", "01-board-qa-tasks-recent.png"));
  assert.equal(s.keyboard_passed, 1);
  assert.equal(s.keyboard_failed, 1);
});
