"use client";

// Onboarding in the Inbox: the cards for approvals your agents ask for ("ask me"),
// notices about invites your agents made, and pairing requests, with the small parts
// they share. Every card names the agent as someone's, with its harness mark, then
// Agent and Board, then the action's own fields. Every write goes to the public API;
// what only a person's own key may do shows the exact command for a terminal.

import { ArrowRight, Check, Circle, Copy, LoaderCircle, TriangleAlert, X } from "lucide-react";
import { type ReactNode, useEffect, useId, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { ApiError, type Member } from "./api";
import { SenderMark } from "./agent-mark";
import { Problem } from "./chrome";
import { changed } from "./onboarding-data";
import {
  type AdminActionResult,
  type Approval,
  type InviteNotice,
  type ServerInvite,
  type PairingRequest,
  allowApproval,
  cancelPairing,
  choosePairingAgent,
  declineApproval,
  declinePairing,
  revokeInvite,
} from "./onboarding-api";
import { type Names, acceptPrompt, agentLabel, alwaysAsks, allowable, byline, live, pairingLine, serverWide, until, wantsRest } from "./onboarding-words";
import { harnessName, identityOf, relativeTime } from "./words";

// ---------- the parts ----------

/** Command is an exact command to run in a terminal, with a copy button inside its box. */
export function Command({ label, command, note, copyLabel = "Copy the command" }: { label: string; command: string; note?: ReactNode; copyLabel?: string }) {
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(command);
      setCopied(true);
      window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => setCopied(false), 1800);
    } catch {
      setCopied(false);
    }
  };
  return (
    <div className="ob-command flex flex-col gap-1.5">
      <p className="text-meta font-bold text-muted">{label}</p>
      <div className="flex items-start gap-1 rounded-control border border-rule bg-background py-1 pr-1 pl-3">
        <code className="min-w-0 flex-1 py-1.5 font-mono text-[13px] leading-[1.55] break-words text-ink select-all">
          {/* Lines break between words only, never inside --server or an id. */}
          {command.split(" ").map((w, i) => (
            <span key={i}>
              {i > 0 && " "}
              <span className="whitespace-nowrap">{w}</span>
            </span>
          ))}
        </code>
        <button type="button" onClick={copy} aria-label={copied ? "Copied" : copyLabel} title="Copy" className="tap inline-flex size-9 shrink-0 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          {copied ? <Check className="size-4 text-accent-strong" strokeWidth={1.75} aria-hidden /> : <Copy className="size-4" strokeWidth={1.5} aria-hidden />}
        </button>
      </div>
      <span aria-live="polite" className="sr-only">
        {copied ? `Copied: ${label}` : ""}
      </span>
      {note && <p className="text-meta text-muted">{note}</p>}
    </div>
  );
}

