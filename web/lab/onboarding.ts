// The scenario data behind the lab's onboarding: a person's allowance (auto mode), the
// approvals their agents ask for (ask me), notices about invites their agents made,
// pairing requests, and the invite a newcomer opens. It is written the easy way, with
// times in scenario minutes and people, agents and boards by name; the fake API
// (fake-onboarding.ts) serves it in the API's own shapes, with ids and display labels.

/** AllowanceCategory is one kind of admin work a person may let their agents do without asking. */
export type AllowanceCategory = "invite-people" | "add-people";

/** AdminAction is the exact action an agent asked for, one of the API's closed set. */
export type AdminAction =
  | { kind: "invite_people"; boards: string[]; pairing?: { work: string }; ttl_hours: number }
  | { kind: "add_people"; board: string; person: string }
  | { kind: "set_server_role"; person: string; role: "admin" | "member" }
  | { kind: "set_board_role"; board: string; person: string; role: "owner" | "member" }
  | { kind: "remove_person"; person: string; board?: string }
  | { kind: "revoke_key"; person: string; key_name: string }
  | { kind: "set_board_policy"; board: string; change: string };

export type ApprovalState = "pending" | "executed" | "declined" | "expired";

export type Approval = {
  /** id is apr_ and 26 characters, as the server makes it. */
  id: string;
  /** agent is the requesting agent (agent_id), and board the board that seat is on. */
  agent: string;
  board: string;
  action: AdminAction;
  state: ApprovalState;
  /** t and expires are created_at and expires_at, in scenario minutes. */
  t: number;
  expires: number;
  /** decided is decided_at, and always whether the person also turned the category on. */
  decided?: { t: number; always?: boolean };
  /** invite is the invite an executed invite_people approval made (its execution.invite_id). */
  invite?: string;
};

export type InviteNoticeState = "active" | "redeemed" | "revoked" | "expired";

/** InviteNotice is the API's "Your agent invited someone": no secret, no recipient, no board names. */
export type InviteNotice = {
  id: string;
  /** agent is the issuing agent (issuing_agent_id). */
  agent: string;
  t: number;
  expires: number;
  state: InviteNoticeState;
};

export type PairingState = "awaiting_account" | "awaiting_session" | "awaiting_endpoint" | "verifying" | "ready" | "declined" | "cancelled" | "expired";

export type Pairing = {
  /** id is prq_ and 26 characters. */
  id: string;
  board: string;
  /** inviter is the person who asked (inviter_id) and agent their initiating agent. */
  inviter: string;
  agent: string;
  /** recipient is the person asked; their agent is the session they chose, once they have. */
  recipient: string;
  recipientAgent?: string;
  /** work is the proposed work: the inviter's words, content and never orders. */
  work: string;
  state: PairingState;
  /** invite is the invite the request rode on (invite_id), when the recipient was new. */
  invite?: string;
  /** awaiting says whose session the request waits on while awaiting_endpoint or verifying. */
  awaiting?: "initiator" | "recipient" | "both";
  t: number;
  expires: number;
};

/** JoinInvite is what the invite page shows about the link it was opened with. */
export type JoinInvite = {
  server: string;
  inviter: string;
  agent: string;
  boards: { name: string; title: string }[];
  pairing?: { work: string };
  expires: number;
  /** secret is the invite in the link's fragment; the lab's is made up. */
  secret: string;
};

/** OnboardingStep is what one step changes: the allowance, and approvals, notices and requests by id. */
export type OnboardingStep = {
  allowance?: AllowanceCategory[];
  approvals?: Approval[];
  notices?: InviteNotice[];
  pairing?: Pairing[];
  join?: JoinInvite | null;
};

export type Onboarding = {
  allowance: AllowanceCategory[];
  approvals: Approval[];
  notices: InviteNotice[];
  pairing: Pairing[];
  join: JoinInvite | null;
};

export const noOnboarding: Onboarding = { allowance: [], approvals: [], notices: [], pairing: [], join: null };

/** foldOnboarding applies one step's changes. */
export function foldOnboarding(o: Onboarding, s: OnboardingStep | undefined): Onboarding {
  if (!s) return o;
  const byId = <T extends { id: string }>(list: T[], changes: T[] | undefined): T[] => {
    if (!changes) return list;
    const out = new Map(list.map((x) => [x.id, x]));
    for (const c of changes) out.set(c.id, c);
    return [...out.values()];
  };
  return {
    allowance: s.allowance ?? o.allowance,
    approvals: byId(o.approvals, s.approvals),
    notices: byId(o.notices, s.notices),
    pairing: byId(o.pairing, s.pairing),
    join: s.join === undefined ? o.join : s.join,
  };
}

/** risky is true for actions that can never be in an allowance: they ask every time. */
export function risky(a: AdminAction): boolean {
  return a.kind !== "invite_people" && a.kind !== "add_people";
}

/** categoryOf is the allowance category an action falls under, or null when it always asks. */
export function categoryOf(a: AdminAction): AllowanceCategory | null {
  return a.kind === "invite_people" ? "invite-people" : a.kind === "add_people" ? "add-people" : null;
}

/** inviteWarning is the API's warning, word for word, whenever inviting is allowed. */
export const inviteWarning = "Agents allowed to invite people can let outsiders read every open board.";
