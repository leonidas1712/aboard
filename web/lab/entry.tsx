"use client";

// The UI lab's entry: what "aboard-lab" resolves to in a lab build (next.config.mjs).
// Loading it, before the page asks the server anything, starts the lab clock, puts the
// fake API under window.fetch and keeps links inside the lab. It fills the board view's
// slots (app/lab-seam.ts) with the experimental views.

import type { Lab } from "@/app/lab-seam";
import { AgentGroups } from "./experiments/agent-groups";
import { Centre } from "./experiments/centre";
import { NowLine } from "./experiments/now-line";
import { install } from "./fake-api";
import "./lab.css";
import { Panel } from "./panel";
import { labHref, startClock } from "./store";

if (typeof window !== "undefined") {
  startClock();
  // Every load builds its board afresh, with new times, so the record check starts over.
  try {
    for (const k of Object.keys(localStorage)) if (k.startsWith("aboard.verifiedHead.")) localStorage.removeItem(k);
  } catch {
    // No storage: nothing remembered to clear.
  }
  install();
  // The real UI links to /?board=NAME and /; in the lab they keep the scenario and step,
  // and stay on this page's own path, so the static export works from any folder.
  document.addEventListener(
    "click",
    (e) => {
      const a = (e.target as Element | null)?.closest?.("a");
      const href = a?.getAttribute("href");
      if (!a || !href || !(href === "/" || href.startsWith("/?")) || e.defaultPrevented || e.metaKey || e.ctrlKey) return;
      e.preventDefault();
      const next = new URLSearchParams(href.slice(2));
      const changes: Record<string, string | null> = { board: null, view: null };
      for (const [k, v] of next) changes[k] = v;
      window.location.href = labHref(changes);
    },
    true,
  );
}

export const lab: Lab | null = { Overlay: Panel, Centre, Agents: AgentGroups, AgentLine: NowLine };
