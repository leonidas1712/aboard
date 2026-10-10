// The words onboarding uses in the Inbox, Settings and on the board, in one place, so
// the cards, rows and record say the same thing the same way. Labels come from the
// API's display fields; when one is missing the thing is no longer visible, and the
// words say so plainly instead of guessing.

import type { AdminAction, Approval, InviteNotice, PairingRequest, PairingState } from "./onboarding-api";

/** Names resolves the ids an onboarding item carries to the labels the person may see. */
export type Names = {
  me: { id: string; name: string } | null;
  person: (id: string | undefined) => string | null;
  board: (id: string | undefined) => string | null;
};

const someone = (n: string | null) => n ?? "someone";

/** wantsRest is what an agent asks to do, after its name: "wants to invite someone…". */
export function wantsRest(a: AdminAction, names: Names, boards: (id: string) => string | null): string {
  const board = (id: string | undefined) => (id ? (boards(id) ?? names.board(id) ?? "a board") : "a board");
  switch (a.kind) {
    case "invite_people":
      return "wants to invite someone to the server as a member";
    case "add_people":
      return `wants to add ${someone(names.person(a.person_id))} to ${board(a.board_id)} as a member`;
    case "set_server_role":
      return a.role === "admin" ? `wants to make ${someone(names.person(a.person_id))} a server admin` : `wants to make ${someone(names.person(a.person_id))} an ordinary member of the server`;
    case "set_board_role":
      return a.role === "owner" ? `wants to make ${someone(names.person(a.person_id))} an owner of ${board(a.board_id)}` : `wants to make ${someone(names.person(a.person_id))} an ordinary member of ${board(a.board_id)}`;
    case "remove_person":
      return a.board_id ? `wants to remove ${someone(names.person(a.person_id))} from ${board(a.board_id)}` : `wants to remove ${someone(names.person(a.person_id))} from the server`;
    case "revoke_key":
      return "wants to revoke an access key";
    case "set_board_policy":
      return `wants to change the rules of ${board(a.board_id)}`;
  }
}

/** serverWide is true for an action that changes the whole server, not one board. */
export const serverWide = (a: AdminAction) => a.kind === "invite_people" || a.kind === "set_server_role" || a.kind === "revoke_key" || (a.kind === "remove_person" && !a.board_id);

/** allowable is true for the two kinds of action an allowance can cover; the rest always ask. */
export const allowable = (a: AdminAction) => a.kind === "invite_people" || a.kind === "add_people";

/** alwaysAsks says why an action can't be allowed in advance, or null when it can. */
export function alwaysAsks(a: AdminAction): string | null {
  switch (a.kind) {
    case "set_server_role":
    case "set_board_role":
      return "Changing someone's role always asks you. It can't be allowed in advance.";
    case "remove_person":
      return "Removing people always asks you. It can't be allowed in advance.";
    case "revoke_key":
      return "Revoking keys always asks you. It can't be allowed in advance.";
    case "set_board_policy":
      return "Changing a board's rules always asks you. It can't be allowed in advance.";
    default:
      return null;
  }
}

/** agentLabel is the agent an item names, or a plain word when it is no longer visible. */
export const agentLabel = (x: Approval | InviteNotice | PairingRequest) => x.display?.agent_name ?? "an agent";

/** byline is how the record reads an admin action an agent took for its person. */
export function byline(kind: "invited" | "added" | "role_changed" | undefined, agent: string, person: string, via: "allowance" | "approval"): string {
  const verb = kind === "invited" ? "invited" : kind === "role_changed" ? "changed" : "added";
  return via === "allowance" ? `${verb} by ${agent}, on ${person}'s allowance` : `${verb} by ${agent}, approved by ${person}`;
}

/** live says whether a pairing request is still under way. */
export const live = (s: PairingState) => s !== "ready" && s !== "declined" && s !== "cancelled" && s !== "expired";

/** acceptCommand is the exact command a session runs to take a pairing request. */
export function acceptCommand(p: PairingRequest): string {
  const server = typeof window === "undefined" ? "" : window.location.origin;
  return `aboard pairing accept ${p.id} --here --server '${server}'`;
}

/** acceptPrompt is what a person pastes into a session so that session takes the request. */
export function acceptPrompt(p: PairingRequest, inviter: string, agent: string, board: string): string {
  return `Accept my aboard pairing request from ${inviter} in this session: run ${acceptCommand(p)}. Then start on the proposed work with ${inviter}'s agent ${agent} on ${board}.`;
}

/**
 * pairingLine is a request's state in one line, for its row and the board. Only ready
 * says verified: it is the only state where both round trips were checked.
 */
export function pairingLine(p: PairingRequest, mine: boolean, other: string, chosenName: string | null): string {
  const theirAgent = `${other}'s agent`;
  switch (p.state) {
    case "awaiting_account":
      return `Waiting for ${other} to set up their account`;
    case "awaiting_session":
      if (!mine && p.chosen_recipient_agent_id) return `Waiting for ${chosenName ?? "your agent"} to accept in its session`;
      return mine ? `Waiting for ${other} to choose an agent` : "Choose an agent to take part";
    case "awaiting_endpoint":
      if (!mine && p.awaiting !== "initiator") return `Waiting for ${chosenName ?? "your agent"} to accept in its session`;
      return `Waiting for ${theirAgent} to come online`;
    case "verifying":
      return `Verifying delivery with ${theirAgent}…`;
    case "ready":
      return "Ready: both agents connected, delivery verified";
    case "declined":
      return mine ? `${other} declined` : "You declined";
    case "cancelled":
      return mine ? "You cancelled it" : `${other} cancelled it`;
    case "expired":
      return "Expired before it was accepted";
  }
}

/** until says when something stops: a time today, tomorrow, or a weekday and date. */
export function until(at: string): string {
  const d = new Date(at);
  const time = d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  const now = new Date(Date.now());
  if (d.toDateString() === now.toDateString()) return `today at ${time}`;
  if (d.toDateString() === new Date(Date.now() + 86_400_000).toDateString()) return `tomorrow at ${time}`;
  return `${d.toLocaleDateString(undefined, { weekday: "long", day: "numeric", month: "short" })} at ${time}`;
}
