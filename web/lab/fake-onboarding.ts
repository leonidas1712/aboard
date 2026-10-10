// The fake API's onboarding routes: the person's allowance, approvals, invite notices,
// pairing requests, the invite preview and the person's people, served from the
// scenario's onboarding data in the shapes spec/openapi.yaml gives them (ids, display
// labels, next commands). What the person does here (allow, decline, change the
// allowance, choose an agent, decline or cancel a request) is kept until the page
// reloads, laid over the scenario's step, the way the server keeps it. As on the real
// server, a browser can't revoke an invite: that answers with the command to run.

import type { Member } from "@/app/api";
import { type AdminAction, type AllowanceCategory, type Approval, type InviteNotice, type Pairing, inviteWarning } from "./onboarding";
import { at, current, scenario } from "./store";

type Ctx = {
  me: string;
  personId: (name: string) => string;
  memberId: (board: string, name: string) => string;
  board: (name: string) => { id: string; name: string; title: string };
  /** ownAgents is the viewer's agents on every board, with their locations. */
  ownAgents: () => Member[];
};

const iso = (t: number) => new Date(at(t)).toISOString();
const server = () => window.location.origin;
const command = (c: string) => `${c} --server '${server()}'`;

// What the person did on this page, by id.
const decided: Record<string, { state: "executed" | "declined"; t: number }> = {};
const pairingChanges: Record<string, Partial<Pairing> & { chosen?: string }> = {};
let allowance: AllowanceCategory[] | null = null;
let revision = 0;

const minutesNow = () => (Date.now() - at(0)) / 60_000;

function onboarding() {
  const o = current().snap.onboarding;
  const approvals = o.approvals.map((a): Approval => {
    const d = decided[a.id];
    return d && a.state === "pending" ? { ...a, state: d.state, decided: { t: d.t }, invite: d.state === "executed" && a.action.kind === "invite_people" ? `inv_LAB${a.id.slice(7)}` : a.invite } : a;
  });
  const made = approvals
    .filter((a) => a.invite && !o.notices.some((n) => n.id === a.invite))
    .map((a): InviteNotice => ({ id: a.invite!, agent: a.agent, t: a.decided!.t, expires: a.decided!.t + 1440, state: "active" }));
  return { ...o, allowance: allowance ?? o.allowance, approvals, notices: [...o.notices, ...made], pairing: o.pairing.map((p) => ({ ...p, ...pairingChanges[p.id] })) };
}

/** needs counts what waits on the person, for the lab's Inbox count and where the lab opens. */
export function onboardingNeeds(): number {
  const o = onboarding();
  return o.approvals.filter((a) => a.state === "pending").length + o.pairing.filter((p) => p.recipient === scenario.me && p.state === "awaiting_session" && !pairingChanges[p.id]?.chosen).length;
}

function display(c: Ctx, person: string, agent: string, agentBoard: string, boards: string[]) {
  const harness = scenario.agents.find((a) => a.name === agent)?.harness;
  return {
    person_handle: person,
    agent_name: agent,
    ...(harness && { agent_harness: harness }),
    requested_on: c.board(agentBoard),
    boards: boards.map((b) => c.board(b)),
  };
}

function action(c: Ctx, a: Approval): Record<string, unknown> {
  const x: AdminAction = a.action;
  switch (x.kind) {
    case "invite_people":
      return { kind: x.kind, invite: { ttl_seconds: x.ttl_hours * 3600, boards: x.boards.map((b) => c.board(b).id), ...(x.pairing && { pairing: { initiating_agent_id: c.memberId(a.board, a.agent), work: x.pairing.work } }) } };
    case "add_people":
      return { kind: x.kind, board_id: c.board(x.board).id, person_id: c.personId(x.person) };
    case "set_server_role":
      return { kind: x.kind, person_id: c.personId(x.person), role: x.role };
    case "set_board_role":
      return { kind: x.kind, board_id: c.board(x.board).id, person_id: c.personId(x.person), role: x.role };
    case "remove_person":
      return { kind: x.kind, person_id: c.personId(x.person), ...(x.board && { board_id: c.board(x.board).id }) };
    case "revoke_key":
      return { kind: x.kind, key_id: "key_01K7Q0LAB0REVOKEDKEY000000" };
    case "set_board_policy":
      return { kind: x.kind, board_id: c.board(x.board).id, policy: { change: x.change } };
  }
}

function approvalOf(c: Ctx, a: Approval) {
  const x = a.action;
  const boards = x.kind === "invite_people" ? x.boards : "board" in x && x.board ? [x.board] : [];
  return {
    id: a.id,
    person_id: c.personId(c.me),
    agent_id: c.memberId(a.board, a.agent),
    parent_key_id: "key_01K7Q0LAB0PARENTKEY000000A",
    action: action(c, a),
    payload_hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000",
    state: a.state,
    created_at: iso(a.t),
    expires_at: iso(a.expires),
    ...(a.decided && { decided_at: iso(a.decided.t) }),
    ...(a.state === "executed" && {
      execution: { at: iso(a.decided?.t ?? a.t), authorization: { person_id: c.personId(c.me), agent_id: c.memberId(a.board, a.agent), parent_key_id: "key_01K7Q0LAB0PARENTKEY000000A", via: "approval", approval_id: a.id, payload_hash: "sha256:0000000000000000000000000000000000000000000000000000000000000000" }, ...(a.invite && { invite_id: a.invite }) },
    }),
    ...(a.state === "pending" && { next: { command: command(`aboard approvals allow ${a.id}`), board_view: "Inbox", resume: "Continue after your person allows or declines this exact action." } }),
    display: display(c, c.me, a.agent, a.board, boards),
  };
}

