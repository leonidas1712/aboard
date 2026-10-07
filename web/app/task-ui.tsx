"use client";

import { ArrowLeft, Clock, MessagesSquare } from "lucide-react";
import { createContext, useContext, useEffect, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type Member, type MemberRef, type Message, type Presence as PresenceState, type Task, type TaskList, type TaskTag, ApiError, getTask, listTasks, post } from "./api";
import { AgentMark } from "./agent-mark";
import { Problem } from "./chrome";
import { count, harnessName, presenceWords, relativeTime } from "./words";

type TaskRoom = {
  tasks: Task[];
  open: (ref: string) => void;
  /** members, identity and me let the task views show each agent's mark, harness and presence. */
  members: Member[];
  identity: (m: MemberRef) => number;
  me: string | null;
};

const Tasks = createContext<TaskRoom>({ tasks: [], open: () => {}, members: [], identity: () => 1, me: null });

export function TaskContext({ children, ...room }: TaskRoom & { children: ReactNode }) {
  return <Tasks.Provider value={room}>{children}</Tasks.Provider>;
}

export function useTasks(board: string, activity: number, head: number | undefined, enabled: boolean) {
  const [list, setList] = useState<TaskList | null>(null);
  const [error, setError] = useState<unknown>(null);
  const request = useRef(0);
  useEffect(() => {
    const generation = ++request.current;
    if (!enabled) { setList(null); return; }
    listTasks(board).then((value) => {
      if (generation !== request.current) return;
      setList(value); setError(null);
    }, (e) => { if (generation === request.current) { if (e instanceof ApiError && (e.status === 403 || e.status === 404)) setList(null); setError(e); } });
    return () => { request.current++; };
  }, [board, activity, head, enabled]);
  return { list, error };
}

export function taskState(t: Task): string {
  if (t.state === "open") return "Not picked up";
  if (t.state === "in_progress") return "In progress";
  return t.state === "done" ? "Done" : "Cancelled";
}

export function TaskChips({ tags }: { tags: TaskTag[] | undefined }) {
  const { tasks, open } = useContext(Tasks);
  if (!tags?.length) return null;
  const unique = [...new Map(tags.map((t) => [t.id, t])).values()];
  return <span className="message-tasks inline-flex flex-wrap items-baseline gap-1.5">
    {unique.map((tag) => {
      const task = tasks.find((t) => t.id === tag.id);
      return <Tooltip key={tag.id}><TooltipTrigger asChild>
        <button type="button" aria-label={`Open task ${tag.ref}`} data-task-chip={tag.ref} onClick={() => open(tag.ref)} className="task-chip inline-flex max-w-[240px] items-baseline gap-1 rounded-[6px] border border-rule px-1.5 text-meta font-normal text-muted transition-colors duration-[140ms] ease-out hover:border-field-border hover:text-ink focus-visible:outline-2 focus-visible:outline-accent">
          <span className="shrink-0 tabular-nums">{tag.ref}</span>{task && unique.length === 1 && <span className="truncate">· {task.title}</span>}
        </button>
      </TooltipTrigger><TooltipContent><strong className="block">{tag.ref}{task && ` ${task.title}`}</strong>{task && <span className="block">{taskState(task)}{task.owner && ` · owner ${task.owner.name}`}</span>}<span className="block">Click to open the task</span></TooltipContent></Tooltip>;
    })}
  </span>;
}

// A task's state as one phrase, from the facts the API gives: an open blocking ask to
// the person makes it "Waiting on you"; any other makes it Blocked.
function waitsOnMe(t: Task, me: string | null): boolean {
  return me !== null && (t.blocked_on ?? []).some((b) => b.to.kind === "human" && b.to.name === me);
}
function isLive(t: Task): boolean {
  return t.state === "open" || t.state === "in_progress";
}

