// Checks every colour scheme against WCAG AA: the text pairs at 4.5:1, text on the
// accent and on the soft attention tone, the marks, edges and focus at 3:1, every agent
// status at 3:1 on every surface, and every identity colour's initial on its fill. It
// reads the real UI's light and dark (app/tokens.css) and its schemes (app/schemes.css).
// Run: node lab/contrast.mjs; it exits 1 on a failure.

import { readFileSync } from "node:fs";

const here = (p) => new URL(p, import.meta.url).pathname;
const tokens = readFileSync(here("../app/tokens.css"), "utf8");
const schemes = readFileSync(here("../app/schemes.css"), "utf8");

function block(css, selector) {
  const i = css.indexOf(selector);
  if (i < 0) throw new Error(`no ${selector}`);
  const body = css.slice(css.indexOf("{", i) + 1, css.indexOf("}", i));
  const raw = Object.fromEntries([...body.matchAll(/--([\w-]+):\s*([^;]+);/g)].map((m) => [m[1], m[2].trim()]));
  return raw;
}

// A scheme's values over the light base, with var() references followed.
function resolve(over) {
  const base = block(tokens, ":root {");
  const all = { ...base, ...over };
  const value = (name, seen = 0) => {
    const v = all[name];
    if (v === undefined || seen > 8) throw new Error(`no --${name}`);
    const ref = v.match(/^var\(--([\w-]+)\)$/);
    return ref ? value(ref[1], seen + 1) : v;
  };
  const out = {};
  for (const k of Object.keys(all)) {
    const v = value(k);
    if (/^#[0-9a-f]{6}$/i.test(v)) out[k] = v;
  }
  // The soft attention tone: the accent at --attention-mix over the surface (tokens.css).
  out["attention-soft"] = mix(out.accent, out.surface, parseFloat(value("attention-mix")) / 100);
  return out;
}

function mix(a, b, p) {
  const ch = (h, i) => parseInt(h.slice(i, i + 2), 16);
  return "#" + [1, 3, 5].map((i) => Math.round(ch(a, i) * p + ch(b, i) * (1 - p)).toString(16).padStart(2, "0")).join("");
}

const themes = {
  light: resolve({}),
  dark: resolve(block(tokens, ':root[data-theme="dark"]')),
  ember: resolve(block(schemes, ':root[data-theme="ember"]')),
  tide: resolve(block(schemes, ':root[data-theme="tide"]')),
  contrast: resolve(block(schemes, ':root[data-theme="contrast"]')),
};

const lum = (hex) => {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a, b) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

const surfaces = ["surface", "sidebar", "background", "selected"];
const pairs = [
  ...surfaces.map((bg) => ["ink", bg, 4.5]),
  ...surfaces.map((bg) => ["muted", bg, 4.5]),
  ["link", "surface", 4.5],
  ["link", "background", 4.5],
  ["on-ink", "ink", 4.5],
  // What waits on the person: a label on the accent, a row on its soft tone.
  ["on-accent", "accent", 4.5],
  ["ink", "attention-soft", 4.5],
  ["muted", "attention-soft", 4.5],
  // The accent for lines, check marks and the focus ring.
  ...surfaces.map((bg) => ["accent-strong", bg, 3]),
  ["field-border", "surface", 3],
  ["field-border", "background", 3],
];
// An agent's status mark sits on every surface a member list or a task card uses; on a
// row in the attention tone it sits in a ring of the surface.
for (const status of ["working", "needs", "hold", "idle"]) {
  for (const bg of surfaces) pairs.push([`status-${status}`, bg, 3]);
}

const lowest = (t, test) => Math.min(...pairs.filter(([fg]) => test(fg)).map(([fg, bg]) => ratio(t[fg], t[bg])));

let failed = 0;
const rows = [];
for (const [name, t] of Object.entries(themes)) {
  const worst = [];
  for (const [fg, bg, min] of pairs) {
    const r = ratio(t[fg], t[bg]);
    if (r < min) {
      failed++;
      worst.push(`${fg} on ${bg} ${r.toFixed(2)} < ${min}`);
    }
  }
  let idMin = Infinity;
  for (let i = 1; i <= 8; i++) {
    const r = ratio(t[`id-${i}-fg`], t[`id-${i}-bg`]);
    idMin = Math.min(idMin, r);
    if (r < 4.5) {
      failed++;
      worst.push(`id-${i} ${r.toFixed(2)} < 4.5`);
    }
  }
  rows.push(
    `| ${name} | ${ratio(t.ink, t.surface).toFixed(1)} | ${ratio(t.muted, t.surface).toFixed(1)} | ${ratio(t["on-accent"], t.accent).toFixed(1)} | ${ratio(t.ink, t["attention-soft"]).toFixed(1)} | ${lowest(t, (f) => f === "accent-strong").toFixed(1)} | ${ratio(t["field-border"], t.surface).toFixed(1)} | ${idMin.toFixed(1)} | ${lowest(t, (f) => f.startsWith("status-")).toFixed(1)} | ${worst.length ? worst.join("; ") : "all pass"} |`,
  );
}
console.log("| Scheme | ink/surface | muted/surface | on-accent/accent | ink/attention-soft | accent-strong (lowest) | field edge | identity (lowest) | status (lowest) | AA |");
console.log("| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |");
for (const r of rows) console.log(r);
process.exit(failed ? 1 : 0);
