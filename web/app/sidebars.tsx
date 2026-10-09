"use client";

// The board view's side panels. The left one is navigation: the boards this person is
// on. The right one is about the board on screen: its agents and people, its charter,
// the rules Aboard enforces on it, and its details.

import { CheckCheck, ChevronDown, ChevronRight, CircleQuestionMark } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { Collapsible, CollapsibleContent, CollapsibleTrigger } from "@/components/ui/collapsible";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { ApiError, type Board, type MidturnPolicy, type Member, type MemberRef, type Message, type Task, addedBy, isArchived, setDelivery } from "./api";
import { AgentMark } from "./agent-mark";
import { RemoveAgent, RemovedAgents } from "./agent-removal";
import { attentionCount } from "./asks";
import { AddAgent, Details } from "./board-details";
import { LifecycleActions } from "./board-lifecycle";
import { modeRules, type SettableMode, settableModes } from "./delivery-modes.gen";
import { AgentLocation } from "./location";
import { changeMidturn, effectiveMidturn, midturnLabels, midturnPolicies, useMidturn } from "./midturn";
import { Kbd } from "./keys-sheet";
import { keyLabel } from "./keys";
import { usePref } from "./prefs";
import type { RecordCheck } from "./use-board";
import { LineText, StatusDot } from "./status";
import { TaskChips, useAgentTasks, useStatus } from "./task-ui";
import { appliedMode, boardLabel, charterBlocks, count, harnessName, relativeTime, rules } from "./words";
import { VisibilityControl } from "./visibility";

/** BoardNav keeps unanswered questions distinct from messages the person hasn't read. */
export function BoardNav({ current, boards, onMarkRead }: { current: string; boards: Board[] | null; onMarkRead: (b: Board) => void }) {
  if (boards === null) return <div className="h-11 animate-pulse rounded-control bg-selected motion-reduce:animate-none" aria-label="Loading" />;
  const recent = [...boards].sort((a, b) => {
    const activity = (b.last_message_at ?? b.created_at).localeCompare(a.last_message_at ?? a.created_at);
    return activity || a.id.localeCompare(b.id);
  });
  const archived = recent.filter((b) => isArchived(b));
  const active = recent.filter((b) => !isArchived(b));
  const needs = active.filter((b) => attentionCount(b) > 0);
  const others = active.filter((b) => attentionCount(b) === 0);
  return (
    <div className="flex flex-col gap-6">
      <a href="/?inbox" aria-current={current === "" ? "page" : undefined} className={cn("-mx-2.5 flex min-h-11 items-center justify-between rounded-control px-2.5 font-bold text-ink no-underline hover:bg-hover", current === "" && "bg-selected hover:bg-selected")}>
        <span className="flex items-center gap-2">Inbox{current !== "" && <Kbd>{keyLabel("inbox")}</Kbd>}</span>
        {boards.reduce((n, b) => n + (b.asks_to_me?.blocking ?? 0), 0) > 0 && <span className="rounded-[6px] bg-attention px-2 py-0.5 text-meta font-bold text-on-accent tabular-nums">{boards.reduce((n, b) => n + (b.asks_to_me?.blocking ?? 0), 0)}</span>}
      </a>
      {needs.length > 0 && (
        <section aria-label="Needs you">
          <h3 className="mb-2 text-meta font-bold text-ink">Needs you</h3>
          <BoardLinks current={current} boards={needs} onMarkRead={onMarkRead} />
        </section>
      )}
      {others.length > 0 && (
        <section aria-label={needs.length > 0 ? "Other boards" : "Your boards"}>
          {needs.length > 0 && <h3 className="mb-2 text-meta font-bold text-muted">Other boards</h3>}
          <BoardLinks current={current} boards={others} onMarkRead={onMarkRead} />
        </section>
      )}
      {archived.length > 0 && (
        <ArchivedGroup count={archived.length} holdsCurrent={archived.some((b) => b.name === current)}>
          <BoardLinks current={current} boards={archived} onMarkRead={onMarkRead} />
        </ArchivedGroup>
      )}
      {boards.length === 0 && <p className="text-meta text-muted">No boards yet.</p>}
    </div>
  );
}