/** onTask is everyone on a task, owner first, then its helpers in the order they joined. */
function onTask(t: Task): MemberRef[] {
  return [...(t.owner ? [t.owner] : []), ...t.with.filter((m) => !(t.owner && m.name === t.owner.name && m.kind === t.owner.kind))];
}

/** Who is a member of the task with the details the board's member list adds: harness and presence. */
function useWho() {
  const { members, identity } = useContext(Tasks);
  return (m: MemberRef) => {
    const found = members.find((x) => x.name === m.name && x.kind === m.kind);
    return { ref: m, harness: found?.harness ?? m.harness ?? null, presence: m.kind === "agent" ? (found?.presence ?? "no_session") : null, identity: identity(m) };
  };
}

function Presence({ presence }: { presence: PresenceState }) {
  return (
    <span className={cn("presence inline-flex items-center gap-1.5 text-meta", presence === "working" || presence === "waiting" ? "text-ink" : "text-muted")}>
      <span
        aria-hidden
        className={cn("size-2 shrink-0 rounded-full", presence === "working" ? "bg-accent" : presence === "no_session" ? "border border-muted" : presence === "waiting" ? "bg-ink" : "bg-muted")}
      />
      {presenceWords[presence]}
    </span>
  );
}

/** OwnerLabel is the word "owner" beside a task's owner; its tooltip says what it means. */
function OwnerLabel({ task }: { task: Task }) {
  const opener = task.opened_by.name !== task.owner?.name ? task.opened_by.name : null;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span tabIndex={0} className="owner-label relative z-10 cursor-help text-meta underline decoration-dotted decoration-1 underline-offset-[3px]">
          owner
        </span>
      </TooltipTrigger>
      <TooltipContent side="top" align="start">
        Responsible for the task{opener && `; opened by ${opener}`}
      </TooltipContent>
    </Tooltip>
  );
}

export function WorkTasks({ tasks, open }: { tasks: Task[]; open: (ref: string) => void }) {
  const who = useWho();
  const active = tasks.filter(isLive);
  if (!active.length) return null;
  return <div className="work flex flex-col gap-4" aria-label="Work by task">
    {active.map((t) => <section key={t.id} aria-label={`Task ${t.ref}: ${t.title}`} className="flex flex-col gap-1">
      <h3><button type="button" aria-label={`Open task ${t.ref}`} onClick={() => open(t.ref)} className="group flex min-h-11 w-full items-baseline gap-2 text-left"><span className="shrink-0 text-meta text-muted tabular-nums">{t.ref}</span><span className="min-w-0 flex-1 font-bold group-hover:underline">{t.title}</span></button></h3>
      {onTask(t).length > 0 ? <ul className="flex flex-col gap-1.5">
        {onTask(t).map((m, i) => {
          const w = who(m);
          return <li key={`${m.kind}:${m.name}`} className="flex min-w-0 items-center gap-2 text-meta">
            <AgentMark member={{ ...m, harness: w.harness }} identity={w.identity} size="sm" />
            <span className="truncate">{m.name}{i === 0 && t.owner && <span className="text-muted"> · owner</span>}</span>
            {w.presence && <span className="ml-auto shrink-0"><Presence presence={w.presence} /></span>}
          </li>;
        })}
      </ul> : <p className="text-meta text-muted">Not picked up</p>}
    </section>)}
  </div>;
}

/**
 * TaskBoard lays the board's work out by what the person would act on: Needs you, In
 * progress, Blocked, Not picked up, with the free agents beside the work nobody has
 * taken. Done folds away. Needs you and Blocked come from open blocking asks, so they
 * appear only when a task has one.
 */
