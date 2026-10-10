// EXPERIMENTAL, lab only: the onboarding items the Inbox lists, in its groups.

import type { Approval, InviteNotice, Onboarding, Pairing } from "../../onboarding";
import { scenario } from "../../store";
import { live } from "./words";

export type Item =
  | { kind: "approval"; id: string; a: Approval }
  | { kind: "pairing"; id: string; p: Pairing }
  | { kind: "notice"; id: string; n: InviteNotice };

export type Groups = {
  /** needs is what waits on the person: pending approvals and requests to choose a session for. */
  needs: Item[];
  /** pairing is every other request, theirs and the person's own, live ones first. */
  pairing: Item[];
  /** invites is a notice for every invite the person's agents made. */
  invites: Item[];
  /** decided is the approvals already allowed, declined or expired, newest first. */
  decided: Item[];
};

/** waitsOnMe is true for a pairing request addressed to the viewer that no session has taken yet. */
export const waitsOnMe = (p: Pairing, asked: Record<string, string>) => p.recipient === scenario.me && p.state === "awaiting_session" && !asked[p.id];

export function groups(o: Onboarding & { asked: Record<string, string> }): Groups {
  const approval = (a: Approval): Item => ({ kind: "approval", id: a.id, a });
  const pairing = (p: Pairing): Item => ({ kind: "pairing", id: p.id, p });
  const pending = o.approvals.filter((a) => a.state === "pending").sort((x, y) => y.t - x.t);
  const asked = o.pairing.filter((p) => waitsOnMe(p, o.asked));
  const rest = o.pairing.filter((p) => !waitsOnMe(p, o.asked)).sort((x, y) => Number(live(y.state)) - Number(live(x.state)) || y.t - x.t);
  return {
    needs: [...asked.map(pairing), ...pending.map(approval)],
    pairing: rest.map(pairing),
    invites: [...o.notices].sort((x, y) => y.t - x.t).map((n) => ({ kind: "notice", id: n.id, n })),
    decided: o.approvals
      .filter((a) => a.state !== "pending")
      .sort((x, y) => (y.decided?.t ?? y.expires) - (x.decided?.t ?? x.expires))
      .map(approval),
  };
}

/** needsCount is how many onboarding items wait on the person, for the Inbox's count. */
export const needsCount = (o: Onboarding & { asked: Record<string, string> }) => groups(o).needs.length;