/**
 * ArchivedGroup holds archived boards at the bottom of a list, closed unless the board
 * on screen is one of them, so finished work stays out of the way.
 */
export function ArchivedGroup({ count: n, holdsCurrent, children }: { count: number; holdsCurrent: boolean; children: ReactNode }) {
  const [open, setOpen] = useState(holdsCurrent);
  // When the board on screen moves into the group, the group opens so its link stays in
  // view; a later collapse by hand is kept.
  const [heldCurrent, setHeldCurrent] = useState(holdsCurrent);
  if (holdsCurrent !== heldCurrent) {
    setHeldCurrent(holdsCurrent);
    if (holdsCurrent) setOpen(true);
  }
  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <section aria-label="Archived boards" className="archived-boards flex flex-col">
        <h3 className="text-meta font-bold text-muted">
          <CollapsibleTrigger className="group -ml-2 flex min-h-9 pointer-coarse:min-h-11 w-[calc(100%+0.5rem)] items-center gap-1.5 rounded-[6px] px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
            <ChevronRight
              className="size-3.5 shrink-0 transition-transform duration-200 ease-out group-data-[state=open]:rotate-90"
              strokeWidth={1.75}
              aria-hidden
            />
            Archived
            <span className="font-normal tabular-nums">{n}</span>
          </CollapsibleTrigger>
        </h3>
        <CollapsibleContent className="pt-2 animate-fade-in">{children}</CollapsibleContent>
      </section>
    </Collapsible>
  );
}

// A board with something unread offers "Mark all as read" on hover or focus, over its
// unread count, which it stands for.
function BoardLinks({ current, boards, onMarkRead }: { current: string; boards: Board[]; onMarkRead: (b: Board) => void }) {
  return (
    <ul className="board-nav -mx-2.5 flex flex-col gap-1">
      {boards.map((b) => {
        const here = b.name === current;
        return (
          <li key={b.id} className="group relative">
            <a
              href={`/?board=${encodeURIComponent(b.name)}`}
              aria-current={here ? "page" : undefined}
              className={cn(
                "flex min-h-12 items-center gap-3 rounded-control px-2.5 py-2 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-hover",
                here && "bg-selected hover:bg-selected",
              )}
            >
              <span className="flex min-w-0 flex-1 flex-col">
                <span className={cn("break-words", here && "font-bold")}>{boardLabel(b)}</span>
                {b.title && <span className="text-meta break-all text-muted">{b.name}</span>}
              </span>
              {attentionCount(b) > 0 && (
                <span className="needs-reply-count shrink-0 rounded-[6px] bg-attention px-2 py-0.5 text-meta font-bold text-on-accent tabular-nums" title={count(attentionCount(b), "question needs your reply", "questions need your reply")}>
                  <span aria-hidden>{attentionCount(b)}</span>
                  <span className="sr-only">, {count(attentionCount(b), "question needs your reply", "questions need your reply")}</span>
                </span>
              )}
              {(b.asks_to_me?.going_with ?? 0) > 0 && <span className="shrink-0 text-meta text-muted tabular-nums" title="Going-with asks">{b.asks_to_me!.going_with}<span className="sr-only"> going-with asks</span></span>}
              {(b.unread ?? 0) > 0 && (
                <span className="unread-count shrink-0 text-meta text-muted tabular-nums" title={`${b.unread} unread`}>
                  <span aria-hidden>{b.unread}</span>
                  <span className="sr-only">, {b.unread} unread</span>
                </span>
              )}
              {b.added && (b.unread ?? 0) === 0 && (
                <span className="added-new shrink-0 text-meta text-muted" title={`Added by ${addedBy(b.added)}`}>
                  new<span className="sr-only">, added by {addedBy(b.added)}</span>
                </span>
              )}
            </a>
            {(b.unread ?? 0) > 0 && (
              <button
                type="button"
                className="mark-read pointer-events-none absolute top-1/2 right-1.5 z-10 inline-flex size-8 -translate-y-1/2 items-center justify-center rounded-[6px] bg-selected text-muted opacity-0 transition-opacity duration-[140ms] ease-out group-hover:pointer-events-auto group-hover:opacity-100 hover:text-ink focus-visible:pointer-events-auto focus-visible:opacity-100 [@media(hover:none)]:hidden"
                aria-label={`Mark all as read on ${boardLabel(b)}`}
                title="Mark all as read"
                onClick={() => onMarkRead(b)}
              >
                <CheckCheck className="size-4" strokeWidth={1.5} aria-hidden />
              </button>
            )}
          </li>
        );
      })}
    </ul>
  );
}