export function TaskBoard({ tasks, open }: { tasks: Task[]; open: (ref: string) => void }) {
  const { members, me } = useContext(Tasks);
  const [done, setDone] = useState(false);
  const live = tasks.filter(isLive);
  const needs = live.filter((t) => waitsOnMe(t, me));
  const blocked = live.filter((t) => t.blocked && !waitsOnMe(t, me));
  const free = (t: Task) => !t.blocked;
  const columns = [
    { key: "needs", title: "Needs you", list: needs, always: false },
    { key: "doing", title: "In progress", list: live.filter((t) => free(t) && t.state === "in_progress"), always: true },
    { key: "blocked", title: "Blocked", list: blocked, always: false },
    { key: "open", title: "Not picked up", list: live.filter((t) => free(t) && t.state === "open"), always: true },
  ].filter((c) => c.always || c.list.length > 0);
  const busy = new Set(live.flatMap((t) => onTask(t).map((m) => `${m.kind}:${m.name}`)));
  const idle = members.filter((m) => m.kind === "agent" && (m.status ?? "active") === "active" && !busy.has(`agent:${m.name}`) && (m.presence === "idle" || m.presence === "no_session" || m.presence === null));
  const finished = tasks.filter((t) => !isLive(t));
  return <div className="task-view quiet-scroll min-h-0 flex-1 overflow-y-auto">
    <div className="px-4 pt-4 pb-10 sm:px-6">
      <div className="task-board grid gap-x-4 gap-y-6 sm:grid-cols-2 lg:max-w-[calc(var(--cols)*320px)] lg:grid-cols-[repeat(var(--cols),minmax(0,1fr))]" style={{ "--cols": columns.length } as CSSProperties}>
        {columns.map((c) => <section key={c.key} aria-label={c.title} className="task-column flex min-w-0 flex-col gap-2">
          <h2 id={`tasks-${c.key}`} className="flex min-h-7 items-center gap-2 text-meta font-bold text-ink">
            {c.key === "needs" ? <span className="rounded-[4px] bg-attention px-1.5">{c.title} <span className="tabular-nums">{c.list.length}</span></span> : <>
              <span aria-hidden className={cn("size-2 shrink-0 rounded-full", c.key === "doing" ? "border-2 border-accent" : c.key === "blocked" ? "bg-ink" : "border border-dashed border-muted")} />
              {c.title}<span className="font-normal text-muted tabular-nums">{c.list.length}</span>
            </>}
          </h2>
          <ul className="flex flex-col gap-2">{c.list.map((t) => <li key={t.id}><TaskCard task={t} open={open} needs={c.key === "needs"} /></li>)}</ul>
          {c.list.length === 0 && <p className="text-meta text-muted">{c.key === "doing" ? "Nothing in progress." : "Every task has an owner."}</p>}
          {c.key === "open" && idle.length > 0 && <div className="free-agents mt-2 flex flex-col gap-2 border-t border-rule pt-3">
            <h3 className="text-meta font-bold text-muted">Free agents</h3>
            <ul className="flex flex-col gap-2">{idle.map((a) => <FreeAgent key={a.id} agent={a} />)}</ul>
          </div>}
        </section>)}
      </div>
      {finished.length > 0 && <section aria-label="Done" className="mt-8 flex flex-col gap-2">
        <button type="button" aria-expanded={done} onClick={() => setDone(!done)} className="min-h-11 self-start text-meta text-muted hover:text-ink hover:underline">{count(finished.length, "task", "tasks")} done or cancelled · {done ? "hide" : "show"}</button>
        {done && <ul className="grid animate-fade-in gap-2 sm:grid-cols-2 lg:grid-cols-4">{finished.map((t) => <li key={t.id}><TaskCard task={t} open={open} needs={false} /></li>)}</ul>}
      </section>}
    </div>
  </div>;
}

function FreeAgent({ agent }: { agent: Member }) {
  const { identity } = useContext(Tasks);
  return <li className="flex min-w-0 items-center gap-2" data-free-agent={agent.name}>
    <AgentMark member={agent} identity={identity(agent)} />
    <span className="min-w-0 flex-1 truncate">{agent.name}</span>
    <Presence presence={agent.presence ?? "no_session"} />
  </li>;
}

