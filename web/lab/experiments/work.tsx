"use client";

// EXPERIMENTAL, lab only: the one side panel. By default it shows the board's Work: its
// tasks, each with the agents on it and what they are doing right now, then the agents
// on no task, Add an agent, and the charter, rules and details folded away. Click an
// agent for its details in a small popover; click a task or a file and it opens here,
// with a way back, so the person never leaves the board. A task shows its owner's note,
// the open question with answer buttons, its files with what you approved, who is on
// it, a box to tell its people something (Split, Reassign and Hold fill in a message you
// read before it goes), and every message that mentions it.

import { ArrowLeft, ChevronRight } from "lucide-react";
import { type ReactNode, useEffect, useRef, useState } from "react";
import { type Board, type Member, type Message, get, post } from "@/app/api";
import { AddAgent } from "@/app/board-details";
import { AgentDetails } from "@/app/sidebars";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import { mentioning, rootOf } from "../scenario";
import { openArtifact, openTask, openThread, scenario, showWork, useLab, useUi } from "../store";
import { ArtifactPanel, FileIcon, approval } from "./artifacts";
import { answer, asksOf, statusOf, toneClass } from "./asks";
import { Mark, active, ago, onBoard, useNow } from "./common";
import { needsYou } from "./tasks";
import { Ids } from "./text";

const back = "inline-flex min-h-9 items-center gap-1.5 rounded-control text-meta font-normal text-muted hover:text-ink";

/** Title heads the side panel: Work, or the way back to it. */
export function Title({ board, fallback }: { board: string; fallback: string }) {
  const { panel } = useUi();
  if (!onBoard(board)) return <>{fallback}</>;
  if (panel.kind === "work") return <span className="text-ink">Work</span>;
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
    <div ref={top} className="flex scroll-mt-2 flex-col gap-6">
      {panel.kind === "task" ? (
        <TaskPanel id={panel.id} members={members} pick={pick} />
      ) : panel.kind === "artifact" ? (
        <ArtifactPanel id={panel.id} />
      ) : (
        <Work members={members} pick={pick} />
      )}
      {panel.kind === "work" && boardPanel}
    </div>
  );
}

function Work({ members, pick }: { members: Member[]; pick: (name: string) => void }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const [board, setBoard] = useState<Board | null>(null);
  useEffect(() => {
    get<Board>(`/v1/boards/${encodeURIComponent(scenario.board.name)}`).then(setBoard, () => {});
  }, []);
  const asks = asksOf(snap, answered);
  const agents = members.filter((m) => m.kind === "agent");
  const live = snap.tasks.filter(active).sort((a, b) => Number(needsYou(b, asks)) - Number(needsYou(a, asks)) || b.t - a.t);
  const busy = new Set(live.flatMap((t) => [t.owner, ...(t.with ?? [])]));
  const rest = agents.filter((a) => !busy.has(a.name));
  const row = (a: Member) => {
    const s = statusOf(snap, a.name, asks, now);
    return <AgentRow key={a.id} agent={a} status={s.text} tone={toneClass[s.tone]} pick={pick} showOwner={new Set(agents.map((x) => x.owner)).size > 1} />;
  };
  return (
    <div className="work flex flex-col gap-5">
      {live.map((t) => {
        const on = [t.owner, ...(t.with ?? [])].map((n) => agents.find((a) => a.name === n)).filter((a): a is Member => !!a);
        const people = [t.owner, ...(t.with ?? [])].filter((n) => scenario.people.some((p) => p.name === n));
        return (
          <section key={t.id} aria-label={`${t.id} ${t.title}`} className="flex flex-col gap-1.5">
            <h3>
              <button type="button" onClick={() => openTask(t.id)} className="group flex w-full items-baseline gap-2 text-left">
                <span className="shrink-0 text-meta text-muted tabular-nums">{t.id}</span>
                <span className="min-w-0 flex-1 font-bold group-hover:underline">{t.title}</span>
                {needsYou(t, asks) && <span className="size-2 shrink-0 rounded-full bg-[var(--needs)]" aria-label="Waiting on you" />}
              </button>
            </h3>
            <ul className="flex flex-col gap-0.5">{on.map(row)}</ul>
            {people.length > 0 && <p className="pl-8 text-meta text-muted">with {people.map((p) => (p === scenario.me ? "you" : p)).join(", ")}</p>}
          </section>
        );
      })}
      {rest.length > 0 && (
        <section aria-label="Not on a task" className="flex flex-col gap-1.5">
          <h3 className="text-meta font-bold text-muted">{live.length > 0 ? "Not on a task" : "Agents"}</h3>
          <ul className="flex flex-col gap-0.5">{rest.map(row)}</ul>
        </section>
      )}
      {board && <AddAgent board={board} />}
    </div>
  );
}