/** Reveal asks the board panel to open one section and bring it into view; n makes each ask new. */
export type Reveal = { section: string; n: number } | null;

type BoardPanelProps = {
  board: Board | null;
  members: Member[] | null;
  record: RecordCheck;
  /** me is the person's own name. */
  me: string | null;
  /** meId is the person's permanent id, which their agents name as owner_id. */
  meId: string | null;
  /** canInvite shows "Add an agent": the browser acts as a person who isn't a guest, since a guest's agents come only from guest codes. */
  canInvite: boolean;
  /** from is the member the timeline is filtered to, if any. */
  from: string | undefined;
  /** onPick filters the timeline to a member, or clears that filter when it's already set. */
  onPick: (name: string) => void;
  reveal: Reveal;
  /** onLifecycle reloads the board after it is archived or restored. */
  onLifecycle: () => void;
  /** identity is a member's identity colour, for its mark. */
  identity: (m: MemberRef) => number;
  /** latest is an agent's newest loaded message on the board, for its details. */
  latest: (agent: string) => Message | null;
  /** onShowMessage shows a message in the conversation. */
  onShowMessage: (id: string) => void;
  /** agents is false to leave out the agents and people, which the UI lab shows its own way. */
  agents?: boolean;
  /** byAgent lists each agent with the tasks it is on, busy agents first (Work by agent). */
  byAgent?: boolean;
};

/** BoardPanel is everything about the board on screen, in sections that open and close. */
export function BoardPanel({ board, members, record, me, meId, canInvite, from, onPick, reveal, onLifecycle, identity, latest, onShowMessage, agents: showAgents = true, byAgent = false }: BoardPanelProps) {
  const agents = (members ?? []).filter((m) => m.kind === "agent");
  const people = (members ?? []).filter((m) => m.kind === "human");
  return (
    <div className="flex flex-col gap-6">
      {showAgents && (
        <Section id="board-agents" title={people.length > 1 ? "Agents and people" : "Agents"} reveal={reveal}>
          <div className="flex flex-col gap-5 pt-2">
            {canInvite && board && !isArchived(board) && <AddAgent board={board} />}
            <WhosHere board={board} members={members} me={me} meId={meId} from={from} onPick={onPick} onRemoved={onLifecycle} identity={identity} latest={latest} onShowMessage={onShowMessage} byAgent={byAgent} />
            {canInvite && board && <RemovedAgents key={board.name} board={board.name} />}
          </div>
        </Section>
      )}

      {board?.charter && (
        <Section
          id="charter"
          title="Charter"
          help="Written by this board's admins. Every agent reads it when it joins and follows it as guidance."
          reveal={reveal}
        >
          <Charter text={board.charter} />
        </Section>
      )}

      {board && (
        <Section
          id="rules"
          title="Rules Aboard enforces"
          help="Set by the board's policy and checked by the server on every message. Agents can't break them."
          reveal={reveal}
        >
          <div className="flex flex-col gap-2">
            {rules(board).map((r) => (
              <p key={r}>{r}</p>
            ))}
            {board.policy.preset === "starter" && (
              <p>
                This board is on the starter policy. Before adding more agents or people, run{" "}
                <code>aboard board policy recommended</code>.
              </p>
            )}
          </div>
        </Section>
      )}

      {board && (
        <Section id="board-details" title="Details" reveal={reveal}>
          <div className="flex flex-col gap-3">
            <Details board={board} agents={agents.length} people={people.length} record={record} />
            <VisibilityControl board={board} owner={people.some((p) => p.name === me && p.access === "admin")} onChanged={onLifecycle} />
            <LifecycleActions board={board} onChanged={onLifecycle} />
          </div>
        </Section>
      )}
    </div>
  );
}