function TaskCard({ task: t, open, needs }: { task: Task; open: (ref: string) => void; needs: boolean }) {
  const who = useWho();
  const done = !isLive(t);
  const people = onTask(t);
  const ask = t.blocked_on?.[0];
  let line: ReactNode = null;
  if (needs) line = <><span className="font-bold">Waiting on you: </span>{ask?.from ? `${ask.from.name} asked you` : "an ask to you blocks it"}</>;
  else if (t.blocked) line = <><span className="font-bold text-ink">Blocked: </span>{ask ? `${ask.from?.name ?? "someone"} asked ${ask.to.name}` : "an open ask blocks it"}</>;
  else if (t.state === "open") line = `No owner · opened by ${t.opened_by.name} ${relativeTime(t.opened_at, Date.now())}`;
  return (
    // One clickable card: the title's button stretches over the whole card, and the inner
    // controls sit above it, so each keeps its own action and stays reachable.
    <article
      data-task={t.ref}
      data-needs-you={needs || undefined}
      className={cn(
        "task-card relative flex flex-col gap-2.5 rounded-box px-3.5 py-3 transition-colors duration-[140ms] ease-out has-[.card-open:focus-visible]:outline-2 has-[.card-open:focus-visible]:outline-accent",
        needs ? "bg-attention text-ink" : cn("border border-rule hover:border-field-border hover:bg-selected", done ? "bg-transparent" : "bg-surface"),
      )}
    >
      <button type="button" aria-label={`Open task ${t.ref}`} onClick={() => open(t.ref)} className="card-open flex flex-col gap-0.5 text-left outline-none after:absolute after:inset-0 after:rounded-box after:content-['']">
        <span className={cn("text-meta tabular-nums", needs ? "text-ink" : "text-muted")}>{t.ref}{t.state === "cancelled" && " · cancelled"}</span>
        <span className={cn("leading-snug", done ? "font-normal" : "font-bold")}>{t.title}</span>
      </button>
      {line && <p className={cn("text-meta", needs ? "text-ink" : "text-muted")}>{line}</p>}
      {people.length > 0 && <ul className={cn("flex flex-col gap-2 border-t pt-2.5", needs ? "border-ink/20" : "border-rule")}>
        {people.map((m, i) => {
          const w = who(m);
          return <li key={`${m.kind}:${m.name}`} className="flex min-w-0 items-center gap-2" data-on-task={m.name}>
            <AgentMark member={{ ...m, harness: w.harness }} identity={w.identity} />
            <span className="min-w-0 flex-1 truncate">{m.name}{i === 0 && t.owner && <span className={needs ? "" : "text-muted"}> · <OwnerLabel task={t} /></span>}</span>
            {!done && w.presence && <Presence presence={w.presence} />}
          </li>;
        })}
      </ul>}
      <p className={cn("flex flex-wrap items-center gap-x-3 gap-y-1 border-t pt-2 text-meta", needs ? "border-ink/20 text-ink" : "border-rule text-muted")}>
        {t.message_count > 0 && <button type="button" onClick={() => open(t.ref)} className="task-threads relative z-10 inline-flex min-h-7 items-center gap-1 underline decoration-1 underline-offset-[3px] hover:text-ink">
          <MessagesSquare className="size-3.5" strokeWidth={1.75} aria-hidden />{t.message_count} in conversation
        </button>}
        <span className="ml-auto">{done && t.closed_at ? `closed ${relativeTime(t.closed_at, Date.now())}` : relativeTime(t.updated_at, Date.now())}</span>
      </p>
    </article>
  );
}

// Where it stands turns muted, with a clock, once it hasn't been updated for this long.
const staleAfter = 2 * 60 * 60 * 1000;

