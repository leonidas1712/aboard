"use client";

// The UI lab's entry: what "aboard-lab" resolves to in a lab build (next.config.mjs).
// Loading it, before the page asks the server anything, starts the lab clock, puts the
// fake API under window.fetch and keeps links inside the lab. It fills the real UI's
// slots (app/lab-seam.ts) with the experimental views.

import type { Lab } from "@/app/lab-seam";
import { asksOf } from "./experiments/asks";
import { Centre } from "./experiments/centre";
import { MessageMeta, ThreadMeta } from "./experiments/chips";
import { Inbox } from "./experiments/inbox";
import { MessageFooter } from "./experiments/message-footer";
import { Nav } from "./experiments/nav";
import { AccountItem, OnboardingCentre, eventLine } from "./experiments/onboarding/board";
import { OnboardingInbox } from "./experiments/onboarding/inbox";
import { JoinPage } from "./experiments/onboarding/join";
import { groups } from "./experiments/onboarding/model";
import { Settings } from "./experiments/onboarding/settings";
import { Text } from "./experiments/text";
import { Title, WorkPanel } from "./experiments/work";
import { install } from "./fake-api";
import "./lab.css";
import { HarnessMark } from "./experiments/harness-mark";
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
  if (!q.has("board") && !q.has("inbox") && !q.has("list") && !q.has("settings") && !q.has("join")) {
    const { snap } = current();
    const asks = asksOf(snap, {}).length + groups({ ...snap.onboarding, asked: {} }).needs.length;
    history.replaceState(null, "", labHref(snap.onboarding.join ? { join: "1" } : asks > 0 ? { inbox: "1" } : { board: scenario.board.name }));
  }
  // The lab's colour schemes: ?theme= picks one for this load (screenshots use it), else
  // the one this browser chose. The page's first-paint script only knows light and dark.
  try {
    const asked = q.get("theme");
    if (asked) localStorage.setItem("aboard.theme", JSON.stringify(asked));
    const chosen = JSON.parse(localStorage.getItem("aboard.theme") ?? "null");
    if (typeof chosen === "string" && chosen !== "system") document.documentElement.dataset.theme = chosen;
  } catch {
    // No storage: the system's light or dark.
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
      const changes: Record<string, string | null> = { board: null, inbox: null, list: href === "/" ? "1" : null, view: null, task: null, artifact: null, settings: null, join: null, item: null };
      for (const [k, v] of next) changes[k] = v;
      window.location.href = labHref(changes);
    },
    true,
  );
}

// The onboarding scenarios have their own Inbox, Settings and invite page.
const onboarding = scenario.steps.some((s) => s.onboarding);

function place(): string | null {
  if (typeof window === "undefined") return null;
  const q = new URLSearchParams(window.location.search);
  if (onboarding && q.has("join")) return "join";
  if (onboarding && q.has("settings")) return "settings";
  return q.has("inbox") ? "inbox" : null;
}

function Place({ onSignOut }: { onSignOut: () => void }) {
  const p = place();
  if (p === "join") return <JoinPage />;
  if (p === "settings") return <Settings onSignOut={onSignOut} />;
  return onboarding ? <OnboardingInbox onSignOut={onSignOut} /> : <Inbox onSignOut={onSignOut} />;
}

export const lab: Lab | null = {
  Overlay: Panel,
  place,
  Place,
  Nav,
  Centre: onboarding ? OnboardingCentre : Centre,
  ...(onboarding && { AccountItems: AccountItem, eventLine }),
  RightTitle: Title,
  RightPanel: WorkPanel,
  MessageFooter,
  MessageMeta,
  ThreadMeta,
  Text,
  AgentMark: HarnessMark,
};