/** Help is a small "?" that explains a heading in a tooltip, on hover or keyboard focus. */
function Help({ topic, children }: { topic: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          className="help tap inline-flex size-7 shrink-0 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
          aria-label={`About ${topic}`}
        >
          <CircleQuestionMark className="size-3.5" strokeWidth={1.75} aria-hidden />
        </button>
      </TooltipTrigger>
      <TooltipContent side="left" align="start" className="help-text">
        {children}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * Section is a panel section whose heading opens and closes it; the browser remembers
 * which. When reveal names it, it opens, scrolls into view and takes focus.
 */
function Section({ id, title, help, reveal, children }: { id: string; title: string; help?: ReactNode; reveal: Reveal; children: ReactNode }) {
  const [open, setOpen] = usePref(`aboard.open.${id}`, true);
  const section = useRef<HTMLElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (reveal?.section !== id) return;
    setOpen(true);
    const frame = requestAnimationFrame(() => {
      const still = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
      section.current?.scrollIntoView({ block: "start", behavior: still ? "auto" : "smooth" });
      trigger.current?.focus({ preventScroll: true });
    });
    return () => cancelAnimationFrame(frame);
  }, [reveal, id, setOpen]);
  return (
    <Collapsible asChild open={open} onOpenChange={setOpen}>
      <section ref={section} aria-labelledby={id} className="flex scroll-mt-2 flex-col lg:scroll-mt-16">
        <div className="flex items-center gap-1">
          <h3 id={id} className="min-w-0 flex-1 text-meta font-bold text-muted">
            <CollapsibleTrigger
              ref={trigger}
              className="group -ml-2 flex min-h-9 pointer-coarse:min-h-11 w-[calc(100%+0.5rem)] items-center gap-1.5 rounded-[6px] px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
            >
              <ChevronRight
                className="size-3.5 shrink-0 transition-transform duration-200 ease-out group-data-[state=open]:rotate-90"
                strokeWidth={1.75}
                aria-hidden
              />
              {title}
            </CollapsibleTrigger>
          </h3>
          {help && <Help topic={title}>{help}</Help>}
        </div>
        <CollapsibleContent className="pt-1 animate-fade-in">{children}</CollapsibleContent>
      </section>
    </Collapsible>
  );
}

/** Charter shows a board's charter as its author wrote it: paragraphs and lists. */
function Charter({ text }: { text: string }) {
  return (
    <div className="charter flex flex-col gap-2 rounded-box bg-surface px-3.5 py-3">
      {charterBlocks(text).map((b, i) =>
        b.kind === "paragraph" ? (
          <p key={i}>{b.text}</p>
        ) : (
          <ul key={i} className="flex list-disc flex-col gap-1 pl-5 marker:text-muted">
            {b.items.map((item, j) => (
              <li key={j}>{item}</li>
            ))}
          </ul>
        ),
      )}
    </div>
  );
}

type WhosHereProps = {
  board: Board | null;
  members: Member[] | null;
  me: string | null;
  meId: string | null;
  from: string | undefined;
  onPick: (name: string) => void;
  /** onRemoved reloads the board after the person removes an agent. */
  onRemoved: () => void;
  identity: (m: MemberRef) => number;
  latest: (agent: string) => Message | null;
  onShowMessage: (id: string) => void;
  byAgent: boolean;
};