/** Label is a task panel section's heading, with a tooltip saying what the section is. */
function Label({ id, help, children }: { id: string; help: string; children: ReactNode }) {
  return <Tooltip>
    <TooltipTrigger asChild><h3 id={id} tabIndex={0} className="cursor-help self-start text-meta font-bold text-muted">{children}</h3></TooltipTrigger>
    <TooltipContent side="top" align="start" className="max-w-[280px]">{help}</TooltipContent>
  </Tooltip>;
}

export function TaskDetail({ board, reference, activity, back, narrow, pick, onPosted, readOnly }: {
  board: string; reference: string; activity: number; back: () => void; narrow: (ref: string) => void; pick: (name: string) => void; onPosted: () => void; readOnly: boolean;
}) {
  const { me } = useContext(Tasks);
  const who = useWho();
  const [task, setTask] = useState<Task | null>(null);
  const [error, setError] = useState<unknown>(null);
  const top = useRef<HTMLElement>(null);
  useEffect(() => { setTask(null); setError(null); }, [board, reference]);
  useEffect(() => {
    let live = true;
    getTask(board, reference).then((t) => { if (live) { setTask(t); setError(null); } }, (e) => { if (live) { if (e instanceof ApiError && (e.status === 403 || e.status === 404)) setTask(null); setError(e); } });
    return () => { live = false; };
  }, [board, reference, activity]);
  useEffect(() => { if (window.innerWidth < 1024) top.current?.scrollIntoView({ block: "start" }); }, [reference]);
  const now = Date.now();
  const t = task;
  const needs = t ? waitsOnMe(t, me) : false;
  const people = t ? onTask(t) : [];
  const agentsOn = people.filter((m) => m.kind === "agent").map((m) => m.name);
  const owner = t?.owner?.kind === "agent" ? t.owner.name : null;
  const stale = t?.stands ? now - new Date(t.stands.at).getTime() > staleAfter && isLive(t) : false;
  const suggestions: Suggestion[] = t ? [
    owner ? { label: "Split", to: [owner], text: `${t.ref}: please split this into smaller tasks, each with an owner, and post them here.` } : null,
    { label: "Reassign", to: owner ? [owner] : [], text: `${t.ref}: please hand this to someone free and tell them where it stands.` },
    agentsOn.length > 0 ? { label: "Hold", to: agentsOn, text: `${t.ref}: hold here. Finish what you're on, then wait until I say go.` } : null,
  ].filter((s): s is Suggestion => s !== null) : [];
  const stateText = !t ? "" : needs ? "Waiting on you" : t.blocked ? "Blocked" : taskState(t);
  return <section ref={top} aria-label={`Task ${reference}`} className="task-panel flex scroll-mt-2 flex-col gap-5">
    <button type="button" onClick={back} className="inline-flex min-h-11 items-center gap-1.5 self-start text-meta text-muted hover:text-ink"><ArrowLeft className="size-4" aria-hidden />Work</button>
    {error !== null && <Problem error={error} />}
    {t ? <>
      <header className="flex flex-col gap-1">
        <p className="text-meta text-muted tabular-nums">{t.ref}</p>
        <h2 className="text-title font-bold text-balance">{t.title}</h2>
        <p className="text-meta text-muted">opened by {t.opened_by.name === me ? "you" : t.opened_by.name} · {relativeTime(t.opened_at, now)}</p>
        <p className="task-state flex flex-wrap items-center gap-x-1.5 text-meta">
          {needs ? <span className="rounded-[4px] bg-attention px-1.5 text-ink">{stateText}</span> : <>
            <span aria-hidden className={cn("size-2 shrink-0 rounded-full", !isLive(t) ? "bg-muted" : t.blocked ? "bg-ink" : t.state === "open" ? "border border-dashed border-muted" : "border-2 border-accent")} />
            <span className="text-muted">{stateText}</span>
          </>}
          {t.owner && <span className="text-muted">· <OwnerLabel task={t} /> {t.owner.name === me ? "you" : t.owner.name}</span>}
        </p>
      </header>

      <div className="flex flex-col gap-4">
        <section aria-labelledby={`about-${t.id}`} className="flex flex-col gap-0.5">
          <Label id={`about-${t.id}`} help="What the task is and why, written when it was opened. It rarely changes.">About</Label>
          {t.about ? <>
            <p className="whitespace-pre-wrap break-words"><TaskLinks text={t.about.text} /></p>
            <p className="text-meta text-muted">by {t.about.by.name} · {relativeTime(t.about.at, now)}</p>
          </> : <p className="text-meta text-muted">Nothing written yet.</p>}
        </section>
        <section aria-labelledby={`stands-${t.id}`} className="where-it-stands flex flex-col gap-0.5">
          <Label id={`stands-${t.id}`} help="Where the task is now, in two or three lines. Its owner keeps it current, as the task's own brief.">Where it stands</Label>
          {t.stands ? <>
            <p className={cn("whitespace-pre-wrap break-words", stale && "text-muted")}><TaskLinks text={t.stands.text} /></p>
            <p className="flex flex-wrap items-center gap-x-1.5 text-meta text-muted" data-stale={stale || undefined}>
              {stale && <Clock className="size-3" strokeWidth={2} aria-label="Not updated in a while" />}
              by {t.stands.by.name} · {relativeTime(t.stands.at, now)}{t.stands.messages_since !== undefined && ` · ${count(t.stands.messages_since, "message", "messages")} since`}
            </p>
          </> : <p className="text-meta text-muted">Nothing yet.</p>}
        </section>
      </div>

      {t.closed_note && <section aria-labelledby={`final-${t.id}`} className="flex flex-col gap-0.5"><h3 id={`final-${t.id}`} className="text-meta font-bold text-muted">Final note</h3><p className="whitespace-pre-wrap break-words"><TaskLinks text={t.closed_note} /></p></section>}

      {t.message_count > 0 && <section aria-labelledby={`conversation-${t.id}`} className="flex flex-col gap-1">
        <h3 id={`conversation-${t.id}`} className="text-meta font-bold text-muted">Conversation · <span className="tabular-nums">{t.message_count}</span></h3>
        <p className="text-meta text-muted">{count(t.message_count, "message", "messages")} about {t.ref}{t.thread_count > 0 && `, in ${count(t.thread_count, "thread", "threads")}`}</p>
        <button type="button" onClick={() => narrow(t.ref)} className="min-h-11 self-start text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline">Show only {t.ref} in the conversation</button>
      </section>}

      {people.length > 0 && <section aria-labelledby={`on-${t.id}`} className="on-it flex flex-col gap-1.5">
        <h3 id={`on-${t.id}`} className="text-meta font-bold text-muted">On it</h3>
        <ul className="flex flex-col gap-1">
          {people.map((m) => {
            const w = who(m);
            return <li key={`${m.kind}:${m.name}`} className="flex min-h-11 min-w-0 items-center gap-2.5" data-on-task={m.name}>
              <AgentMark member={{ ...m, harness: w.harness }} identity={w.identity} />
              <span className="flex min-w-0 flex-1 flex-wrap items-baseline gap-x-1.5">
                <button type="button" onClick={() => pick(m.name)} title={`Show only ${m.name}'s messages`} className="truncate hover:underline">{m.name === me ? "you" : m.name}</button>
                {t.owner && m.name === t.owner.name && m.kind === t.owner.kind && <span className="text-muted"><OwnerLabel task={t} /></span>}
                {m.kind === "agent" && harnessName(w.harness) && <span className="text-meta text-muted">{harnessName(w.harness)}</span>}
              </span>
              {w.presence ? <Presence presence={w.presence} /> : <span className="text-meta text-muted">person</span>}
            </li>;
          })}
        </ul>
      </section>}

      {isLive(t) && !readOnly && <TellTheTeam key={t.id} board={board} task={t.ref} to={agentsOn} suggestions={suggestions} narrow={narrow} onPosted={onPosted} />}
    </> : error === null && <p className="text-meta text-muted" role="status">Loading task…</p>}
  </section>;
}

