// EXPERIMENTAL, lab only: the words the onboarding mock uses, in one place, so the
// Inbox, the board and the record say the same thing the same way.

import type { AdminAction, Approval, Pairing, PairingState } from "../../onboarding";
import { scenario } from "../../store";
import { serverUrl } from "./state";

const me = scenario.me;

/** whose is "your" for the viewer and "sam's" for anyone else. */
export const whose = (person: string) => (person === me ? "your" : `${person}'s`);

/** wantsRest is what an agent asks to do, after its name: "wants to invite someone…". */
export function wantsRest(a: AdminAction): string {
  switch (a.kind) {
    case "invite_people":
      return "wants to invite someone to the server as a member";
    case "add_people":
      return `wants to add ${a.person} to ${a.board} as a member`;
    case "set_server_role":
      return a.role === "admin" ? `wants to make ${a.person} a server admin` : `wants to make ${a.person} an ordinary member of the server`;
    case "set_board_role":
      return a.role === "owner" ? `wants to make ${a.person} an owner of ${a.board}` : `wants to make ${a.person} an ordinary member of ${a.board}`;
    case "remove_person":
      return a.board ? `wants to remove ${a.person} from ${a.board}` : `wants to remove ${a.person} from the server`;
    case "revoke_key":
      return `wants to revoke ${a.person}'s key ${a.key_name}`;
    case "set_board_policy":
      return `wants to change the rules of ${a.board}`;
  }
}

/** wants is the card's title in words: "Your agent reviewer wants to make priya a server admin". */
export const wants = (agent: string, a: AdminAction) => `Your agent ${agent} ${wantsRest(a)}`;

/** serverWide is true for an action that changes the whole server, not one board. */
export const serverWide = (a: AdminAction) => a.kind === "invite_people" || a.kind === "set_server_role" || a.kind === "revoke_key" || (a.kind === "remove_person" && !a.board);

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

/** did is what the agent did once allowed, for the record: "writer invited someone". */
export function did(agent: string, a: AdminAction): string {
  switch (a.kind) {
    case "invite_people":
      return `${agent} invited someone to the server`;
    case "add_people":
      return `${agent} added ${a.person} to ${a.board}`;
    case "set_server_role":
      return `${agent} made ${a.person} a server ${a.role}`;
    case "set_board_role":
      return `${agent} made ${a.person} ${a.role === "owner" ? "an owner" : "a member"} of ${a.board}`;
    case "remove_person":
      return `${agent} removed ${a.person} from ${a.board ?? "the server"}`;
    case "revoke_key":
      return `${agent} revoked ${a.person}'s key ${a.key_name}`;
    case "set_board_policy":
      return `${agent} changed the rules of ${a.board}`;
  }
}

/** byline is how the record reads an admin action an agent took for its person. */
export function byline(verb: "invited" | "added", agent: string, person: string, via: "allowance" | "approval"): string {
  return via === "allowance" ? `${verb} by ${agent}, on ${person}'s allowance` : `${verb} by ${agent}, approved by ${person}`;
}

/** The exact commands the API's next.command carries, filled in with real ids and the server. */
export const commands = {
  allow: (a: Approval) => `aboard approvals allow ${a.id} --server '${serverUrl}'`,
  decline: (a: Approval) => `aboard approvals decline ${a.id} --server '${serverUrl}'`,
  revoke: (id: string) => `aboard invite revoke '${id}' --server '${serverUrl}'`,
  accept: (p: Pairing) => `aboard pairing accept ${p.id} --here --server '${serverUrl}'`,
};

/** acceptPrompt is what a person pastes into a session so that session takes the request. */
export function acceptPrompt(p: Pairing): string {
  return `Accept my aboard pairing request from ${p.inviter} in this session: run ${commands.accept(p)}. Then start on the proposed work with ${p.inviter}'s agent ${p.agent} on ${p.board}.`;
}

/** live says whether a pairing request is still under way. */
export const live = (s: PairingState) => s !== "ready" && s !== "declined" && s !== "cancelled" && s !== "expired";

/**
 * pairingLine is a request's state in one line, for its row and the board. Only "ready"
 * says verified: it is the only state where both round trips were checked.
 */
export function pairingLine(p: Pairing, asked?: string): string {
  const mine = p.inviter === me;
  const other = mine ? p.recipient : p.inviter;
  const theirAgent = `${other}'s agent`;
  switch (p.state) {
    case "awaiting_account":
      return `Waiting for ${p.recipient} to set up their account`;
    case "awaiting_session":
      if (!mine && asked) return "Waiting for your session to accept";
      return mine ? `Waiting for ${p.recipient} to choose an agent` : "Choose an agent to take part";
    case "awaiting_endpoint":
      if (!mine && p.awaiting !== "initiator") return `Waiting for ${p.recipientAgent ?? "your session"} to accept in its session`;
      return `Waiting for ${theirAgent} to come online`;
    case "verifying":
      return `Verifying delivery with ${theirAgent}…`;
    case "ready":
      return "Ready: both agents connected, delivery verified";
    case "declined":
      return mine ? `${p.recipient} declined` : "You declined";
    case "cancelled":
      return mine ? "You cancelled it" : `${p.inviter} cancelled it`;
    case "expired":
      return "Expired before it was accepted";
  }
}

/** until says when something stops, as the real UI says a code's end: a time today, else a weekday and time. */
export function until(ms: number): string {
  const d = new Date(ms);
  const time = d.toLocaleTimeString("en-GB", { hour: "2-digit", minute: "2-digit" });
  const today = new Date(Date.now());
  if (d.toDateString() === today.toDateString()) return `today at ${time}`;
  const tomorrow = new Date(Date.now() + 86_400_000);
  if (d.toDateString() === tomorrow.toDateString()) return `tomorrow at ${time}`;
  return `${d.toLocaleDateString("en-GB", { weekday: "long", day: "numeric", month: "short" })} at ${time}`;
}

/** shortId is an id cut for a row: its kind and last six characters, enough to match a terminal. */
export const shortId = (id: string) => `${id.slice(0, 4)}…${id.slice(-6)}`;