function WhosHere({ board, members, me, meId, from, onPick, onRemoved, identity, latest, onShowMessage, byAgent }: WhosHereProps) {
  const tasksOf = useAgentTasks();
  const listed = (members ?? []).filter((m) => m.kind === "agent");
  // By agent, the agents on a task come first, each keeping the order it joined in.
  const agents = byAgent ? [...listed].sort((a, b) => Number(tasksOf(b.name).length > 0) - Number(tasksOf(a.name).length > 0)) : listed;
  const people = (members ?? []).filter((m) => m.kind === "human");
  const owners = new Set(agents.map((a) => a.owner));
  const showOwner = owners.size > 1;
  return (
    <div className="flex flex-col gap-6">
      {members === null ? (
        <div className="h-16 animate-pulse rounded-control bg-selected motion-reduce:animate-none" aria-label="Loading" />
      ) : agents.length === 0 ? (
        <p>No agents yet.</p>
      ) : (
        <ul className="flex flex-col gap-1" aria-label="Agents">
          {agents.map((a) => (
            <AgentItem
              key={a.id}
              agent={a}
              board={board?.name}
              mine={meId !== null && a.owner_id === meId}
              roleCharter={board?.roles[a.role ?? ""]?.charter}
              showOwner={showOwner}
              picked={from === a.name}
              identity={identity(a)}
              latest={latest(a.name)}
              onPick={() => onPick(a.name)}
              onShowMessage={onShowMessage}
              onRemoved={onRemoved}
              tasks={byAgent ? tasksOf(a.name) : undefined}
            />
          ))}
        </ul>
      )}
      {people.length > 1 && (
        <section aria-labelledby="people">
          <h4 id="people" className="mb-2 text-meta font-bold text-muted">
            People
          </h4>
          <ul className="flex flex-col gap-1.5">
            {people.map((p) => (
              <li key={p.id} className="flex items-baseline justify-between gap-3" data-person={p.name}>
                <NameButton name={p.name} picked={from === p.name} onPick={() => onPick(p.name)}>
                  {p.name}
                  {p.name === me && <span className="font-normal text-muted"> (you)</span>}
                </NameButton>
                <span className="board-role text-meta text-muted">{boardRole(p)}</span>
              </li>
            ))}
          </ul>
        </section>
      )}
    </div>
  );
}

/** boardRole names a person's place on the board: a guest of the server, an owner or a member. */
function boardRole(p: Member): string {
  if (p.server_role === "guest") return "Guest";
  return p.access === "admin" ? "Owner" : "Member";
}

/** NameButton is a member's name that filters the timeline to them. */
function NameButton({ name, picked, onPick, children }: { name: string; picked: boolean; onPick: () => void; children: ReactNode }) {
  return (
    <button
      type="button"
      onClick={onPick}
      aria-pressed={picked}
      title={picked ? `Show everyone's messages` : `Show only ${name}'s messages`}
      className={cn(
        "member-filter -mx-2 min-h-9 pointer-coarse:min-h-11 min-w-0 rounded-[6px] px-2 text-left font-bold break-all text-ink transition-colors duration-[140ms] ease-out hover:bg-hover",
        picked && "bg-selected hover:bg-selected underline decoration-ink decoration-2 underline-offset-[5px]",
      )}
    >
      {children}
    </button>
  );
}