/** Fields is the board view's two-column grid: a label in Meta, then its value. */
export function Fields({ rows }: { rows: [string, ReactNode][] }) {
  return (
    <dl className="grid grid-cols-[76px_minmax(0,1fr)] gap-x-3 gap-y-2">
      {rows.map(([k, v]) => (
        <div key={k} className="contents">
          <dt className="pt-px text-meta text-muted">{k}</dt>
          <dd className="min-w-0 break-words">{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Warning is a neutral box: warnings are never the accent, which means "act on this". */
export function Warning({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cn("ob-warning flex gap-2.5 rounded-box border border-field-border bg-selected px-3.5 py-3 text-ink", className)}>
      <TriangleAlert className="mt-0.5 size-4 shrink-0" strokeWidth={1.75} aria-label="Warning" />
      <div className="flex min-w-0 flex-col gap-2">{children}</div>
    </div>
  );
}

/** Done is a short line saying what happened, with an accent-strong check. */
export function Done({ children, muted = false }: { children: ReactNode; muted?: boolean }) {
  return (
    <p role="status" aria-live="polite" className={cn("ob-done flex items-start gap-2 animate-fade-in", muted && "text-muted")}>
      {muted ? <Circle className="mt-1 size-3.5 shrink-0 text-muted" strokeWidth={2} aria-hidden /> : <Check className="mt-0.5 size-[18px] shrink-0 text-accent-strong" strokeWidth={2} aria-hidden />}
      <span className="min-w-0">{children}</span>
    </p>
  );
}

type Stage = { label: string; state: "done" | "now" | "later" };

/** Stages is a request's progress: done steps with a check, the current one turning, later ones as a ring. */
function Stages({ stages }: { stages: Stage[] }) {
  return (
    <ol aria-label="Progress" className="ob-stages flex flex-col gap-2">
      {stages.map((s) => (
        <li key={s.label} className={cn("flex items-start gap-2.5", s.state === "later" && "text-muted", s.state === "now" && "font-bold")} aria-current={s.state === "now" ? "step" : undefined}>
          <span className="mt-0.5 inline-flex size-[18px] shrink-0 items-center justify-center" aria-hidden>
            {s.state === "done" ? <Check className="size-[18px] text-accent-strong" strokeWidth={2} /> : s.state === "now" ? <LoaderCircle className="size-4 text-ink motion-safe:animate-spin motion-safe:[animation-duration:2.4s]" strokeWidth={1.75} /> : <Circle className="size-3.5 text-faint" strokeWidth={1.75} />}
          </span>
          <span className="min-w-0">
            {s.label}
            <span className="sr-only">{s.state === "done" ? ", done" : s.state === "now" ? ", in progress" : ", not yet"}</span>
          </span>
        </li>
      ))}
    </ol>
  );
}

/** AgentMark is an agent's harness mark at the size of a card's title. */
export function AgentMark({ name, harness, className = "size-7" }: { name: string; harness?: string | null; className?: string }) {
  return <SenderMark name={name} kind="agent" harness={harness ?? null} identity={identityOf(`agent:${name}`)} className={className} />;
}

/** AgentTitle is a card's title: "Your agent [mark] reviewer wants …", the mark being its harness. */
function AgentTitle({ whose, agent, harness, rest }: { whose: string; agent: string; harness?: string; rest: string }) {
  return (
    <h2 className="text-headline font-bold break-words">
      {whose}{" "}
      <span className="inline-flex translate-y-[3px] align-baseline">
        <AgentMark name={agent} harness={harness} />
      </span>{" "}
      {agent} {rest}
    </h2>
  );
}

/** agentRow is the Agent field: "reviewer · Codex · your agent", so an agent never reads as a person. */
const agentRow = (agent: string, harness: string | undefined, whose: string): [string, ReactNode] => ["Agent", [agent, harnessName(harness), whose].filter(Boolean).join(" · ")];

/** boardRow is the Board field: where the agent asked, and a note when the change reaches the whole server. */
function boardRow(board: string | undefined, wide: boolean): [string, ReactNode] {
  return [
    "Board",
    <span key="board" className="flex flex-col">
      <span>{board ?? "no longer visible to you"}</span>
      {wide && <span className="text-meta text-muted">This changes the whole server, not just this board.</span>}
    </span>,
  ];
}

const ago = (at: string, now: number) => relativeTime(at, now);

/** problemOf is a write's refusal in words, with the exact command when only the person's key may do it. */
function Refusal({ error }: { error: unknown }) {
  if (error instanceof ApiError && error.next?.command) {
    return (
      <div className="flex flex-col gap-2">
        <Problem error={error} />
        <Command label="Run it in your own terminal" command={error.next.command} />
      </div>
    );
  }
  return <Problem error={error} />;
}

// ---------- approvals ----------

const inviteWarningText = "Agents allowed to invite people can let outsiders read every open board.";

export function ApprovalDetail({ a, names, now, settingsHref, onShowInvite }: { a: Approval; names: Names; now: number; settingsHref: string; onShowInvite?: (id: string) => void }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<{ always: boolean; warning?: string; invite?: ServerInvite } | null>(null);
  const [inviteShown, setInviteShown] = useState(true);
  const key = useRef<Record<string, string>>({});
  const act = a.action;
  const agent = agentLabel(a);
  const boards = (id: string) => a.display?.boards.find((b) => b.id === id)?.name ?? null;
  const why = alwaysAsks(act);
  const invite = act.kind === "invite_people";
  const decide = async (what: "once" | "always" | "decline") => {
    setBusy(true);
    setError(null);
    key.current[what] ??= crypto.randomUUID();
    try {
      if (what === "decline") await declineApproval(a.id, key.current[what]);
      else {
        const r: AdminActionResult = await allowApproval(a.id, what === "always", key.current[what]);
        setResult({ always: what === "always", warning: r.warning, invite: r.invite });
      }
      setConfirming(false);
      changed();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const rows: [string, ReactNode][] = [agentRow(agent, a.display?.agent_harness, "your agent"), boardRow(a.display?.requested_on?.name, serverWide(act))];
  const person = (id: string) => names.person(id) ?? "someone no longer on the server";
  if (act.kind === "invite_people") {
    rows.push(["Invite", `One new person, as a member. The invite works once, for ${Math.round((act.invite.ttl_seconds ?? 86_400) / 3600)} hours.`]);
    const joins = (act.invite.boards ?? []).map((id) => boards(id) ?? "a board you can't see");
    if (joins.length) rows.push(["Joins", `${joins.join(", ")}, as a member`]);
    if (act.invite.pairing) rows.push(["Pairing", <span key="p">{agent} asks the new person&apos;s agent to pair on: &ldquo;{act.invite.pairing.work}&rdquo;</span>]);
  } else if (act.kind === "add_people") {
    rows.push(["Person", person(act.person_id)], ["Change", `joins ${boards(act.board_id) ?? "the board"} as a member`]);
  } else if (act.kind === "set_server_role") {
    rows.push(["Person", person(act.person_id)], ["Change", act.role === "admin" ? "member to server admin: can invite and remove people and manage every board's people" : "admin to member"]);
  } else if (act.kind === "set_board_role") {
    rows.push(["Person", person(act.person_id)], ["Change", `member to ${act.role} of ${boards(act.board_id) ?? "the board"}`]);
  } else if (act.kind === "remove_person") {
    rows.push(["Person", person(act.person_id)], ["Change", act.board_id ? `leaves ${boards(act.board_id) ?? "the board"}` : "leaves the server, and every board on it"]);
  } else if (act.kind === "revoke_key") {
    rows.push(["Key", <span key="k" className="font-mono text-[13px]">{act.key_id}</span>], ["Change", "the key stops working"]);
  } else {
    rows.push(["Change", `the rules of ${boards(act.board_id) ?? "the board"}`]);
  }
  if (a.state === "pending" && a.expires_at) rows.push(["Expires", `This request ends ${until(a.expires_at)} if nobody decides`]);
  const command = a.next?.command ?? `aboard approvals allow ${a.id}`;
  const always = result?.always;
  return (
    <article className="ob-approval flex max-w-[640px] flex-col gap-5" data-approval={a.id}>
      <p className="text-meta text-muted">
        {a.state === "pending" ? "Asks you to approve" : "Asked you to approve"} · {ago(a.created_at, now)}
      </p>
      <AgentTitle whose="Your agent" agent={agent} harness={a.display?.agent_harness} rest={wantsRest(act, names, boards)} />
      <Fields rows={rows} />
      {a.state === "pending" && (
        <div className="flex flex-col gap-3">
          {confirming ? (
            <Warning className="animate-fade-in">
              <p className="font-bold">Let your agents invite people without asking?</p>
              <p>An agent can be talked into things by a message it reads. If one is tricked, it could invite someone you don&apos;t know, and that person could read every open board on this server.</p>
              <p className="text-meta text-muted">Each invite still shows in your Inbox, where you can revoke it before it is used. You can turn this off in Settings.</p>
              <div className="flex flex-wrap gap-2 pt-1">
                <Button variant="secondary" disabled={busy} onClick={() => void decide("always")}>
                  Allow always
                </Button>
                <Button variant="quiet" disabled={busy} onClick={() => setConfirming(false)}>
                  Cancel
                </Button>
              </div>
            </Warning>
          ) : (
            <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Decide">
              <Button variant="primary" disabled={busy} onClick={() => void decide("once")}>
                {busy ? "Allowing…" : "Allow once"}
              </Button>
              {allowable(act) && (
                <Button variant="secondary" disabled={busy} onClick={() => (invite ? setConfirming(true) : void decide("always"))}>
                  Allow always
                </Button>
              )}
              <Button variant="quiet" disabled={busy} onClick={() => void decide("decline")}>
                Decline
              </Button>
            </div>
          )}
          {why ? (
            <p className="text-meta text-muted">{why}</p>
          ) : (
            !confirming && (
              <p className="text-meta text-muted">
                {invite ? "Allow always lets your agents invite people without asking. " : "Allow always lets your agents add people to boards without asking, as members. "}
                Change it any time in{" "}
                <a href={settingsHref} className="text-muted">
                  Settings
                </a>
                .
              </p>
            )
          )}
          {error !== null && <Refusal error={error} />}
          <Command label="Or from your terminal" command={command} note="Only you can run it, in your own terminal: agent sessions can't approve." />
        </div>
      )}
      {a.state === "executed" && (
        <div className="flex flex-col gap-3">
          <Done>
            {always === undefined ? "Allowed" : always ? "Allowed always" : "Allowed once"}
            {a.decided_at && <span className="text-muted"> · {ago(a.decided_at, now)}</span>}.
            {a.execution?.invite_id && onShowInvite && (
              <>
                {" "}
                <button type="button" className="text-link underline underline-offset-[3px] hover:no-underline" onClick={() => onShowInvite(a.execution!.invite_id!)}>
                  See the invite
                </button>
              </>
            )}
          </Done>
          {always && (
            <p className="text-meta text-muted">
              Auto mode now covers {invite ? "inviting people to the server" : "adding people to boards"}.{" "}
              <a href={settingsHref} className="text-muted">
                Change it in Settings
              </a>
            </p>
          )}
          {result?.invite && inviteShown && <InviteIssued invite={result.invite} onDismiss={() => setInviteShown(false)} />}
          {always && invite && <Warning>{result?.warning ?? inviteWarningText}</Warning>}
          <p className="text-meta text-muted">
            In the record: {invite ? `an invite made by ${agent}, approved by you` : `${byline(act.kind === "add_people" ? "added" : "role_changed", agent, "you", "approval")}`}.<br />
            Approval <span className="font-mono text-[12px]">{a.id}</span>
          </p>
        </div>
      )}
      {a.state === "declined" && (
        <Done muted>
          Declined{a.decided_at && ` · ${ago(a.decided_at, now)}`}. Nothing was done; {agent} sees that you declined.
        </Done>
      )}
      {a.state === "expired" && <Done muted>Expired without a decision. Nothing was done; {agent} can ask again.</Done>}
    </article>
  );
}

/** InviteIssued shows the link and prompt the server made for an invite the person just allowed. The server returns the secret once, so this stays until dismissed and a reload loses it. */
export function InviteIssued({ invite, onDismiss }: { invite: ServerInvite; onDismiss: () => void }) {
  // The contract lets link be absent; the address is then built from the secret. A missing prompt is left out, never rebuilt here.
  const link = invite.link ?? `${window.location.origin}/join#${invite.invite}`;
  return (
    <section className="ob-invite-issued flex flex-col gap-3 rounded-box border border-field-border bg-selected px-3.5 py-3" aria-label="The invite you made">
      <p className="font-bold">The invite is ready</p>
      <Command label="Invite link" command={link} copyLabel="Copy the invite link" />
      {invite.suggested_handle && <p className="text-meta text-muted">Invited as @{invite.suggested_handle}</p>}
      {invite.prompt && <Command label="Prompt for the colleague's agent" command={invite.prompt} copyLabel="Copy the prompt" />}
      <p className="text-meta text-muted">Shown once. Send both to the person you&apos;re inviting; your agent can also collect them. Copy them now; they aren&apos;t shown again.</p>
      <div>
        <Button variant="quiet" onClick={onDismiss}>
          Dismiss
        </Button>
      </div>
    </section>
  );
}

/** approvalTitle is a row's first line for an approval. */
export const approvalTitle = (a: Approval, names: Names) => `Your agent ${agentLabel(a)} ${wantsRest(a.action, names, (id) => a.display?.boards.find((b) => b.id === id)?.name ?? null)}`;

// ---------- invite notices ----------

export const noticeState: Record<InviteNotice["state"], string> = { active: "Open", redeemed: "Used", revoked: "Revoked", expired: "Expired" };

/** noticeLine is a notice's state and time in a few words, for its row. */
export function noticeLine(n: InviteNotice, now: number): string {
  if (n.state === "active") return `Open · works until ${until(n.expires_at)}`;
  if (n.state === "redeemed") return "Used: someone joined with it";
  if (n.state === "revoked") return `Revoked · made ${ago(n.created_at, now)}`;
  return `Expired ${ago(n.expires_at, now)}, unused`;
}

export function NoticeDetail({ n, now }: { n: InviteNotice; now: number }) {
  const agent = agentLabel(n);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const revoke = async () => {
    setBusy(true);
    setError(null);
    try {
      await revokeInvite(n.id);
      changed();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <article className="ob-notice flex max-w-[640px] flex-col gap-5" data-notice={n.id}>
      <p className="text-meta text-muted">Invite notice · {ago(n.created_at, now)}</p>
      <AgentTitle whose="Your agent" agent={agent} harness={n.display?.agent_harness} rest="invited someone" />
      <Fields
        rows={[
          agentRow(agent, n.display?.agent_harness, "your agent"),
          boardRow(n.display?.requested_on?.name, true),
          ...(n.suggested_handle ? [["Invited as", `@${n.suggested_handle}`] as [string, ReactNode]] : []),
          ["State", noticeState[n.state]],
          ["Made", `${ago(n.created_at, now)} by ${agent}`],
          [n.state === "expired" ? "Ended" : "Works until", until(n.expires_at)],
          ["Invite", <span key="i" className="font-mono text-[13px]">{n.id}</span>],
        ]}
      />
      {n.state === "active" && (
        <div className="flex flex-col gap-3">
          <p className="text-muted">The invite makes one new account on this server, as a member. Revoke it if you didn&apos;t expect it, or it went to the wrong person.</p>
          <div>
            <Button variant="secondary" disabled={busy} onClick={() => void revoke()}>
              {busy ? "Revoking…" : "Revoke the invite"}
            </Button>
          </div>
          {error !== null && <Refusal error={error} />}
          {error === null && <Command label="Or from your terminal" command={n.next.command} />}
        </div>
      )}
      {n.state === "redeemed" && <Done>Someone joined the server with this invite. It can&apos;t be used again.</Done>}
      {n.state === "revoked" && <Done muted>Revoked. Nobody can join with it.</Done>}
      {n.state === "expired" && <Done muted>Expired before anyone used it. Nobody can join with it.</Done>}
      <p className="text-meta text-muted">The invite&apos;s link is shown once, to the person who approves it. {agent} can collect it from its session; this page can&apos;t show it again.</p>
    </article>
  );
}

// ---------- pairing requests ----------

/** PairingView is what a pairing card says about a request, from the viewer's side. */
export type PairingView = { mine: boolean; other: string; inviter: string; agent: string; board: string; chosen: Member | null };

export function pairingView(p: PairingRequest, names: Names, agents: Member[]): PairingView {
  const mine = names.me !== null && p.inviter_id === names.me.id;
  const inviter = mine ? (names.me?.name ?? "you") : (p.display?.person_handle ?? names.person(p.inviter_id) ?? "someone");
  const recipient = names.person(p.recipient_id) ?? (p.state === "awaiting_account" ? "the person you invited" : "the person you asked");
  const chosenId = p.chosen_recipient_agent_id ?? p.recipient?.agent_id;
  return {
    mine,
    other: mine ? recipient : inviter,
    inviter,
    agent: agentLabel(p),
    board: p.display?.boards.find((b) => b.id === p.board_id)?.name ?? names.board(p.board_id) ?? "a board",
    chosen: agents.find((m) => m.id === chosenId) ?? null,
  };
}

/** pairingTitle is a row's first line for a pairing request. */
export const pairingTitle = (v: PairingView) => (v.mine ? `Your agent ${v.agent} asked ${v.other}'s agents to pair on ${v.board}` : `${v.inviter}'s agent ${v.agent} asks your agents to pair on ${v.board}`);

/** waitsOnMe is true for a request to the viewer that no agent of theirs has been chosen for yet. */
export const waitsOnMe = (p: PairingRequest, v: PairingView) => !v.mine && p.state === "awaiting_session" && !p.chosen_recipient_agent_id;

function stagesOf(p: PairingRequest, v: PairingView): Stage[] {
  const order = ["awaiting_account", "awaiting_session", "awaiting_endpoint", "verifying", "ready"];
  let at = order.indexOf(p.state);
  if (!v.mine && p.state === "awaiting_session" && p.chosen_recipient_agent_id) at = 2;
  const steps: [string, string, number][] = [];
  if (v.mine && p.invite_id) steps.push([`${v.other} sets up their account`, `${v.other} set up their account`, 0]);
  steps.push(v.mine ? [`${v.other} chooses an agent`, `${v.other} chose an agent`, 1] : ["You choose an agent", "You chose an agent", 1]);
  const wait = p.awaiting === "initiator" ? `${v.mine ? v.agent : `${v.inviter}'s agent ${v.agent}`} comes back online` : v.mine ? `${v.other}'s agent accepts in its session` : `${v.chosen?.name ?? "Your agent"} accepts in its session`;
  steps.push([wait, v.mine ? `${v.other}'s agent accepted` : `${v.chosen?.name ?? "Your agent"} accepted`, 2]);
  steps.push([`Verifying delivery with ${v.other}'s agent`, "Delivery verified both ways", 3]);
  return steps.map(([doing, did, i]) => ({ label: at > i ? did : doing, state: at > i ? "done" : at === i ? "now" : "later" }));
}

export function PairingDetail({ p, names, agents, now, boardHref }: { p: PairingRequest; names: Names; agents: Member[]; now: number; boardHref: string }) {
  const v = pairingView(p, names, agents);
  const choosing = waitsOnMe(p, v);
  const [open, setOpen] = useState<"choose" | "prompt" | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const write = async (f: () => Promise<unknown>) => {
    setBusy(true);
    setError(null);
    try {
      await f();
      changed();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const prompt = acceptPrompt(p, v.inviter, v.agent, v.board);
  const chosenAt = v.chosen?.location;
  return (
    <article className="ob-pairing flex max-w-[640px] flex-col gap-5" data-pairing={p.id}>
      <p className="text-meta text-muted">Pairing request · {ago(p.created_at, now)}</p>
      <AgentTitle whose={v.mine ? "Your agent" : `${v.inviter}'s agent`} agent={v.agent} harness={p.display?.agent_harness} rest={v.mine ? `asked ${v.other}'s agents to pair on ${v.board}` : `asks your agents to pair on ${v.board}`} />
      <figure className="flex flex-col gap-1.5">
        <figcaption className="text-meta font-bold text-muted">Proposed work, in {v.mine ? "your" : `${v.inviter}'s`} words</figcaption>
        <blockquote className="rounded-box border border-rule bg-surface px-3.5 py-3 text-now break-words whitespace-pre-wrap">{p.work}</blockquote>
        {!v.mine && <p className="text-meta text-muted">Your agent reads this as a request, not as orders. Pairing gives {v.inviter} no control over your agents.</p>}
      </figure>
      <Fields
        rows={[
          agentRow(v.agent, p.display?.agent_harness, v.mine ? "your agent" : `${v.inviter}'s agent`),
          boardRow(v.board, false),
          ["With", v.mine ? (v.chosen ? `${v.other}'s agent ${v.chosen.name}` : `${v.other}'s agent, once they choose one`) : v.chosen ? `your agent ${v.chosen.name}` : "the agent you choose"],
          ...(live(p.state) ? ([["Expires", `Ends ${until(p.expires_at)} unless it is accepted`]] as [string, string][]) : []),
        ]}
      />
      {choosing && (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Answer the request">
            <Button variant="primary" aria-expanded={open === "choose"} onClick={() => setOpen(open === "choose" ? null : "choose")}>
              Choose an agent
            </Button>
            <Button
              variant="secondary"
              aria-expanded={open === "prompt"}
              onClick={() => {
                void navigator.clipboard?.writeText(prompt).catch(() => {});
                setOpen("prompt");
              }}
            >
              Copy prompt
            </Button>
            <Button variant="quiet" disabled={busy} onClick={() => void write(() => declinePairing(p.id))}>
              Decline
            </Button>
          </div>
          {open === "choose" && <ChooseAgent p={p} board={v.board} agents={agents} now={now} busy={busy} onClose={() => setOpen(null)} onChoose={(id) => void write(() => choosePairingAgent(p.id, id, p.generation))} />}
          {open === "prompt" && <PromptBox prompt={prompt} onClose={() => setOpen(null)} />}
          {error !== null && <Refusal error={error} />}
        </div>
      )}
      {!choosing && live(p.state) && (
        <div className="flex flex-col gap-3">
          <Stages stages={stagesOf(p, v)} />
          <p className="text-meta text-muted">
            {!v.mine && p.chosen_recipient_agent_id && p.state === "awaiting_session"
              ? `Sent to ${v.chosen?.name ?? "your agent"}${chosenAt ? `, in ${chosenAt.folder}${chosenAt.machine ? ` on ${chosenAt.machine}` : ""}` : ""}. It accepts the request in its session; this page follows along.`
              : p.state === "awaiting_account"
                ? `${v.other} has the invite link. Their agent sets up their account, then they choose a session.`
                : p.state === "verifying"
                  ? "Each agent sends the other a check and waits for its reply. Nothing is marked verified until both replies arrive."
                  : p.state === "awaiting_endpoint"
                    ? "Once that session is online and accepts, the two agents check delivery."
                    : `Nothing to do: ${v.other} picks which of their agents takes part.`}
          </p>
          {v.mine && (
            <div>
              <Button variant="quiet" className="-ml-3" disabled={busy} onClick={() => void write(() => cancelPairing(p.id))}>
                Cancel the request
              </Button>
            </div>
          )}
          {error !== null && <Refusal error={error} />}
        </div>
      )}
      {p.state === "ready" && (
        <div className="flex flex-col gap-3">
          <Stages stages={stagesOf(p, v)} />
          <Done>
            Ready: both agents connected, delivery verified.{" "}
            <a href={boardHref} className="inline-flex items-center gap-1">
              Open {v.board}
              <ArrowRight className="size-3.5" strokeWidth={1.75} aria-hidden />
            </a>
          </Done>
        </div>
      )}
      {p.state === "declined" && <Done muted>{v.mine ? `${v.other} declined. Nothing changed on ${v.board}.` : `You declined. ${v.inviter} sees that you declined; nothing changed on ${v.board}.`}</Done>}
      {p.state === "cancelled" && <Done muted>{v.mine ? "You cancelled the request." : `${v.inviter} cancelled the request.`} Nothing changed on {v.board}.</Done>}
      {p.state === "expired" && <Done muted>Expired {ago(p.expires_at, now)} before {v.mine ? `${v.other}'s agent` : "one of your agents"} accepted it. Nothing changed on {v.board}.</Done>}
    </article>
  );
}

/** ChooseAgent lists the person's agents on the request's board; the one they pick is asked to accept it. */
function ChooseAgent({ p, board, agents, now, busy, onClose, onChoose }: { p: PairingRequest; board: string; agents: Member[]; now: number; busy: boolean; onClose: () => void; onChoose: (id: string) => void }) {
  const id = useId();
  const here = agents.filter((m) => m.board === board && (!m.status || m.status === "active"));
  const online = (m: Member) => m.presence === "working" || m.presence === "idle" || m.presence === "waiting";
  const [pick, setPick] = useState<string | null>(here.find(online)?.id ?? null);
  return (
    <section aria-labelledby={`${id}-t`} className="ob-choose flex flex-col gap-3 rounded-box bg-surface px-3.5 pt-2 pb-3.5 animate-fade-in">
      <div className="flex items-center justify-between gap-2">
        <h3 id={`${id}-t`} className="font-bold">
          Choose an agent
        </h3>
        <button type="button" onClick={onClose} aria-label="Close Choose an agent" title="Close" className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      {here.length === 0 ? (
        <p className="text-meta text-muted">None of your agents is on {board} yet. Copy the prompt into a session instead: it joins {board} and accepts the request there.</p>
      ) : (
        <>
          <p className="-mt-2 text-meta text-muted">The agent you pick is sent the request on {board}, and accepts it in its session. It keeps working in its own folder.</p>
          <div role="radiogroup" aria-label="Your agents" className="flex flex-col gap-1">
            {here.map((m) => {
              const off = !online(m);
              const on = pick === m.id;
              const loc = m.location;
              return (
                <button
                  key={m.id}
                  type="button"
                  role="radio"
                  aria-checked={on}
                  disabled={off}
                  onClick={() => setPick(m.id)}
                  className={cn("grid min-h-14 w-full grid-cols-[20px_28px_minmax(0,1fr)] items-center gap-x-2.5 rounded-control border px-3 py-2 text-left transition-colors duration-[140ms] ease-out", on ? "border-accent-strong bg-selected" : "border-rule hover:bg-hover", off && "cursor-not-allowed text-muted hover:bg-transparent")}
                >
                  <span aria-hidden className={cn("inline-flex size-4 items-center justify-center rounded-full border", on ? "border-accent-strong" : "border-field-border")}>
                    {on && <span className="size-2 rounded-full bg-accent-strong" />}
                  </span>
                  <AgentMark name={m.name} harness={m.harness} />
                  <span className="flex min-w-0 flex-col">
                    <span className="truncate">
                      <span className="font-bold">{m.name}</span> · {harnessName(m.harness) ?? "agent"}
                      {loc && (
                        <>
                          {" "}
                          in <span className="font-mono text-[13px]">{loc.folder}</span>
                        </>
                      )}
                    </span>
                    <span className="truncate text-meta text-muted">
                      {[loc?.machine, off ? `offline${loc ? `, last seen ${relativeTime(loc.last_active, now)}` : ""}` : m.presence === "working" ? "working now" : "idle"].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                </button>
              );
            })}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button variant="primary" disabled={!pick || busy} onClick={() => pick && onChoose(pick)}>
              {busy ? "Sending…" : "Send it to this agent"}
            </Button>
            <p className="text-meta text-muted">Offline agents can&apos;t accept until their session runs again.</p>
          </div>
        </>
      )}
      <span className="sr-only">{p.id}</span>
    </section>
  );
}

/** PromptBox shows the prompt that was copied, for any session, the way Ask an agent does. */
function PromptBox({ prompt, onClose }: { prompt: string; onClose: () => void }) {
  const id = useId();
  return (
    <section aria-labelledby={`${id}-t`} className="ob-prompt flex flex-col gap-2 rounded-box bg-surface px-3.5 pt-2 pb-3 animate-fade-in">
      <div className="flex items-center justify-between gap-2">
        <h3 id={`${id}-t`} className="text-meta font-bold text-ink">
          Copied: paste it into any session of yours
        </h3>
        <button type="button" onClick={onClose} aria-label="Close the prompt" title="Close" className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <p className="-mt-1 text-meta text-muted">Claude Code, Codex or any agent set up for aboard. The session that runs it takes part; this card follows along.</p>
      <pre aria-label="Prompt for your session" className="rounded-control border border-rule bg-background px-3 py-2.5 font-sans text-body break-words whitespace-pre-wrap select-all">
        {prompt}
      </pre>
    </section>
  );
}

/** pairingRowLine is a pairing row's second line: the board, and where the request stands. */
export const pairingRowLine = (p: PairingRequest, v: PairingView) => `${v.board} · ${waitsOnMe(p, v) ? p.work : pairingLine(p, v.mine, v.other, v.chosen?.name ?? null)}`;