/** AgentRow is one agent: mark, name and what it is doing now. Its name opens its details. */
function AgentRow({ agent, status, tone, pick, showOwner }: { agent: Member; status: string; tone: string; pick: (name: string) => void; showOwner: boolean }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLLIElement>(null);
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
  const presence = agent.presence ?? "no_session";
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
              presence === "working" ? "bg-accent" : presence === "idle" ? "bg-muted" : presence === "waiting" ? "bg-ink" : "bg-sidebar",
            )}
          />
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="flex items-center gap-1">
            <span className="truncate">{agent.name}</span>
            <ChevronRight className={cn("size-3 shrink-0 text-muted transition-[transform,opacity] duration-200", open ? "rotate-90 opacity-100" : "opacity-0 group-hover:opacity-100")} strokeWidth={1.75} aria-hidden />
          </span>
          <span className={cn("line-clamp-1 text-meta", tone)}>{status}</span>
        </span>
      </button>
      {open && (
        <div role="dialog" aria-label={`${agent.name}'s details`} className="agent-popover absolute top-full right-0 left-0 z-20 mt-1 flex animate-fade-in flex-col gap-2 rounded-box border border-field-border bg-surface px-3.5 py-3">
          <p className="text-meta text-muted">{status}</p>
          <AgentDetails agent={agent} board={scenario.board.name} mine={agent.owner === scenario.me} roleCharter={scenario.board.roles?.[agent.role ?? ""]} showOwner={showOwner} />
          <button
            type="button"
            className="min-h-8 self-start text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline"
            onClick={() => {
              setOpen(false);
              pick(agent.name);
            }}
          >
            Show only {agent.name}&apos;s messages
          </button>
        </div>
      )}
    </li>
  );
}

type Suggestion = { label: string; to: string[]; text: string };

function TaskPanel({ id, members, pick }: { id: string; members: Member[]; pick: (name: string) => void }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const [showMentions, setShowMentions] = useState(false);
  const t = snap.tasks.find((x) => x.id === id);
  if (!t) return <p className="text-muted">There is no task {id} on this board.</p>;
  const asks = asksOf(snap, answered);
  const ask = asks.find((a) => a.task === t.id && !a.ahead && a.board === scenario.board.name);
  const needs = needsYou(t, asks);
  const files = snap.artifacts.filter((a) => a.task === t.id);
  const on = [t.owner, ...(t.with ?? [])].filter((n): n is string => !!n);
  const mentions = mentioning(snap.messages, t.id);
  const agentsOn = on.filter((n) => members.some((m) => m.kind === "agent" && m.name === n));
  const owner = t.owner && agentsOn.includes(t.owner) ? t.owner : null;
  const suggestions: Suggestion[] = [
    owner && { label: "Split it", to: [owner], text: `${t.id}: please split this into smaller tasks, each with an owner, and post them here.` },
    { label: "Reassign", to: owner ? [owner] : [scenario.steward ?? "all"], text: `${t.id}: please hand this to someone free and tell them where it stands.` },
    agentsOn.length > 0 && { label: "Hold", to: agentsOn, text: `${t.id}: hold here. Finish what you're on, then wait until I say go.` },
  ].filter((s): s is Suggestion => !!s);
  const stateText = needs ? "Waiting on you" : t.state === "open" ? "Not picked up" : t.state === "claimed" ? "Claimed, not started" : t.state === "waiting" ? `Waiting on ${t.waitingOn}` : t.state === "done" ? "Done" : "In progress";
  return (
    <article className="task-panel flex flex-col gap-5" aria-label={`${t.id} ${t.title}`}>
      <header className="flex flex-col gap-1">
        <p className="text-meta text-muted tabular-nums">{t.id}</p>
        <h3 className="text-title font-bold">{t.title}</h3>
        <p className="flex items-center gap-1.5 text-meta">
          <span aria-hidden className={cn("size-2 rounded-full", needs ? "bg-[var(--needs)]" : t.state === "done" ? "bg-muted" : "border-2 border-accent")} />
          <span className={needs ? "text-ink" : "text-muted"}>{stateText}</span>
          {t.owner && <span className="text-muted">· owner {t.owner === scenario.me ? "you" : t.owner}</span>}
        </p>
      </header>

      {t.note && (
        <section aria-label="The owner's note" className="flex flex-col gap-1">
          <p>
            <Ids text={t.note.text} />
          </p>
          <p className="text-meta text-muted">
            Written by {t.note.by} · {ago(t.note.t, now)}
          </p>
        </section>
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
                      {n === t.owner && <span className="text-meta text-muted"> owner</span>}
                    </button>
                    <span className={cn("text-meta", toneClass[s.tone])}>{s.text}</span>
                  </span>
                </li>
              );
            })}
          </ul>
        </section>
      )}

      {t.state !== "done" && <TellTheTeam key={t.id} task={t.id} to={agentsOn} suggestions={suggestions} />}

      {mentions.length > 0 && (
        <section aria-label={`Messages that mention ${t.id}`} className="flex flex-col gap-1.5">
          <button type="button" aria-expanded={showMentions} onClick={() => setShowMentions(!showMentions)} className="min-h-8 self-start text-meta text-muted hover:text-ink hover:underline">
            {count(mentions.length, "message mentions", "messages mention")} {t.id} · {showMentions ? "hide them" : "show them"}
          </button>
          {showMentions && (
            <ul className="flex animate-fade-in flex-col gap-1.5">
              {mentions.map((m) => (
                <li key={m.id}>
                  <button type="button" onClick={() => openThread(rootOf(snap.messages, m).id)} className="grid w-full grid-cols-[20px_minmax(0,1fr)] gap-x-2 text-left text-meta hover:underline">
                    <Mark name={m.from} />
                    <span className="line-clamp-2">
                      <strong>{m.from === scenario.me ? "You" : m.from}</strong> · {ago(m.t, now)}: {m.question ?? m.body}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}
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