function AgentItem({
  agent,
  board,
  mine,
  roleCharter,
  showOwner,
  picked,
  identity,
  latest,
  onPick,
  onShowMessage,
  onRemoved,
  tasks,
}: {
  agent: Member;
  onRemoved: () => void;
  /** tasks are the live tasks the agent is on, shown as chips under it (Work by agent). */
  tasks?: Task[];
  /** board is the board's name, once it is loaded. */
  board?: string;
  /** mine is true for the person's own agent, whose delivery mode they may change. */
  mine: boolean;
  roleCharter?: string;
  showOwner: boolean;
  picked: boolean;
  identity: number;
  /** latest is the agent's newest message on this board that the page has loaded. */
  latest: Message | null;
  onPick: () => void;
  onShowMessage: (id: string) => void;
}) {
  const status = useStatus()(agent);
  // The row turns marigold only for what waits on this person: its session's prompt, or
  // an ask to them. An ask to someone else keeps the marigold dot and its words.
  const waiting = agent.presence === "waiting" || status.word === "waiting on you";
  const [open, setOpen] = useState(false);
  const item = useRef<HTMLLIElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  // The details close on a click elsewhere or on Escape, but not while a menu or dialog
  // opened from them (the delivery menu, Remove's confirmation) is in use.
  useEffect(() => {
    if (!open) return;
    const layer = (el: Element | null) => !!el?.closest('[role="menu"], [role="alertdialog"], [data-radix-popper-content-wrapper]');
    const away = (e: PointerEvent) => {
      const target = e.target as Element;
      if (item.current?.contains(target) || layer(target)) return;
      setOpen(false);
    };
    // A menu that closed on this Escape has already handled it (and marked it), so one
    // Escape closes one layer.
    const esc = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.defaultPrevented || layer(document.activeElement)) return;
      setOpen(false);
      trigger.current?.focus();
    };
    document.addEventListener("pointerdown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("pointerdown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <li ref={item} className={cn("agent relative transition-colors duration-200 ease-out", waiting && "-mx-2 rounded-box bg-attention-soft px-2 py-1.5")} data-agent={agent.name} data-status={status.tone}>
      <button
        ref={trigger}
        type="button"
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => setOpen(!open)}
        title={`${agent.name}'s details`}
        className={cn(
          "agent-row -mx-2 flex min-h-11 w-[calc(100%+1rem)] items-center gap-2 rounded-control px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-hover",
          open && "bg-selected hover:bg-selected",
        )}
      >
        <AgentMark member={agent} identity={identity} size="sm" status={status.tone} />
        <span className={cn("agent-name min-w-0 flex-1 truncate font-bold", picked && "underline decoration-ink decoration-2 underline-offset-[5px]")}>
          {agent.name}
        </span>
        <span className="flex min-w-0 shrink items-center" title={status.sentence}>
          <CrossFade value={status.word} className={cn("text-meta", status.tone === "working" || status.tone === "needs" ? "text-ink" : "text-muted")} />
          <span className="sr-only">. {status.sentence}</span>
        </span>
        <ChevronRight className={cn("size-3.5 shrink-0 text-muted transition-transform duration-200 ease-out", open && "rotate-90")} strokeWidth={1.75} aria-hidden />
      </button>
      <LineText line={agent.line} late={agent.state === "late"} className="-mt-1 pb-1 pl-7" />
      {agent.presence === "waiting" && <p className="text-meta">Its session is waiting for a person, such as at a permission prompt.</p>}
      {tasks && (
        <p className="agent-tasks flex flex-wrap items-baseline gap-1 pb-2.5 pl-7">
          {tasks.length > 0 ? <TaskChips tags={tasks.map((t) => ({ id: t.id, ref: t.ref, how: "given" }))} /> : <span className="text-meta text-muted">on no task</span>}
        </p>
      )}
      {open && (
        <div
          role="dialog"
          aria-label={`${agent.name}'s details`}
          className="agent-popover absolute top-full right-0 left-0 z-20 mt-1 flex animate-fade-in flex-col gap-2 rounded-box border border-field-border bg-surface px-3.5 py-3 shadow-float"
        >
          <p className="flex items-start gap-2 text-meta">
            <StatusDot tone={status.tone} className="mt-[7px]" />
            <span>{status.sentence}</span>
          </p>
          <AgentDetails agent={agent} board={board} mine={mine} roleCharter={roleCharter} showOwner={showOwner} />
          {agent.can_remove === true && board && <RemoveAgent board={board} agent={agent} onRemoved={onRemoved} />}
          <div className="flex flex-col gap-1 border-t border-rule pt-2 text-meta">
            {latest ? (
              <button
                type="button"
                className="latest-message flex min-h-11 flex-col items-start text-left"
                onClick={() => {
                  setOpen(false);
                  onShowMessage(latest.id);
                }}
              >
                <span className="text-link underline decoration-1 underline-offset-[3px]">Latest message on this board</span>
                <span className="line-clamp-2 text-muted">
                  {relativeTime(latest.at, Date.now())}: {latest.body}
                </span>
              </button>
            ) : (
              <span className="text-muted">No messages on this board yet.</span>
            )}
            <button
              type="button"
              aria-pressed={picked}
              onClick={() => {
                setOpen(false);
                onPick();
              }}
              className="member-filter min-h-11 self-start text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
            >
              {picked ? "Show everyone's messages" : "All its messages"}
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

/** AgentDetails is an agent's fields: its owner (with a second person), role, harness and delivery mode. */
export function AgentDetails({
  agent,
  board,
  mine,
  roleCharter,
  showOwner,
}: {
  agent: Member;
  board?: string;
  mine: boolean;
  roleCharter?: string;
  showOwner: boolean;
}) {
  const presence = agent.presence ?? "no_session";
  const label = cn("text-meta", presence === "waiting" ? "text-ink" : "text-muted");
  // The mode its person set, held by the server; a server that holds none shows what
  // the agent's delivery daemon reports applying.
  const held = agent.delivery_mode ?? null;
  const applied = agent.delivery ? appliedMode(agent.delivery) : null;
  const mode = held ?? applied;
  const midturn = useMidturn();
  const mid = mine && midturn.view ? effectiveMidturn(midturn.view, agent.id) : null;
  return (
    <>
    <dl className="mt-1 grid grid-cols-[72px_minmax(0,1fr)] items-baseline gap-x-3 gap-y-1">
      {showOwner && (
        <>
          <dt className={label}>Owner</dt>
          <dd>{agent.owner}</dd>
        </>
      )}
      <dt className={label}>Role</dt>
      <dd>
        <Collapsible>
          <CollapsibleTrigger className="group inline-flex items-center gap-1 rounded-[6px] text-ink hover:underline hover:decoration-1 hover:underline-offset-[3px]">
            {agent.role}
            <ChevronDown
              className="size-3.5 text-muted transition-transform duration-200 ease-out group-data-[state=open]:rotate-180"
              strokeWidth={1.5}
              aria-hidden
            />
            <span className="sr-only">: what this role does</span>
          </CollapsibleTrigger>
          <CollapsibleContent className="animate-fade-in">
            <p className="mt-1 text-meta">{roleCharter?.trim() || "This role has no description."}</p>
          </CollapsibleContent>
        </Collapsible>
      </dd>
      {agent.harness && (
        <>
          <dt className={label}>Harness</dt>
          <dd>{harnessName(agent.harness)}</dd>
        </>
      )}
      {mode && (
        <>
          <dt className={label}>Delivery</dt>
          <dd className="delivery">
            {mine && held && board ? (
              <DeliveryMenu board={board} agent={agent.name} mode={held} />
            ) : (
              <span className="delivery-mode" title={modeRules[mode]}>
                {mode}
              </span>
            )}
            {held && applied && applied !== held && presence !== "no_session" && (
              <p
                className="delivery-applied mt-1 text-meta text-muted"
                title="A delivery daemon from an older aboard keeps the mode on its own machine."
              >
                Its delivery daemon still applies {applied}.
              </p>
            )}
          </dd>
        </>
      )}
      {mid && midturn.view && (
        <>
          <dt className={label}>Mid-turn</dt>
          <dd className="midturn">
            <MidturnMenu agent={agent} view={midturn.view.policy} mid={mid} />
          </dd>
        </>
      )}
    </dl>
    {mine && <AgentLocation agent={agent} board={board} />}
    </>
  );
}

/**
 * MidturnMenu is the person's own agent's mid-turn override: follow the person's setting
 * or choose one for this agent. The trigger shows the policy in force now.
 */
function MidturnMenu({ agent, view, mid }: { agent: Member; view: MidturnPolicy; mid: { policy: MidturnPolicy; overridden: boolean } }) {
  const [error, setError] = useState<string | null>(null);
  const pick = (value: string) => {
    setError(null);
    changeMidturn(value === "default" ? null : (value as MidturnPolicy), agent.id).catch((e: unknown) =>
      setError(e instanceof ApiError ? e.message : "Couldn't change the mid-turn setting."),
    );
  };
  const text = midturnLabels[mid.policy].toLowerCase();
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          className="midturn-menu group inline-flex items-center gap-1 rounded-[6px] text-ink hover:underline hover:decoration-1 hover:underline-offset-[3px] data-[state=open]:underline"
          aria-label={`Mid-turn messages for ${agent.name}: ${text}${mid.overridden ? "" : ", from your setting"}. Change it`}
        >
          {text}
          {!mid.overridden && <span className="text-meta text-muted">(default)</span>}
          <ChevronDown
            className="size-3.5 text-muted transition-transform duration-200 ease-out group-data-[state=open]:rotate-180"
            strokeWidth={1.5}
            aria-hidden
          />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="midturn-options w-[22rem] max-w-[calc(100vw-2rem)]">
          <DropdownMenuLabel>Mid-turn messages for {agent.name}</DropdownMenuLabel>
          <p className="px-3 pb-1 text-meta text-muted">
            Urgent messages from your own agents arrive at its next step instead of waiting for the turn to end.
          </p>
          <DropdownMenuRadioGroup value={mid.overridden ? mid.policy : "default"} onValueChange={pick}>
            <DropdownMenuRadioItem value="default">Default (follows your setting: {midturnLabels[view]})</DropdownMenuRadioItem>
            {midturnPolicies.map((p) => (
              <DropdownMenuRadioItem key={p} value={p}>
                {midturnLabels[p]}
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {error && (
        <p role="alert" className="mt-1 text-meta">
          {error}
        </p>
      )}
    </>
  );
}

/**
 * DeliveryMenu is the person's own agent's delivery mode, as a quiet menu of the four
 * modes, each with the rule the agent is told. Choosing one sets it on the server, so
 * the agent's delivery daemon follows it on whichever machine runs the agent.
 */
function DeliveryMenu({ board, agent, mode }: { board: string; agent: string; mode: SettableMode }) {
  // chosen is the mode picked here until the board's members show it.
  const [chosen, setChosen] = useState<SettableMode | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => setChosen(null), [mode]);
  const current = chosen ?? mode;
  const pick = (value: string) => {
    const next = value as SettableMode;
    if (next === current) return;
    setChosen(next);
    setError(null);
    setDelivery(board, agent, next).then(
      (r) => setChosen(r.mode),
      (e: unknown) => {
        setChosen(null);
        setError(e instanceof ApiError ? e.message : "Couldn't change the delivery mode.");
      },
    );
  };
  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger
          className="delivery-menu group inline-flex items-center gap-1 rounded-[6px] text-ink hover:underline hover:decoration-1 hover:underline-offset-[3px] data-[state=open]:underline"
          aria-label={`Delivery mode of ${agent}: ${current}. Change it`}
        >
          {current}
          <ChevronDown
            className="size-3.5 text-muted transition-transform duration-200 ease-out group-data-[state=open]:rotate-180"
            strokeWidth={1.5}
            aria-hidden
          />
        </DropdownMenuTrigger>
        <DropdownMenuContent align="start" className="delivery-modes w-[22rem] max-w-[calc(100vw-2rem)]">
          <DropdownMenuLabel>When messages wake {agent}</DropdownMenuLabel>
          <p className="px-3 pb-1 text-meta text-muted">Each mode with the rule {agent} is told.</p>
          <DropdownMenuRadioGroup value={current} onValueChange={pick}>
            {settableModes.map((m) => (
              <DropdownMenuRadioItem key={m} value={m} className="items-start">
                <span className="flex flex-col gap-0.5">
                  <span className="font-bold">{m}</span>
                  <span className="text-meta text-muted">{modeRules[m]}</span>
                </span>
              </DropdownMenuRadioItem>
            ))}
          </DropdownMenuRadioGroup>
        </DropdownMenuContent>
      </DropdownMenu>
      {error && (
        <p role="alert" className="mt-1 text-meta">
          {error}
        </p>
      )}
    </>
  );
}

/** CrossFade shows a word that fades into the next one when it changes. */
function CrossFade({ value, className }: { value: string; className?: string }) {
  const [shown, setShown] = useState(value);
  const [leaving, setLeaving] = useState<string | null>(null);
  useEffect(() => {
    if (value === shown) return;
    setLeaving(shown);
    setShown(value);
  }, [value, shown]);
  return (
    <span className={cn("presence grid text-right", className)} aria-live="polite">
      {leaving !== null && (
        <span key={leaving} aria-hidden className="animate-fade-out [grid-area:1/1]" onAnimationEnd={() => setLeaving(null)}>
          {leaving}
        </span>
      )}
      <span key={shown} className={cn("[grid-area:1/1]", leaving !== null && "animate-fade-in")}>
        {shown}
      </span>
    </span>
  );
}
