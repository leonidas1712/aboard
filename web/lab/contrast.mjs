// Checks every colour scheme against WCAG AA: the text pairs at 4.5:1, the marks, edges
// and focus at 3:1, marigold attention with its ink, and every identity colour's
// initial on its fill. It reads the real UI's light and dark (app/globals.css) and the
// lab's schemes (lab/themes.css). Run: node lab/contrast.mjs; it exits 1 on a failure.

import { readFileSync } from "node:fs";

const here = (p) => new URL(p, import.meta.url).pathname;
const globals = readFileSync(here("../app/globals.css"), "utf8");
const lab = readFileSync(here("./themes.css"), "utf8");

function block(css, selector) {
  const i = css.indexOf(selector);
  if (i < 0) throw new Error(`no ${selector}`);
  const body = css.slice(css.indexOf("{", i) + 1, css.indexOf("}", i));
  return Object.fromEntries([...body.matchAll(/--([\w-]+):\s*(#[0-9a-f]{6})/gi)].map((m) => [m[1], m[2]]));
}

const themes = {
  light: block(globals, ":root {"),
  dark: block(globals, ':root[data-theme="dark"]'),
  ember: block(lab, ':root[data-theme="ember"]'),
  tide: block(lab, ':root[data-theme="tide"]'),
  contrast: block(lab, ':root[data-theme="contrast"]'),
};

const lum = (hex) => {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
};
const ratio = (a, b) => {
  const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
};

const pairs = [
  ["ink", "background", 4.5],
  ["ink", "surface", 4.5],
  ["ink", "sidebar", 4.5],
  ["muted", "surface", 4.5],
  ["muted", "sidebar", 4.5],
  ["muted", "background", 4.5],
  ["link", "surface", 4.5],
  ["link", "background", 4.5],
  ["ink", "attention", 4.5],
  ["ink", "selected", 4.5],
  ["on-ink", "ink", 4.5],
  ["accent", "surface", 3],
  ["field-border", "surface", 3],
];

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
    `| ${name} | ${ratio(t.ink, t.surface).toFixed(1)} | ${ratio(t.muted, t.surface).toFixed(1)} | ${ratio(t.link, t.surface).toFixed(1)} | ${ratio(t.ink, t.attention).toFixed(1)} | ${ratio(t.accent, t.surface).toFixed(1)} | ${idMin.toFixed(1)} | ${worst.length ? worst.join("; ") : "all pass"} |`,
  );
}
console.log("| Scheme | ink/surface | muted/surface | link/surface | ink/attention | accent/surface | identity (lowest) | AA |");
console.log("| --- | --- | --- | --- | --- | --- | --- | --- |");
for (const r of rows) console.log(r);
process.exit(failed ? 1 : 0);