type Suggestion = { label: string; to: string[]; text: string };

/**
 * TellTheTeam is a message to the agents on a task, as text the person reads and can
 * change before it goes. Split, Reassign and Hold fill in a suggestion and pick who it
 * goes to; Send posts an ordinary message about the task.
 */
function TellTheTeam({ board, task, to, suggestions, narrow, onPosted }: { board: string; task: string; to: string[]; suggestions: Suggestion[]; narrow: (ref: string) => void; onPosted: () => void }) {
  // Everyone on the task, until a suggestion picks someone else; everyone with no one on it.
  const [picked, setPicked] = useState<string[] | null>(null);
  const recipients = picked ?? to;
  const [text, setText] = useState("");
  const [sent, setSent] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<unknown>(null);
  const key = useRef("");
  const label = recipients.length ? recipients.join(", ") : "everyone";
  if (sent) {
    return <p className="tell-sent text-meta" role="status">
      Sent to {sent}.{" "}
      <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => narrow(task)}>Show it in the conversation</button>
      {" · "}
      <button type="button" className="min-h-11 text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => { setSent(null); setText(""); setPicked(null); }}>Write another</button>
    </p>;
  }
  const send = async (e: FormEvent) => {
    e.preventDefault();
    const body = text.trim();
    if (!body || busy) return;
    if (!key.current) key.current = crypto.randomUUID();
    setBusy(true);
    setProblem(null);
    try {
      await post<Message>(`/v1/boards/${encodeURIComponent(board)}/messages`, { body, to: recipients.length ? recipients.map((r) => `@${r}`) : ["all"], about: [task] }, key.current);
      key.current = "";
      setSent(label);
      onPosted();
    } catch (err) {
      setProblem(err);
    } finally {
      setBusy(false);
    }
  };
  return <form className="tell flex flex-col gap-2 border-t border-rule pt-4" onSubmit={send}>
    <label htmlFor={`tell-${task}`} className="text-meta font-bold text-muted">Tell the team on {task}</label>
    <textarea
      id={`tell-${task}`}
      value={text}
      onChange={(e) => { setText(e.target.value); key.current = ""; }}
      rows={3}
      placeholder={`A message to ${label}`}
      className="w-full resize-y rounded-control border border-field-border bg-surface px-3 py-2 text-ink placeholder:text-muted focus-visible:outline-2 focus-visible:outline-accent"
    />
    <p className="tell-to text-meta text-muted">To {label} · about {task}</p>
    {problem !== null && <Problem error={problem} />}
    <div className="flex flex-wrap items-center gap-2">
      {suggestions.map((s) => <button key={s.label} type="button" onClick={() => { setText(s.text); setPicked(s.to); key.current = ""; }} className="min-h-11 rounded-control border border-rule px-3 text-meta transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected">{s.label}</button>)}
      <button type="submit" disabled={busy || !text.trim()} className="ml-auto min-h-11 rounded-control bg-ink px-5 font-bold text-on-ink transition-opacity duration-[140ms] ease-out disabled:opacity-60">Send</button>
    </div>
  </form>;
}

export function TaskLinks({ text, tags }: { text: string; tags?: TaskTag[] }) {
  const { tasks, open } = useContext(Tasks);
  return text.split(/(\b[A-Z][A-Z0-9]{1,15}-[1-9][0-9]*\b)/).map((part, i) => {
    const task = tasks.find((t) => t.ref === part) ?? tags?.find((t) => t.ref === part);
    return task ? <button key={i} type="button" onClick={() => open(task.ref)} className="font-medium text-link underline decoration-dotted underline-offset-2 hover:decoration-solid" aria-label={`Open task ${task.ref}`}>{part}</button> : part;
  });
}
