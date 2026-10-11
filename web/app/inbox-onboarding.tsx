"use client";

// The Inbox's onboarding items, in its groups: Needs you (approvals your agents asked
// for, and pairing requests to you that wait for you to choose an agent), Pairing (every
// other request, yours and to you, live ones first), Invites your agents made (a notice
// per invite) and Decided (approvals allowed, declined or expired in the last seven days).

import { cn } from "@/lib/utils";
import type { Onboarding } from "./onboarding-data";
import type { Approval, BoardAddNotice, InviteArrivalNotice, InviteNotice, PairingRequest } from "./onboarding-api";
import { ArrivalDetail, BoardAddDetail, addedKey, addedTitle, arrivalKey, arrivalTitle } from "./inbox-added";
import { AgentMark, ApprovalDetail, NoticeDetail, PairingDetail, approvalTitle, noticeLine, noticeTitle, pairingRowLine, pairingTitle, pairingView, waitsOnMe } from "./onboarding-ui";
import { agentLabel, allowable, live } from "./onboarding-words";
import { SenderMark } from "./agent-mark";
import { personIdentity, relativeTime } from "./words";
import { newerFirst } from "./time";

export type OnboardingItem =
  | { kind: "approval"; id: string; a: Approval; t: string }
  | { kind: "pairing"; id: string; p: PairingRequest; t: string }
  | { kind: "notice"; id: string; n: InviteNotice; t: string }
  | { kind: "added"; id: string; a: BoardAddNotice; t: string }
  | { kind: "arrival"; id: string; r: InviteArrivalNotice; t: string };

export type OnboardingGroups = { needs: OnboardingItem[]; news: OnboardingItem[]; pairing: OnboardingItem[]; invites: OnboardingItem[]; decided: OnboardingItem[] };

const newest = (x: OnboardingItem, y: OnboardingItem) => newerFirst(x.t, y.t);

/** onboardingGroups sorts the items; dismissed holds the keys of the board adds and arrivals this browser hid. */
export function onboardingGroups(ob: Onboarding, dismissed: Record<string, true>): OnboardingGroups {
  const approval = (a: Approval): OnboardingItem => ({ kind: "approval", id: a.id, a, t: a.decided_at ?? a.created_at });
  const pairing = (p: PairingRequest): OnboardingItem => ({ kind: "pairing", id: p.id, p, t: p.created_at });
  const asked = ob.pairing.filter((p) => waitsOnMe(p, pairingView(p, ob.names, ob.agents)));
  const news = [
    ...ob.inbox.board_adds.map((a): OnboardingItem => ({ kind: "added", id: addedKey(a), a, t: a.added.at })),
    ...ob.inbox.arrivals.map((r): OnboardingItem => ({ kind: "arrival", id: arrivalKey(r), r, t: r.at })),
  ]
    .filter((i) => !dismissed[i.id])
    .sort(newest);
  // A used invite whose arrival shows tells the same story twice: the arrival keeps it.
  const arrived = new Set(news.flatMap((i) => (i.kind === "arrival" ? [i.r.invite_id] : [])));
  return {
    needs: [...asked.map(pairing), ...ob.approvals.filter((a) => a.state === "pending").map(approval)].sort(newest),
    news,
    pairing: ob.pairing
      .filter((p) => !asked.includes(p))
      .map(pairing)
      .sort((x, y) => Number(live((y as { p: PairingRequest }).p.state)) - Number(live((x as { p: PairingRequest }).p.state)) || newest(x, y)),
    invites: ob.notices
      .filter((n) => !(n.state === "redeemed" && arrived.has(n.id)))
      .map((n): OnboardingItem => ({ kind: "notice", id: n.id, n, t: n.created_at }))
      .sort(newest),
    decided: ob.approvals.filter((a) => a.state !== "pending").map(approval).sort(newest),
  };
}

/** inviteItem is the item that tells an invite's story: its notice, or its arrival once someone joined with it. */
export function inviteItem(groups: OnboardingGroups, inviteId: string): string {
  const arrival = groups.news.find((i) => i.kind === "arrival" && i.r.invite_id === inviteId);
  return groups.invites.some((i) => i.id === inviteId) || !arrival ? inviteId : arrival.id;
}