function noticeOf(c: Ctx, n: InviteNotice, board: string) {
  return {
    id: n.id,
    issuing_agent_id: c.memberId(board, n.agent),
    message: "Your agent invited someone",
    created_at: iso(n.t),
    expires_at: iso(n.expires),
    state: n.state,
    next: { command: command(`aboard invite revoke '${n.id}'`), resume: "The invite stops working at once." },
    display: display(c, c.me, n.agent, board, []),
  };
}

function pairingOf(c: Ctx, p: Pairing & { chosen?: string }) {
  const chosen = p.chosen ?? (p.state !== "awaiting_session" && p.state !== "awaiting_account" ? p.recipientAgent : undefined);
  return {
    id: p.id,
    server_id: "srv_01K7Q0LAB0SERVER0000000000",
    board_id: c.board(p.board).id,
    inviter_id: c.personId(p.inviter),
    initiating_agent_id: c.memberId(p.board, p.agent),
    ...(p.state !== "awaiting_account" && { recipient_id: c.personId(p.recipient) }),
    ...(p.invite && { invite_id: p.invite }),
    work: p.work,
    state: p.state,
    generation: 1,
    ...(p.awaiting && { awaiting: p.awaiting }),
    ...(chosen && { chosen_recipient_agent_id: c.memberId(p.board, chosen) }),
    created_at: iso(p.t),
    expires_at: iso(p.expires),
    display: display(c, p.inviter, p.agent, p.board, [p.board]),
  };
}

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** onboardingRoute answers an onboarding request, or returns null for a path it doesn't serve. */
export function onboardingRoute(c: Ctx, method: string, parts: string[], q: URLSearchParams, body: Record<string, unknown>): Response | null {
  const [a, b, d, e] = parts;
  const o = onboarding();
  const agentBoard = scenario.board.name;
  if (a === "me" && b === "allowance") {
    if (method === "PUT") {
      allowance = ((body.categories as AllowanceCategory[]) ?? []).filter((x) => x === "add-people" || x === "invite-people");
      revision++;
    }
    const cats = method === "PUT" ? allowance! : o.allowance;
    return json({ id: "alw_01K7Q0LAB0ALLWNCE00000000", revision, person_id: c.personId(c.me), categories: cats, ...(cats.includes("invite-people") && { warning: inviteWarning }) });
  }
  if (a === "me" && b === "approvals" && !d) {
    const state = q.get("state") ?? "all";
    const list = o.approvals.filter((x) => state === "all" || (state === "pending" ? x.state === "pending" : x.state !== "pending"));
    return json({ approvals: list.sort((x, y) => Number(y.state === "pending") - Number(x.state === "pending") || x.t - y.t).map((x) => approvalOf(c, x)) });
  }
  if (a === "me" && b === "approvals" && d && (e === "allow" || e === "decline")) {
    const found = o.approvals.find((x) => x.id === d);
    if (!found) return json({ error: { code: "approval_not_found", message: "No approval has that id.", hint: "Read your approvals again." } }, 404);
    if (found.state === "pending") decided[d] = { state: e === "allow" ? "executed" : "declined", t: minutesNow() };
    const always = e === "allow" && body.always === true;
    if (always) {
      const cat: AllowanceCategory = found.action.kind === "invite_people" ? "invite-people" : "add-people";
      allowance = [...new Set([...(allowance ?? o.allowance), cat])];
      revision++;
    }
    const now = onboarding().approvals.find((x) => x.id === d)!;
    if (e === "decline") return json(approvalOf(c, now));
    return json({ state: "executed", approval: approvalOf(c, now), ...(always && found.action.kind === "invite_people" && { warning: inviteWarning }) });
  }
  if (a === "me" && b === "invite-notices") return json({ notices: o.notices.sort((x, y) => y.t - x.t).map((n) => noticeOf(c, n, agentBoard)) });
  if (a === "me" && b === "agents") return json({ agents: c.ownAgents() });
  if (a === "invites" && b === "preview") {
    const j = o.join;
    if (!j || body.invite !== j.secret) return json({ error: { code: "invite_invalid", message: "That invite can't be used.", hint: "Ask for a new invite." } }, 404);
    return json({ server_id: "srv_01K7Q0LAB0SERVER0000000000", server_url: `https://${j.server}`, server_name: j.server, inviter_handle: j.inviter, boards: j.boards.map((x) => ({ id: c.board(x.name).id, name: x.name, title: x.title })), ...(j.pairing && { work: j.pairing.work }), expires_at: iso(j.expires) });
  }
  if (a === "invites" && b && method === "DELETE") {
    return json({ error: { code: "human_token_required", message: "Manage invitations with your own person key.", hint: "Run aboard invites in your terminal.", next: { command: command(`aboard invite revoke '${b}'`), resume: "The invite stops working at once." } } }, 403);
  }
  if (a === "pairing-requests" && !b) return json({ requests: o.pairing.map((p) => pairingOf(c, p)) });
  if (a === "pairing-requests" && b && d) {
    const p = o.pairing.find((x) => x.id === b);
    if (!p) return json({ error: { code: "pairing_not_found", message: "No pairing request has that id.", hint: "Read your pairing requests again." } }, 404);
    if (d === "choose") {
      const agent = c.ownAgents().find((m) => m.id === body.agent_id);
      if (!agent) return json({ error: { code: "agent_not_found", message: "That agent isn't yours on this board.", hint: "Choose one of your agents on the board." } }, 404);
      pairingChanges[b] = { ...pairingChanges[b], chosen: agent.name };
    }
    if (d === "decline") pairingChanges[b] = { ...pairingChanges[b], state: "declined" };
    if (d === "cancel") pairingChanges[b] = { ...pairingChanges[b], state: "cancelled" };
    return json(pairingOf(c, { ...p, ...pairingChanges[b] }));
  }
  return null;
}
