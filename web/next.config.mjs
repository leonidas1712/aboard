// The UI is static files (output: 'export') that the aboard binary embeds and serves.
// Everything it shows comes from the public API and the event stream.
//
// ABOARD_LAB=1 builds the UI lab instead (web/lab, `make lab`): the same app against an
// in-memory fake of the API, with experimental views. The lab enters only through the
// "aboard-lab" module, which a normal build resolves to app/lab-off.ts (nothing), so no
// lab code is in the module graph of the build the binary embeds. `make web-lab-check`
// proves it on web/out.
import { PHASE_DEVELOPMENT_SERVER } from "next/constants.js";

const lab = process.env.ABOARD_LAB === "1";

/** @type {(phase: string) => import('next').NextConfig} */
export default function config(phase) {
  return {
    output: "export",
    images: { unoptimized: true },
    turbopack: {
      resolveAlias: { "aboard-lab": lab ? "./lab/entry.tsx" : "./app/lab-off.ts" },
    },
    // The lab builds beside the real UI, never over it: its static export in lab-out,
    // its dev server in .next-lab.
    ...(lab && {
      distDir: phase === PHASE_DEVELOPMENT_SERVER ? ".next-lab" : "lab-out",
      agentRules: false,
      devIndicators: false,
    }),
  };
}