/** arrivalBy is who made the invite someone arrived with ("your agent claude", or "you"), and how, for the arrival's Invite field. */
export function arrivalBy(r: InviteArrivalNotice, ob: Onboarding): { by: string; made: string } {
  const notice = ob.notices.find((n) => n.id === r.invite_id);
  if (!notice) return { by: "you", made: "Made by you" };
  const by = `your agent ${agentLabel(notice)}`;
  const approved = ob.approvals.some((a) => a.execution?.invite_id === r.invite_id);
  return { by, made: `Made by ${by}, ${approved ? "approved by you" : "on your allowance"}` };
}

/** age is how long ago, short enough for a row: "4 min", "now". */
function age(at: string, now: number): string {
  const words = relativeTime(at, now);
  return words === "just now" ? "now" : words.replace(/ ago$/, "");
}

export function OnboardingRow({ item, ob, now, selected, onRead }: { item: OnboardingItem; ob: Onboarding; now: number; selected: boolean; onRead: () => void }) {
  let who: { name: string; harness?: string; person?: true };
  let title: string;
  let meta: string;
  if (item.kind === "approval") {
    const a = item.a;
    who = { name: agentLabel(a), harness: a.display?.agent_harness };
    title = approvalTitle(a, ob.names);
    const state = a.state === "pending" ? (allowable(a.action) ? "asks you to approve" : "always asks you") : a.state === "executed" ? "allowed" : a.state;
    meta = [a.display?.requested_on?.name, agentLabel(a), state].filter(Boolean).join(" · ");
  } else if (item.kind === "added") {
    const by = item.a.added.by;
    // A person has no harness mark: only an agent's add shows one.
    who = by.kind === "agent" ? { name: by.name ?? "someone" } : { name: by.name ?? "someone", person: true };
    title = addedTitle(item.a);
    meta = `${item.a.board.title || item.a.board.name} · paste the join prompt into a session`;
  } else if (item.kind === "arrival") {
    who = { name: item.r.handle, person: true };
    title = arrivalTitle(item.r);
    const { by } = arrivalBy(item.r, ob);
    meta = `${item.r.boards.map((b) => b.board.title || b.board.name).join(", ")} · ${by === "you" ? "you invited them" : `invited by ${by}`}`;
  } else if (item.kind === "notice") {
    who = { name: agentLabel(item.n), harness: item.n.display?.agent_harness };
    title = noticeTitle(item.n, ob.names);
    meta = noticeLine(item.n, ob.names, now);
  } else {
    const v = pairingView(item.p, ob.names, ob.agents);
    who = { name: v.agent, harness: item.p.display?.agent_harness };
    title = pairingTitle(v);
    meta = pairingRowLine(item.p, v);
  }
  return (
    <li>
      <button
        type="button"
        data-item={item.id}
        onClick={onRead}
        aria-current={selected ? "true" : undefined}
        className={cn("grid min-h-14 w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-3 rounded-control px-2.5 py-3 text-left transition-colors duration-[140ms] ease-out hover:bg-hover active:bg-selected", selected && "bg-selected hover:bg-selected")}
      >
        <span className="mt-0.5">
          {who.person ? <SenderMark name={who.name} kind="human" identity={personIdentity(who.name)} className="size-7" /> : <AgentMark name={who.name} harness={who.harness} />}
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="flex min-w-0 items-baseline gap-3">
            <span className={cn("line-clamp-2 min-w-0 flex-1", selected && "font-bold")}>{title}</span>
            <span className="shrink-0 text-meta text-muted tabular-nums">{age(item.t, now)}</span>
          </span>
          <span className="truncate text-meta text-muted">{meta}</span>
        </span>
      </button>
    </li>
  );
}

export function OnboardingDetail({ item, ob, now, onShowInvite, onDismiss }: { item: OnboardingItem; ob: Onboarding; now: number; onShowInvite: (id: string) => void; onDismiss: (id: string) => void }) {
  if (item.kind === "added") return <BoardAddDetail n={item.a} agents={ob.agents} now={now} onDismiss={() => onDismiss(item.id)} />;
  if (item.kind === "arrival") return <ArrivalDetail n={item.r} made={arrivalBy(item.r, ob).made} now={now} onDismiss={() => onDismiss(item.id)} />;
  if (item.kind === "approval") return <ApprovalDetail a={item.a} names={ob.names} now={now} settingsHref="/?view=settings" onShowInvite={onShowInvite} />;
  if (item.kind === "notice") return <NoticeDetail n={item.n} names={ob.names} now={now} />;

  const v = pairingView(item.p, ob.names, ob.agents);
  return <PairingDetail p={item.p} names={ob.names} agents={ob.agents} now={now} boardHref={`/?board=${encodeURIComponent(v.board)}`} />;
}
