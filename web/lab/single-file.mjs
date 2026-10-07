// Makes the lab's static export (lab-out/, from `ABOARD_LAB=1 next build`) into one
// self-contained page, lab-out/aboard-ui-lab.html: every script, stylesheet, font and
// icon inlined, so it opens from disk or from any host with no server. It also checks
// that the export is the lab's (it carries the lab's marker).

import { existsSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

const out = new URL("../lab-out/", import.meta.url).pathname;
const page = join(out, "index.html");
const local = (href) => join(out, decodeURIComponent(href.split("?")[0].replace(/^\//, "")));
const types = { ".woff2": "font/woff2", ".png": "image/png", ".svg": "image/svg+xml" };
const dataUri = (file) => `data:${types[file.slice(file.lastIndexOf("."))]};base64,${readFileSync(file).toString("base64")}`;

let html = readFileSync(page, "utf8");

// Stylesheets, with the fonts they load.
html = html.replace(/<link rel="stylesheet" href="([^"]+)"[^>]*\/?>/g, (_, href) => {
  const css = readFileSync(local(href), "utf8").replace(/url\("?(\/[^")]+)"?\)/g, (m, u) => (existsSync(local(u)) ? `url("${dataUri(local(u))}")` : m));
  return `<style>${css}</style>`;
});
// Scripts, inline and in order. Each Turbopack chunk names itself by its script's src,
// which an inline script lacks, so it is given its old address as a string instead. A
// script's text never closes the tag it sits in.
// Next.js finds its asset prefix from the same src; an inline script's is empty, which
// means no prefix. Both rewrites are checked below, so a Next.js upgrade that changes
// them fails here rather than making a blank page.
const self = '"object"==typeof document?document.currentScript:void 0';
const prefix = "let{pathname:t}=new URL(e.src)";
let prefixed = 0;
html = html.replace(/<script src="([^"]+)"([^>]*)><\/script>/g, (_, src, attrs) => {
  if (/noModule/i.test(attrs)) return ""; // the polyfills for old browsers
  let js = readFileSync(local(src), "utf8").replace(self, JSON.stringify(src.replace(/^\/_next\//, "")));
  if (js.includes(prefix)) {
    prefixed++;
    js = js.replace(prefix, 'let{pathname:t}=new URL(e.src||"/_next/",location.href)');
  }
  js = js.replace(/<\/script/gi, "<\\/script");
  return `<script${attrs.replace(/\s*async=""/, "")}>${js}</script>`;
});
// Preloads of what is now inline, and icons as data.
html = html.replace(/<link rel="preload"[^>]*\/?>/g, "");
html = html.replace(/<link rel="(icon|apple-touch-icon)" href="([^"]+)"/g, (_, rel, href) => `<link rel="${rel}" href="${dataUri(local(href))}"`);

if (prefixed !== 1) throw new Error(`single-file: found Next.js's asset-prefix lookup ${prefixed} times, not once; update this script`);
const left =[...html.matchAll(/(?:src|href)="(\/_next\/[^"]+)"/g)].map((m) => m[1]);
if (left.length) throw new Error(`single-file: still links to ${left.join(", ")}`);
// Every chunk the build made must now be inline, or the page would load it at run time.
const chunks = readdirSync(join(out, "_next/static/chunks")).filter((f) => f.endsWith(".js"));
const missing = chunks.filter((f) => !readFileSync(page, "utf8").includes(f));
if (missing.length) throw new Error(`single-file: chunks the page may load later: ${missing.join(", ")}`);
if (!chunks.some((f) => readFileSync(join(out, "_next/static/chunks", f), "utf8").includes("aboard-ui-lab"))) throw new Error("single-file: lab-out isn't a lab build (no lab marker)");

const dest = join(out, "aboard-ui-lab.html");
writeFileSync(dest, html);
console.log(`Wrote ${dest} (${Math.round(html.length / 1024)} KB): open it in a browser, no server needed.`);
