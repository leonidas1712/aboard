"use client";

// The UI lab's entry: what "aboard-lab" resolves to in a lab build (next.config.mjs).
// Loading it, before the page asks the server anything, starts the lab clock, puts the
// fake API under window.fetch and keeps links inside the lab. It fills the real UI's
// slots (app/lab-seam.ts) with the experimental views.

import type { Lab } from "@/app/lab-seam";
import { asksOf } from "./experiments/asks";
import { Centre } from "./experiments/centre";
import { Inbox } from "./experiments/inbox";
import { MessageFooter } from "./experiments/message-footer";
import { Nav } from "./experiments/nav";
import { Text } from "./experiments/text";
import { Title, WorkPanel } from "./experiments/work";
import { install } from "./fake-api";
import "./lab.css";
import { Panel } from "./panel";
import { current, labHref, scenario, startClock } from "./store";

if (typeof window !== "undefined") {
  startClock();
  try {
    // Every load builds its board afresh, with new times, so the record check starts over.
    for (const k of Object.keys(localStorage)) if (k.startsWith("aboard.verifiedHead.")) localStorage.removeItem(k);
    // The charter, rules and details start folded under the Work panel.
    for (const id of ["charter", "rules", "board-details"]) if (localStorage.getItem(`aboard.open.${id}`) === null) localStorage.setItem(`aboard.open.${id}`, "false");
  } catch {
    // No storage: nothing to clear, and the sections start open.
  }
  // With nothing named, the lab opens where the person would: the Inbox when something
  // waits on them, else the scenario's board.
  const q = new URLSearchParams(window.location.search);
  if (!q.has("board") && !q.has("inbox") && !q.has("list")) {
    const asks = asksOf(current().snap, {}).length;
    history.replaceState(null, "", labHref(asks > 0 ? { inbox: "1" } : { board: scenario.board.name }));
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
      const changes: Record<string, string | null> = { board: null, inbox: null, list: href === "/" ? "1" : null, view: null, task: null, artifact: null };
      for (const [k, v] of next) changes[k] = v;
      window.location.href = labHref(changes);
    },
    true,
  );
}

export const lab: Lab | null = {
  Overlay: Panel,
  place: () => (typeof window !== "undefined" && new URLSearchParams(window.location.search).has("inbox") ? "inbox" : null),
  Place: Inbox,
  Nav,
  Centre,
  RightTitle: Title,
  RightPanel: WorkPanel,
  MessageFooter,
  Text,
};
