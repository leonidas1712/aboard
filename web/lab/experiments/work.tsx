"use client";

// EXPERIMENTAL, lab only: the one side panel. By default it shows the board's Work: its
// tasks, each with the agents on it and what they are doing right now, then the agents
// on no task, Add an agent, and the charter, rules and details folded away. Click an
// agent for its details in a small popover; click a task or a file and it opens here,
// with a way back, so the person never leaves the board. A task shows About (what it is,
// written when opened) and Where it stands (its owner's current note),
// the open question with answer buttons, its files with what you approved, who is on
// it, a box to tell its people something (Split, Reassign and Hold fill in a message you
// read before it goes), and its threads and messages, with a way to narrow the
// conversation to them.

import { ArrowLeft, ChevronRight, Clock } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { boardMessages } from "../fake-api";
import type { ScenarioTask } from "../scenario";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { type Board, type Member, type Message, get, post } from "@/app/api";
import { AddAgent } from "@/app/board-details";
import { AgentDetails } from "@/app/sidebars";
import { usePref } from "@/app/prefs";
import { count, relativeTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { at, filterTo, groupWork, narrowTo, openArtifact, openTask, openThread, scenario, showWork, useLab, useUi } from "../store";
import { ArtifactPanel, FileIcon, approval } from "./artifacts";
import { agentState, answer, asksOf, stateDot, statusOf, toneClass } from "./asks";
import { Mark, active, ago, minutesSince, onBoard, staleAfter, useNow } from "./common";
import { OwnerLabel, TaskChip, ThreadList, linkCount } from "./chips";
import { latestFrom, tasksOf } from "./links";
import { Ask } from "./ask";
import { blockerOf, needsYou } from "./tasks";
import { Ids } from "./text";

const back = "inline-flex min-h-9 items-center gap-1.5 rounded-control text-meta font-normal text-muted hover:text-ink";

/** WorkBy is how the Work panel groups the board: by task, or one row per agent. */
export type WorkBy = "task" | "agent";

/** Title heads the side panel: Work and how it is grouped, or the way back to it. */
export function Title({ board, fallback }: { board: string; fallback: string }) {
  const { panel, workBy: by } = useUi();
  if (!onBoard(board)) return <>{fallback}</>;
  if (panel.kind === "work") {
    return (
      <span className="flex items-baseline gap-1.5">
        <span className="text-ink">Work</span>
        <span className="font-normal text-muted">·</span>
        <span role="group" aria-label="Group the work" className="inline-flex gap-0.5 font-normal">
          {(["task", "agent"] as WorkBy[]).map((b) => (
            <button
              key={b}
              type="button"
              aria-pressed={by === b}
              onClick={() => groupWork(b)}
              className={cn("rounded-[5px] px-1.5", by === b ? "bg-selected font-bold text-ink" : "text-muted hover:text-ink")}
            >
              by {b}
            </button>
          ))}
        </span>
      </span>
    );
  }
  const from = panel.kind === "artifact" ? panel.from : null;
  return (
    <button type="button" className={back} onClick={() => (from ? openTask(from) : showWork())}>
      <ArrowLeft className="size-3.5" strokeWidth={1.75} aria-hidden />
      {from ?? "Work"}
    </button>
  );
}

export function WorkPanel({ board, members, pick, boardPanel }: { board: string; members: Member[]; pick: (name: string) => void; boardPanel: ReactNode }) {
  const { panel, n } = useUi();
  const top = useRef<HTMLDivElement>(null);
  // On a narrow screen the panel sits below the conversation: bring an opened task or file into view.
  useEffect(() => {
    if (panel.kind !== "work" && window.innerWidth < 1024) top.current?.scrollIntoView({ behavior: "smooth", block: "start" });
  }, [panel, n]);
  if (!onBoard(board)) return <>{boardPanel}</>;
  return (
    <div ref={top} className="lab-work flex scroll-mt-2 flex-col gap-6">
      {panel.kind === "task" ? (
        <TaskPanel id={panel.id} members={members} pick={pick} />
      ) : panel.kind === "artifact" ? (
        <ArtifactPanel id={panel.id} />
      ) : (
        <Work members={members} />
      )}
      {panel.kind === "work" && (
        <>
          <People members={members} />
          {boardPanel}
        </>
      )}
    </div>
  );
}

function Work({ members }: { members: Member[] }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const { workBy: by } = useUi();
  const [board, setBoard] = useState<Board | null>(null);
  useEffect(() => {
    get<Board>(`/v1/boards/${encodeURIComponent(scenario.board.name)}`).then(setBoard, () => {});
  }, []);
  const asks = asksOf(snap, answered);
  const agents = members.filter((m) => m.kind === "agent");
  const live = snap.tasks.filter(active).sort((a, b) => Number(needsYou(b, asks)) - Number(needsYou(a, asks)) || b.t - a.t);
  const busy = new Set(live.flatMap((t) => [t.owner, ...(t.with ?? [])]));
  const rest = agents.filter((a) => !busy.has(a.name));
  const showOwner = new Set(agents.map((x) => x.owner)).size > 1;
  const row = (a: Member, tasks?: string[]) => {
    const s = statusOf(snap, a.name, asks, now);
    return <AgentRow key={a.id} agent={a} status={s.text} tone={toneClass[s.tone]} showOwner={showOwner} tasks={tasks} />;
  };
  return (
    <div className="work flex flex-col gap-5">
      {/* The way to bring an agent in, first: the panel lists who works here. */}
      {board && <AddAgent board={board} />}
      {by === "agent" ? (
        <ul className="flex flex-col gap-1" aria-label="Agents">
          {[...agents]
            .sort((a, b) => Number(busy.has(b.name)) - Number(busy.has(a.name)) || a.name.localeCompare(b.name))
            .map((a) =>
              row(
                a,
                live.filter((t) => t.owner === a.name || t.with?.includes(a.name)).map((t) => t.id),
              ),
            )}
        </ul>
      ) : (
        <>
          {live.map((t) => {
            const on = [t.owner, ...(t.with ?? [])].map((n) => agents.find((a) => a.name === n)).filter((a): a is Member => !!a);
            const people = [t.owner, ...(t.with ?? [])].filter((n) => scenario.people.some((p) => p.name === n));
            return (
              <section key={t.id} aria-label={`Task ${t.id}: ${t.title}`} className="flex flex-col gap-1.5">
                <h3>
                  <button type="button" onClick={() => openTask(t.id)} className="group flex w-full items-baseline gap-2 text-left" title={`Open task ${t.id}`}>
                    <span className="shrink-0 text-meta text-muted tabular-nums">{t.id}</span>
                    <span className="min-w-0 flex-1">
                      <span className="font-bold group-hover:underline">{t.title}</span>
                      {needsYou(t, asks) && <span className="ml-1.5 inline-block rounded-[4px] bg-attention px-1.5 text-meta text-ink">needs you</span>}
                    </span>
                  </button>
                </h3>
                <ul className="flex flex-col gap-0.5">{on.map((a) => row(a))}</ul>
                {people.length > 0 && <p className="pl-8 text-meta text-muted">with {people.map((p) => (p === scenario.me ? "you" : p)).join(", ")}</p>}
              </section>
            );
          })}
          {rest.length > 0 && (
            <section aria-label="Not on a task" className="flex flex-col gap-1.5">
              <h3 className="text-meta font-bold text-muted">{live.length > 0 ? "Not on a task" : "Agents"}</h3>
              <ul className="flex flex-col gap-0.5">{rest.map((a) => row(a))}</ul>
            </section>
          )}
        </>
      )}
    </div>
  );
}

/**
 * People is who is on the board, for reference: mark, name and their place on it. It is
 * folded away like the charter and rules, and says nothing about tasks or threads.
 */
function People({ members }: { members: Member[] }) {
  const [open, setOpen] = usePref("aboard.open.lab-people", false);
  const people = members.filter((m) => m.kind === "human");
  if (people.length === 0) return null;
  const place = (p: Member) => (p.server_role === "guest" ? "guest" : p.access === "admin" ? "owner" : "member");
  return (
    <section aria-labelledby="lab-people" className="flex flex-col">
      <h3 id="lab-people" className="text-meta font-bold text-muted">
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="group -ml-2 flex min-h-9 w-[calc(100%+0.5rem)] items-center gap-1.5 rounded-[6px] px-2 text-left transition-colors duration-[140ms] ease-out hover:bg-selected hover:text-ink"
        >
          <ChevronRight className={cn("size-3.5 shrink-0 transition-transform duration-200 ease-out", open && "rotate-90")} strokeWidth={1.75} aria-hidden />
          People <span className="font-normal tabular-nums">{people.length}</span>
        </button>
      </h3>
      {open && (
        <ul className="flex animate-fade-in flex-col gap-1.5 pt-1">
          {people.map((p) => (
            <li key={p.id} data-person={p.name} className="grid grid-cols-[20px_minmax(0,1fr)_auto] items-center gap-x-2">
              <Mark name={p.name} />
              <span className="truncate">
                {p.name}
                {p.name === scenario.me && <span className="text-muted"> (you)</span>}
              </span>
              <span className="text-meta text-muted">{place(p)}</span>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * AgentRow is one agent: mark, name and what it is doing now, and by agent the tasks it
 * is on. Its name opens a popover with its details, its latest message on the board and
 * a way to narrow the conversation to it.
 */
function AgentRow({ agent, status, tone, showOwner, tasks }: { agent: Member; status: string; tone: string; showOwner: boolean; tasks?: string[] }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLLIElement>(null);
  const { agentOpen, n: asked } = useUi();
  // A click on the agent elsewhere (a task card) opens its popover here.
  useEffect(() => {
    if (agentOpen !== agent.name) return;
    setOpen(true);
    ref.current?.scrollIntoView({ block: "nearest", behavior: "smooth" });
  }, [agentOpen, asked, agent.name]);
  useEffect(() => {
    if (!open) return;
    const away = (e: MouseEvent) => {
      if (!ref.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    document.addEventListener("mousedown", away);
    document.addEventListener("keydown", esc);
    return () => {
      document.removeEventListener("mousedown", away);
      document.removeEventListener("keydown", esc);
    };
  }, [open]);
  const { snap } = useLab();
  const state = agentState(snap, agent.name, Date.now());
  const latest = open ? latestFrom(agent.name) : undefined;
  const now = useNow();
  return (
    <li ref={ref} data-agent={agent.name} className="relative">
      <button
        type="button"
        aria-expanded={open}
        aria-haspopup="dialog"
        onClick={() => setOpen(!open)}
        className="group grid w-full grid-cols-[20px_minmax(0,1fr)] items-start gap-x-2 rounded-control px-1 py-1 text-left hover:bg-selected"
        title={`${agent.name}: ${status}`}
      >
        <span className="relative mt-0.5">
          <Mark name={agent.name} />
          <span
            aria-hidden
            className={cn(
              "absolute -right-0.5 -bottom-0.5 size-2 rounded-full border-2 border-sidebar",
              stateDot[state],
            )}
          />
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="flex items-center gap-1">
            <span className="truncate">{agent.name}</span>
            <span className="agent-state text-meta text-muted">{state}</span>
            <ChevronRight className={cn("size-3 shrink-0 text-muted transition-[transform,opacity] duration-200", open ? "rotate-90 opacity-100" : "opacity-0 group-hover:opacity-100")} strokeWidth={1.75} aria-hidden />
          </span>
          <span className={cn("line-clamp-2 text-meta", tone)}>
            {tone.includes("late") && <Clock className="mr-1 inline size-3 -translate-y-px" strokeWidth={2} aria-label="Late or idle" />}
            {status}
          </span>
        </span>
      </button>
      {tasks && (
        <p className="flex flex-wrap gap-1 pb-1 pl-8">
          {tasks.length > 0 ? tasks.map((id) => <TaskChip key={id} id={id} short={tasks.length > 1} />) : <span className="text-meta text-muted">on no task</span>}
        </p>
      )}
      {open && (
        <div role="dialog" aria-label={`${agent.name}'s details`} className="agent-popover absolute top-full right-0 left-0 z-20 mt-1 flex animate-fade-in flex-col gap-2 rounded-box border border-field-border bg-surface px-3.5 py-3">
          <AgentDetails agent={agent} board={scenario.board.name} mine={agent.owner === scenario.me} roleCharter={scenario.board.roles?.[agent.role ?? ""]} showOwner={showOwner} />
          <div className="flex flex-col gap-1 border-t border-rule pt-2 text-meta">
            {latest ? (
              <button
                type="button"
                className="flex flex-col items-start text-left"
                onClick={() => {
                  setOpen(false);
                  openThread(latest.id);
                }}
              >
                <span className="text-link underline decoration-1 underline-offset-[3px]">Latest message on this board</span>
                <span className="line-clamp-2 text-muted">
                  {relativeTime(new Date(latest.at).toISOString(), now)}: {latest.body}
                </span>
              </button>
            ) : (
              <span className="text-muted">No messages on this board yet</span>
            )}
            <button
              type="button"
              className="min-h-8 self-start text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
              onClick={() => {
                setOpen(false);
                narrowTo(agent.name);
              }}
            >
              All its messages
            </button>
          </div>
        </div>
      )}
    </li>
  );
}

/** Label is a task panel section's heading, with a short tooltip saying what it is. */
function Label({ id, help, children }: { id: string; help: string; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <h4 id={id} tabIndex={0} className="cursor-help self-start text-meta font-bold text-muted">
          {children}
        </h4>
      </TooltipTrigger>
      <TooltipContent side="top" align="start" className="max-w-[280px]">
        {help}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * WhereItStands is the task's own little brief: two or three lines its owner keeps
 * current, with a byline and how much happened since. Past staleAfter it turns muted
 * with a clock; anyone can ask the owner to update it.
 */
function WhereItStands({ task: t }: { task: ScenarioTask }) {
  const { snap } = useLab();
  const now = useNow();
  const owner = t.owner && !scenario.people.some((p) => p.name === t.owner) ? t.owner : null;
  const messages = t.note ? boardMessages().filter((m) => m.at > at(t.note!.t) && tasksOf(m, snap).includes(t.id)).length : 0;
  const stale = t.note ? minutesSince(t.note.t, now) > staleAfter : false;
  return (
    <section aria-labelledby={`stands-${t.id}`} className="flex flex-col gap-0.5">
      <Label id={`stands-${t.id}`} help="Where the task is now, in two or three lines. Its owner keeps it current, as the task's own brief.">
        Where it stands
      </Label>
      {t.note ? (
        <>
          <p className={cn(stale && "text-muted")}>
            <Ids text={t.note.text} />
          </p>
          <p className="flex flex-wrap items-center gap-x-1.5 text-meta text-muted">
            {stale && <Clock className="size-3" strokeWidth={2} aria-label="Not updated in a while" />}
            Updated by {t.note.by === scenario.me ? "you" : t.note.by} · {ago(t.note.t, now)} · {count(messages, "message", "messages")} since
            {owner && (
              <>
                {" · "}
                <Ask label={`Ask ${owner} to update`} to={owner} text={`${t.id}: please update where it stands (aboard task note ${t.id} "…").`} className="min-h-0" />
              </>
            )}
          </p>
        </>
      ) : (
        <p className="text-meta text-muted">
          Nothing yet.{" "}
          {owner && <Ask label={`Ask ${owner} to write it`} to={owner} text={`${t.id}: please say where it stands (aboard task note ${t.id} "…").`} className="min-h-0" />}
        </p>
      )}
    </section>
  );
}

type Suggestion = { label: string; to: string[]; text: string };

function TaskPanel({ id, members, pick }: { id: string; members: Member[]; pick: (name: string) => void }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const t = snap.tasks.find((x) => x.id === id);
  if (!t) return <p className="text-muted">There is no task {id} on this board.</p>;
  const asks = asksOf(snap, answered);
  const ask = asks.find((a) => a.task === t.id && !a.ahead && a.board === scenario.board.name);
  const needs = needsYou(t, asks);
  const files = snap.artifacts.filter((a) => a.task === t.id);
  const on = [t.owner, ...(t.with ?? [])].filter((n): n is string => !!n);
  const links = linkCount(snap, t.id);
  const agentsOn = on.filter((n) => members.some((m) => m.kind === "agent" && m.name === n));
  const owner = t.owner && agentsOn.includes(t.owner) ? t.owner : null;
  const suggestions: Suggestion[] = [
    owner && { label: "Split it", to: [owner], text: `${t.id}: please split this into smaller tasks, each with an owner, and post them here.` },
    { label: "Reassign", to: owner ? [owner] : [scenario.steward ?? "all"], text: `${t.id}: please hand this to someone free and tell them where it stands.` },
    agentsOn.length > 0 && { label: "Hold", to: agentsOn, text: `${t.id}: hold here. Finish what you're on, then wait until I say go.` },
  ].filter((s): s is Suggestion => !!s);
  const blocker = blockerOf(t);
  const stateText = needs ? "Waiting on you" : blocker ? `Blocked: ${blocker.from || "its owner"} asked ${blocker.on}` : t.state === "open" ? "Not picked up" : t.state === "claimed" ? "Claimed, not started" : t.state === "done" ? "Done" : "In progress";
  return (
    <article className="task-panel flex flex-col gap-5" aria-label={`${t.id} ${t.title}`}>
      <header className="flex flex-col gap-1">
        <p className="text-meta text-muted tabular-nums">{t.id}</p>
        <h3 className="text-title font-bold">{t.title}</h3>
        <p className="text-meta text-muted">
          opened by {(t.by ?? scenario.steward) === scenario.me ? "you" : (t.by ?? scenario.steward ?? "someone")} · {ago(snap.opened[t.id] ?? t.t, now)}
        </p>
        <p className="flex items-center gap-1.5 text-meta">
          {needs ? (
            <span className="rounded-[4px] bg-attention px-1.5 text-ink">{stateText}</span>
          ) : (
            <>
              <span aria-hidden className={cn("size-2 rounded-full", t.state === "done" ? "bg-muted" : "border-2 border-accent")} />
              <span className="text-muted">{stateText}</span>
            </>
          )}
          {t.owner && (
            <span className="text-muted">
              · <OwnerLabel task={t} /> {t.owner === scenario.me ? "you" : t.owner}
            </span>
          )}
        </p>
      </header>

      {(t.about || t.note) && (
        <div className="flex flex-col gap-4">
          {t.about && (
            <section aria-labelledby={`about-${t.id}`} className="flex flex-col gap-0.5">
              <Label id={`about-${t.id}`} help="What the task is and why, written when it was opened. It rarely changes.">
                About
              </Label>
              <p>
                <Ids text={t.about} />
              </p>
            </section>
          )}
          <WhereItStands task={t} />
        </div>
      )}

      {ask && (
        <section aria-label="The open question" className="flex flex-col gap-2">
          <h4 className="font-bold">{ask.question}</h4>
          <p className="text-meta text-muted">{ask.from} asks you</p>
          <div className="flex flex-wrap gap-2">
            {ask.options.map((o, i) => (
              <button
                key={o}
                type="button"
                onClick={() => void answer(ask, o)}
                className="inline-flex min-h-10 items-center gap-2 rounded-control border border-rule bg-surface px-3 text-left hover:border-field-border hover:bg-selected"
              >
                <kbd className="inline-flex size-5 items-center justify-center rounded-[4px] border border-rule font-sans text-[12px] text-muted">{i + 1}</kbd>
                {o}
              </button>
            ))}
          </div>
        </section>
      )}

      {links && (
        <section aria-label={`Conversation about ${t.id}`} className="flex flex-col gap-1">
          <div className="flex flex-wrap items-baseline justify-between gap-x-3">
            <h4 className="text-meta font-bold text-muted">Conversation · {links}</h4>
            <button type="button" className="min-h-8 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => filterTo(t.id)}>
              Show only {t.id} in the conversation
            </button>
          </div>
          <ThreadList task={t.id} className="-mx-1.5" />
        </section>
      )}

      {files.length > 0 && (
        <section aria-label="Artifacts" className="flex flex-col gap-1.5">
          <h4 className="text-meta font-bold text-muted">Artifacts</h4>
          <ul className="flex flex-col gap-1.5">
            {files.map((a) => {
              const ok = approval(a);
              return (
                <li key={a.id}>
                  <button
                    type="button"
                    onClick={() => openArtifact(a.id, t.id)}
                    className="flex w-full items-center gap-2.5 rounded-control border border-rule bg-surface px-3 py-2.5 text-left hover:border-field-border"
                  >
                    <FileIcon a={a} />
                    <span className="flex min-w-0 flex-1 flex-col">
                      <span className="truncate font-bold">
                        {a.name} <span className="font-normal text-muted">v{a.version}</span>
                      </span>
                      <span className={cn("text-meta", ok?.changed ? "font-bold text-ink" : "text-muted")}>{ok ? ok.text : `${a.by} · ${ago(a.t, now)}`}</span>
                    </span>
                    {ok?.changed && <span className="text-meta text-link">Review</span>}
                  </button>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {on.length > 0 && (
        <section aria-label="On it" className="flex flex-col gap-1.5">
          <h4 className="text-meta font-bold text-muted">On it</h4>
          <ul className="flex flex-col gap-1">
            {on.map((n) => {
              const human = scenario.people.some((p) => p.name === n);
              const s = human ? { text: n === scenario.me ? "you" : "person", tone: "quiet" as const } : statusOf(snap, n, asks, now);
              return (
                <li key={n} className="grid grid-cols-[20px_minmax(0,1fr)] items-baseline gap-x-2">
                  <Mark name={n} />
                  <span className="flex min-w-0 flex-col">
                    <button type="button" className="self-start hover:underline" onClick={() => pick(n)} title={`Show only ${n}'s messages`}>
                      {n}
                      {n === t.owner && (
                        <>
                          {" "}
                          <OwnerLabel task={t} />
                        </>
                      )}
                      {!human && <span className="text-meta text-muted"> · {agentState(snap, n, now)}</span>}
                    </button>
                    <span className={cn("text-meta", toneClass[s.tone])}>
                      {s.tone === "late" && <Clock className="mr-1 inline size-3 -translate-y-px" strokeWidth={2} aria-label="Late or idle" />}
                      {s.text}
                    </span>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {t.state !== "done" && <TellTheTeam key={t.id} task={t.id} to={agentsOn} suggestions={suggestions} />}

    </article>
  );
}

/**
 * TellTheTeam is a message to everyone on a task, as text you can read and change
 * before it goes. Split, Reassign and Hold fill in a suggestion and pick who it goes to.
 */
function TellTheTeam({ task, to, suggestions }: { task: string; to: string[]; suggestions: Suggestion[] }) {
  // Everyone on the task, until a suggestion picks someone else.
  const [picked, setRecipients] = useState<string[] | null>(null);
  const recipients = picked ?? (to.length ? to : ["all"]);
  const [text, setText] = useState("");
  const [sent, setSent] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);
  const label = recipients.includes("all") ? "everyone" : recipients.join(", ");
  if (sent) {
    return (
      <p className="text-meta">
        Sent to {label}.{" "}
        <button type="button" className="text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(sent.id)}>
          See it in the conversation
        </button>{" "}
        ·{" "}
        <button
          type="button"
          className="text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
          onClick={() => {
            setSent(null);
            setText("");
            setRecipients(null);
          }}
        >
          Write another
        </button>
      </p>
    );
  }
  return (
    <form
      className="tell flex flex-col gap-2"
      onSubmit={async (e) => {
        e.preventDefault();
        if (!text.trim()) return;
        setBusy(true);
        try {
          const body = text.includes(task) ? text.trim() : `${task}: ${text.trim()}`;
          const m = await post<Message>(`/v1/boards/${encodeURIComponent(scenario.board.name)}/messages`, {
            body,
            to: recipients.includes("all") ? ["all"] : recipients.map((r) => `@${r}`),
          });
          setSent(m);
        } finally {
          setBusy(false);
        }
      }}
    >
      <label htmlFor={`tell-${task}`} className="text-meta font-bold text-muted">
        Tell the team on {task}
      </label>
      <textarea
        id={`tell-${task}`}
        value={text}
        onChange={(e) => setText(e.target.value)}
        rows={3}
        placeholder={`A message to ${label}`}
        className="w-full resize-y rounded-control border border-field-border bg-surface px-3 py-2 text-ink placeholder:text-muted"
      />
      <p className="text-meta text-muted">To {label}</p>
      <div className="flex flex-wrap items-center gap-2">
        {suggestions.map((s) => (
          <button
            key={s.label}
            type="button"
            onClick={() => {
              setText(s.text);
              setRecipients(s.to);
            }}
            className="min-h-9 rounded-control border border-rule px-3 text-meta hover:border-field-border hover:bg-selected"
          >
            {s.label}
          </button>
        ))}
        <button type="submit" disabled={busy || !text.trim()} className="ml-auto min-h-9 rounded-control bg-ink px-3.5 font-bold text-on-ink disabled:opacity-60">
          Send
        </button>
      </div>
    </form>
  );
}
