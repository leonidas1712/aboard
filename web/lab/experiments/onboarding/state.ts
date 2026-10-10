"use client";

// EXPERIMENTAL, lab only: what the person did to the onboarding mock on this page
// (allowed or declined an approval, changed the allowance, revoked an invite, chose a
// session or declined a pairing request), laid over the scenario's step. In the product
// each of these is one API call (/v1/me/approvals/{id}/allow, PUT /v1/me/allowance,
// DELETE /v1/invites/{id}, /v1/pairing-requests/{id}/decline); here nothing leaves the page.
// ?allow=add-people,invite-people starts the page with that allowance, for screenshots.

import { useSyncExternalStore } from "react";
import type { AllowanceCategory, Approval, InviteNotice, Onboarding, Pairing } from "../../onboarding";
import { at, scenario, useLab } from "../../store";

type Local = {
  decisions: Record<string, { state: "executed" | "declined"; always: boolean; t: number }>;
  allowance: AllowanceCategory[] | null;
  revoked: string[];
  pairing: Record<string, Partial<Pairing>>;
  /** asked is the session each pairing request was handed to from the Inbox. */
  asked: Record<string, string>;
};

function preset(): AllowanceCategory[] | null {
  if (typeof window === "undefined") return null;
  const v = new URLSearchParams(window.location.search).get("allow");
  if (v === null) return null;
  return v.split(",").filter((c): c is AllowanceCategory => c === "add-people" || c === "invite-people");
}

let local: Local = { decisions: {}, allowance: preset(), revoked: [], pairing: {}, asked: {} };
const listeners = new Set<() => void>();
const set = (next: Partial<Local>) => {
  local = { ...local, ...next };
  for (const l of listeners) l();
};

/** minutesNow is the lab clock in scenario minutes. */
export const minutesNow = () => (Date.now() - at(0)) / 60_000;

/** serverUrl is the issuer every command names, as the API's next.command does. */
export const serverUrl = scenario.people.length > 1 ? "https://aboard.example.team" : "http://localhost:7777";

/** useOnboarding is the step's onboarding with what the person did here laid over it. */
export function useOnboarding(): Onboarding & { asked: Record<string, string> } {
  const { snap } = useLab();
  const l = useSyncExternalStore(
    (f) => {
      listeners.add(f);
      return () => listeners.delete(f);
    },
    () => local,
    () => local,
  );
  const o = snap.onboarding;
  const approvals: Approval[] = o.approvals.map((a) => {
    const d = l.decisions[a.id];
    if (!d || a.state !== "pending") return a;
    return { ...a, state: d.state, decided: { t: d.t, always: d.always }, invite: d.state === "executed" && a.action.kind === "invite_people" ? `inv_LAB${a.id.slice(7)}` : undefined };
  });
  // An invite allowed here makes its notice, as the server would.
  const made: InviteNotice[] = approvals
    .filter((a) => a.invite && !o.notices.some((n) => n.id === a.invite))
    .map((a) => ({ id: a.invite!, agent: a.agent, t: a.decided!.t, expires: a.decided!.t + (a.action.kind === "invite_people" ? a.action.ttl_hours * 60 : 1440), state: "active" }));
  const notices = [...o.notices, ...made].map((n) => (l.revoked.includes(n.id) && n.state === "active" ? { ...n, state: "revoked" as const } : n));
  const pairing = o.pairing.map((p) => ({ ...p, ...l.pairing[p.id] }));
  return { ...o, allowance: l.allowance ?? o.allowance, approvals, notices, pairing, asked: l.asked };
}

/** decide allows an approval once or always, or declines it. */
export function decide(a: Approval, state: "executed" | "declined", always = false, current: AllowanceCategory[] = []) {
  set({ decisions: { ...local.decisions, [a.id]: { state, always, t: minutesNow() } } });
  if (always) {
    const c = a.action.kind === "invite_people" ? "invite-people" : "add-people";
    if (!current.includes(c)) set({ allowance: [...current, c] });
  }
}

/** setAllowance replaces the person's allowance, as PUT /v1/me/allowance does. */
export const setAllowance = (categories: AllowanceCategory[]) => set({ allowance: categories });

/** revoke revokes an unredeemed invite. */
export const revoke = (id: string) => set({ revoked: [...local.revoked, id] });

/** declinePairing declines a request addressed to the person, or cancels their own. */
export const declinePairing = (p: Pairing, mine: boolean) => set({ pairing: { ...local.pairing, [p.id]: { state: mine ? "cancelled" : "declined" } } });

/** askSession hands a request to one of the person's sessions, which then accepts it there. */
export const askSession = (p: Pairing, session: string) => set({ asked: { ...local.asked, [p.id]: session } });
