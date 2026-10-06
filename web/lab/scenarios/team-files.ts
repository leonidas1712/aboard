// The files of the team scenario (team.ts): the status page, an explainer, a write-up,
// an architecture overview gone stale, and a wireframe a person attached.

import type { Artifact } from "../scenario";

const page = (title: string, body: string) => `<!doctype html><html><head><meta charset="utf-8"><title>${title}</title>
<style>
  body { font: 15px/1.5 system-ui, sans-serif; color: #14212b; background: #fbfcfc; margin: 0; padding: 24px; }
  h1 { font-size: 22px; margin: 0 0 4px; } h2 { font-size: 16px; margin: 20px 0 6px; }
  .muted { color: #4a5a66; font-size: 14px; } table { border-collapse: collapse; width: 100%; }
  td, th { text-align: left; padding: 6px 8px; border-bottom: 1px solid #cbd3d6; font-size: 14px; }
  .ok { color: #255a33; font-weight: 700; } .warn { color: #713e1e; font-weight: 700; }
  code { background: #e1e8eb; padding: 1px 4px; border-radius: 4px; }
</style></head><body>${body}</body></html>`;

// p95 latency at each load test step, drawn as bars.
function chart(points: [string, number][]): string {
  const w = 520;
  const h = 180;
  const max = 600;
  const bw = 70;
  const bars = points
    .map(([label, v], i) => {
      const x = 40 + i * (bw + 30);
      const bh = (v / max) * (h - 40);
      return `<rect x="${x}" y="${h - 20 - bh}" width="${bw}" height="${bh}" rx="4" fill="${v > 500 ? "#b4632f" : "#2f7fa6"}"/>
<text x="${x + bw / 2}" y="${h - 26 - bh}" text-anchor="middle" font-size="13" fill="#14212b">${v} ms</text>
<text x="${x + bw / 2}" y="${h - 4}" text-anchor="middle" font-size="13" fill="#4a5a66">${label}</text>`;
    })
    .join("");
  const limit = h - 20 - (500 / max) * (h - 40);
  return `<svg viewBox="0 0 ${w} ${h}" width="100%" role="img" aria-label="p95 latency by load">
<line x1="30" x2="${w}" y1="${limit}" y2="${limit}" stroke="#713e1e" stroke-dasharray="4 4"/>
<text x="${w - 4}" y="${limit - 6}" text-anchor="end" font-size="12" fill="#713e1e">limit 500 ms</text>${bars}</svg>`;
}

export function status(version: number, points: [string, number][], note: string, t: number): Artifact {
  return {
    id: "status",
    name: "status.html",
    format: "html",
    kind: "artifact",
    maintained: true,
    by: "tester",
    version,
    t,
    summary: "Checkout v2 at a glance: tasks, the load test and the ramp.",
    body: page(
      "Checkout v2 status",
      `<h1>Checkout v2 status</h1><p class="muted">Kept by tester · version ${version}</p>
<h2>Load test, p95 latency</h2>${chart(points)}
<h2>Where things stand</h2><p>${note}</p>
<table><tr><th>Area</th><th>State</th></tr>
<tr><td>Payments on v2 (T1)</td><td class="ok">${version >= 3 ? "In review" : "In progress"}</td></tr>
<tr><td>Refund keys (T5)</td><td class="${version >= 3 ? "warn" : "ok"}">${version >= 3 ? "Waiting on leo" : "In progress"}</td></tr>
<tr><td>Ramp to 10% (T9)</td><td>${version >= 3 ? "16:00 if p95 &lt; 500 ms" : "Not started"}</td></tr></table>`,
    ),
  };
}

export const explainer: Artifact = {
  id: "explainer",
  name: "what-changed-in-payments.html",
  format: "html",
  kind: "artifact",
  by: "claude",
  version: 1,
  t: 135,
  summary: "One-off: what the payments PR changes, for its reviewers.",
  body: page(
    "What changed in payments",
    `<h1>What changed in payments</h1><p class="muted">For the review of the payments PR (22 files) · by claude</p>
<h2>In one line</h2><p><code>createIntent</code> and <code>confirmIntent</code> now call the v2 API; v1 stays behind the <code>checkout_v2</code> flag.</p>
<h2>Read these first</h2><table><tr><th>File</th><th>Why</th></tr>
<tr><td><code>payments/intent.ts</code></td><td>The v2 calls and the new error mapping</td></tr>
<tr><td><code>payments/3ds.ts</code></td><td>3-D Secure now runs on v2's redirect flow</td></tr>
<tr><td><code>payments/errors.ts</code></td><td>Declined cards map to five reasons, not one</td></tr></table>
<h2>Not in this PR</h2><p>Refunds (T5) and the ramp (T9).</p>`,
  ),
};

export const refundKeys: Artifact = {
  id: "refund-keys",
  name: "refund-keys.md",
  format: "md",
  kind: "artifact",
  by: "codex",
  version: 1,
  t: 127,
  summary: "Two ways to key refunds, and what each costs. Waits on leo.",
  body: `# Refund idempotency keys

Refunds need an idempotency key so a retried request never refunds twice (T5).

## Option 1: reuse the payments format
- Keys look like \`pay_<uuid>\`, the same as payments.
- The payments parser needs no change (T1).
- In the logs a refund and a payment look alike.

## Option 2: a new \`ref_\` prefix
- Easy to find in the logs and dashboards.
- About 40 lines more: a second parser and its tests.

## Recommendation
Option 2, unless the ramp on Friday is at risk. **Needs leo's call.**`,
};

export const architecture: Artifact = {
  id: "architecture",
  name: "architecture.md",
  format: "md",
  kind: "artifact",
  maintained: true,
  by: "docs",
  version: 4,
  t: -1900,
  summary: "How checkout fits together: services, flags and data.",
  body: `# Checkout architecture

## Services
- **cart**: totals, discounts and tax.
- **payments**: intents on the v1 API.
- **webhooks**: retries with backoff.

## Flags
- None yet.

*Last updated before the v2 work started.*`,
};

export const wireframe: Artifact = {
  id: "wireframe",
  name: "checkout-wireframe.svg",
  format: "svg",
  kind: "content",
  by: "priya",
  version: 1,
  t: 24,
  summary: "The new checkout layout, from design.",
  body: `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 480 320" font-family="sans-serif">
<rect width="480" height="320" fill="#fbfcfc"/>
<rect x="20" y="20" width="440" height="36" rx="6" fill="#e1e8eb"/><text x="36" y="44" font-size="15" fill="#14212b">Checkout</text>
<rect x="20" y="72" width="270" height="228" rx="8" fill="none" stroke="#7a8a94"/>
<text x="36" y="98" font-size="14" fill="#4a5a66">Card</text><rect x="36" y="108" width="238" height="32" rx="6" fill="#e1e8eb"/>
<text x="36" y="166" font-size="14" fill="#4a5a66">Billing address</text><rect x="36" y="176" width="238" height="32" rx="6" fill="#e1e8eb"/>
<rect x="36" y="244" width="238" height="40" rx="8" fill="#14212b"/><text x="155" y="270" font-size="15" fill="#fbfcfc" text-anchor="middle">Pay</text>
<rect x="306" y="72" width="154" height="228" rx="8" fill="none" stroke="#7a8a94"/>
<text x="322" y="98" font-size="14" fill="#4a5a66">Order summary</text>
<rect x="322" y="112" width="122" height="12" rx="3" fill="#e1e8eb"/><rect x="322" y="134" width="96" height="12" rx="3" fill="#e1e8eb"/>
<rect x="322" y="156" width="110" height="12" rx="3" fill="#e1e8eb"/></svg>`,
};
