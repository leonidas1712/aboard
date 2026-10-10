// The onboarding part of the public API the board view uses: the person's allowance,
// the approvals their agents ask for, notices about invites their agents made, pairing
// requests, the invite preview and the person's own agents. Shapes follow
// spec/openapi.yaml; ids bind every action, and display labels only name them.

import { type Member, type NextStep, get, post, put, send } from "./api";

export type { NextStep };

export type AllowanceCategory = "invite-people" | "add-people";

export type Allowance = { id: string; revision: number; person_id: string; categories: AllowanceCategory[]; warning?: string };

export type DisplayBoard = { id: string; name: string; title: string };

/** Display is the current labels for ids the person may already see; a missing label means it is no longer visible. */
export type Display = {
  person_handle?: string;
  recipient_handle?: string;
  recipient_agent_name?: string;
  recipient_agent_harness?: string;
  key_name?: string;
  agent_name?: string;
  agent_harness?: string;
  requested_on?: DisplayBoard;
  boards: DisplayBoard[];
};

export type AdminAction =
  | { kind: "invite_people"; invite: { ttl_seconds?: number; boards?: string[]; pairing?: { initiating_agent_id: string; work: string } } }
  | { kind: "add_people"; board_id: string; person_id: string }
  | { kind: "set_server_role"; person_id: string; role: "admin" | "member" }
  | { kind: "set_board_role"; board_id: string; person_id: string; role: string }
  | { kind: "remove_person"; person_id: string; board_id?: string }
  | { kind: "revoke_key"; key_id: string }
  | { kind: "set_board_policy"; board_id: string; policy: Record<string, unknown> };

export type AdminAuthorization = {
  kind?: "invited" | "added" | "role_changed";
  person_id: string;
  agent_id: string;
  via: "allowance" | "approval";
  approval_id?: string;
  allowance_id?: string;
};

export type Approval = {
  id: string;
  person_id: string;
  agent_id: string;
  action: AdminAction;
  state: "pending" | "executed" | "declined" | "expired";
  created_at: string;
  expires_at?: string;
  decided_at?: string;
  decision?: "once" | "always";
  execution?: { at: string; authorization: AdminAuthorization; invite_id?: string };
  next?: NextStep;
  display?: Display;
};

export type AdminActionResult = { state: "pending" | "executed"; approval: Approval; warning?: string; next?: NextStep };

export type InviteNotice = {
  id: string;
  issuing_agent_id: string;
  message: string;
  created_at: string;
  expires_at: string;
  state: "active" | "redeemed" | "revoked" | "expired";
  next: NextStep;
  display?: Display;
};

export type PairingState = "awaiting_account" | "awaiting_session" | "awaiting_endpoint" | "verifying" | "ready" | "declined" | "cancelled" | "expired";

export type PairingEndpoint = { person_id: string; agent_id: string; generation: number };

export type PairingRequest = {
  id: string;
  board_id: string;
  inviter_id: string;
  initiating_agent_id: string;
  recipient_id?: string;
  invite_id?: string;
  work: string;
  state: PairingState;
  generation: number;
  initiator?: PairingEndpoint;
  recipient?: PairingEndpoint;
  awaiting?: "initiator" | "recipient" | "both";
  chosen_recipient_agent_id?: string;
  choice_message_id?: string;
  created_at: string;
  expires_at: string;
  next?: NextStep;
  display?: Display;
};

export type InvitePreview = {
  server_id: string;
  server_url: string;
  server_name: string;
  inviter_handle: string;
  boards: DisplayBoard[];
  work?: string;
  expires_at: string;
};

export type Person = { id: string; handle: string; display_name: string | null };

export const getAllowance = () => get<Allowance>("/v1/me/allowance");
export const setAllowance = (categories: AllowanceCategory[]) => put<Allowance>("/v1/me/allowance", { categories });

export const listApprovals = (state: "pending" | "decided" | "all" = "all") => get<{ approvals: Approval[] }>("/v1/me/approvals", { state });
export const allowApproval = (id: string, always: boolean, key?: string) => post<AdminActionResult>(`/v1/me/approvals/${encodeURIComponent(id)}/allow`, { always }, key);
export const declineApproval = (id: string, key?: string) => send<Approval>("POST", `/v1/me/approvals/${encodeURIComponent(id)}/decline`, key);

export const listInviteNotices = () => get<{ notices: InviteNotice[] }>("/v1/me/invite-notices");
export const revokeInvite = (id: string) => send<{ id: string; revoked: true; changed: boolean }>("DELETE", `/v1/invites/${encodeURIComponent(id)}`);

export const listPairing = () => get<{ requests: PairingRequest[] }>("/v1/pairing-requests");
export const choosePairingAgent = (id: string, agent: string, generation: number, key?: string) =>
  post<PairingRequest>(`/v1/pairing-requests/${encodeURIComponent(id)}/choose`, { agent_id: agent, generation }, key);
export const declinePairing = (id: string) => send<PairingRequest>("POST", `/v1/pairing-requests/${encodeURIComponent(id)}/decline`);
export const cancelPairing = (id: string) => send<PairingRequest>("POST", `/v1/pairing-requests/${encodeURIComponent(id)}/cancel`);

export const listOwnAgents = () => get<{ agents: Member[] }>("/v1/me/agents");
export const listPeople = () => get<{ people: Person[] }>("/v1/people");

/** previewInvite reads what an invite link offers, without using it: the secret goes in the body, never the address. */
export async function previewInvite(invite: string): Promise<InvitePreview> {
  return post<InvitePreview>("/v1/invites/preview", { invite });
}
