"use client";

// EXPERIMENTAL, lab only: the board panel's agents as one compact row each (presence,
// name, now line and its age), grouped by the task they share so who works with whom
// shows at a glance. A name opens the agent's details in place (owner, role, harness,
// delivery: the panel's own fields); a chevron on hover says it opens. The now line
// leads to its evidence: the agent's own messages in the timeline. An agent on more
// than one task sits under the one it owns. Grouping waits until it says something:
// four or more agents, and a task with two of them on it. Fed by the scenario, not the
// API.

import { ChevronRight, History } from "lucide-react";
import { type ReactNode, useState } from "react";
import type { Member } from "@/app/api";
import { exactTime, presenceWords } from "@/app/words";
import { cn } from "@/lib/utils";
import { type ScenarioTask, taskRef } from "../scenario";
import { at, openTask, scenario, showView, useLab } from "../store";
import { active, ago, minutesSince, onBoard, staleAfter, useNow } from "./common";

type Group = { task: ScenarioTask; agents: Member[] };

export function groupsOf(tasks: ScenarioTask[], agents: Member[]): { groups: Group[]; rest: Member[] } | null {
  const live = tasks.filter(active);
  const on = (t: ScenarioTask) => [t.owner, ...(t.with ?? [])].filter((n): n is string => !!n);
  if (agents.length < 4 || !live.some((t) => on(t).filter((n) => agents.some((a) => a.name === n)).length >= 2)) return null;
  const placed = new Map<string, string>();
  // Owners first, so an agent sits under the task it owns.
  for (const t of live) if (t.owner && !placed.has(t.owner)) placed.set(t.owner, t.id);
  for (const t of live) for (const n of t.with ?? []) if (!placed.has(n)) placed.set(n, t.id);
  const groups = live
    .map((task) => ({ task, agents: agents.filter((a) => placed.get(a.name) === task.id) }))
    .filter((g) => g.agents.length > 0)
    .sort((a, b) => b.agents.length - a.agents.length);
  return { groups, rest: agents.filter((a) => !placed.has(a.name)) };
}

function stateWords(t: ScenarioTask): string {
  if (t.state === "claimed") return "Claimed, not started";
  if (t.state === "waiting") return t.waitingOn === scenario.me ? "Waiting on you" : `Waiting on ${t.waitingOn ?? "someone"}`;
  return "Working";
}

type RowProps = { agent: Member; details: (a: Member) => ReactNode; pick: (name: string) => void };

function AgentRow({ agent, details, pick }: RowProps) {
  const { snap } = useLab();
  const now = useNow();
  const [open, setOpen] = useState(false);
  const presence = agent.presence ?? "no_session";
  const waiting = presence === "waiting";
  const line = snap.now[agent.name];
  const stale = line ? minutesSince(line.t, now) > staleAfter : false;
  return (
    <li
      data-agent={agent.name}
      className={cn("agent-row flex flex-col transition-colors duration-200 ease-out", waiting && "-mx-2.5 rounded-control bg-attention px-2.5 py-1.5")}
    >
      <div className="flex items-baseline gap-2">
        <span
          aria-hidden
          className={cn(
            "presence-dot size-2 shrink-0 -translate-y-px rounded-full",
            presence === "working" ? "bg-accent" : presence === "no_session" ? "border border-muted" : waiting ? "bg-ink" : "bg-muted",
          )}
        />
        <button
          type="button"
          aria-expanded={open}
          onClick={() => setOpen(!open)}
          className="group/name inline-flex min-h-7 min-w-0 items-center gap-1 rounded-[6px] font-bold text-ink hover:underline hover:decoration-1 hover:underline-offset-[3px]"
          title={open ? `Hide ${agent.name}'s details` : `Show ${agent.name}'s owner, role, harness and delivery`}
        >
          <span className="break-all">{agent.name}</span>
          <ChevronRight
            className={cn(
              "size-3.5 shrink-0 text-muted transition-[transform,opacity] duration-200 ease-out",
              open ? "rotate-90 opacity-100" : "opacity-0 group-hover/name:opacity-100 group-focus-visible/name:opacity-100 [@media(hover:none)]:opacity-100",
            )}
            strokeWidth={1.75}
            aria-hidden
          />
        </button>
        <span className={cn("ml-auto shrink-0 text-meta", presence === "working" || waiting ? "text-ink" : "text-muted")}>{presenceWords[presence]}</span>
      </div>
      <div className="pl-4">
        {line ? (
          <button
            type="button"
            onClick={() => {
              showView("timeline");
              pick(agent.name);
            }}
            className={cn("agent-now block text-left text-meta hover:underline", stale ? "text-muted" : "text-ink")}
            data-now={stale ? "stale" : "fresh"}
            title={`${stale ? "Not updated in a while. " : ""}Set ${exactTime(new Date(at(line.t)).toISOString())}. Shows ${agent.name}'s messages.`}
          >
            {stale && <History className="mr-1 inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-label="Not updated in a while" />}
            <span className="line-clamp-2 inline">{line.text}</span>
            <span className="whitespace-nowrap text-muted"> · {stale ? `set ${ago(line.t, now)}` : ago(line.t, now)}</span>
          </button>
        ) : (
          <p className="agent-now text-meta text-muted" data-now="none">
            No now line
          </p>
        )}
        {waiting && <p className="text-meta">Its session is waiting for you, such as a permission prompt.</p>}
        {open && <div className="animate-fade-in pt-1 pb-2">{details(agent)}</div>}
      </div>
    </li>
  );
}

export function AgentGroups({ board, agents, details, pick }: { board: string; agents: Member[]; item: (a: Member) => ReactNode; details: (a: Member) => ReactNode; pick: (name: string) => void }) {
  const { snap } = useLab();
  const row = (a: Member) => <AgentRow key={a.id} agent={a} details={details} pick={pick} />;
  const grouped = onBoard(board) ? groupsOf(snap.tasks, agents) : null;
  if (!grouped) {
    return (
      <ul className="flex flex-col gap-3" aria-label="Agents">
        {agents.map(row)}
      </ul>
    );
  }
  const people = (t: ScenarioTask) => [t.owner, ...(t.with ?? [])].filter((n) => n && scenario.people.some((p) => p.name === n));
  return (
    <div className="agent-groups flex flex-col gap-4" aria-label="Agents, by the task they share" role="group">
      {grouped.groups.map(({ task, agents: on }) => {
        const mine = task.state === "waiting" && task.waitingOn === scenario.me;
        const humans = people(task);
        return (
          <section key={task.id} aria-label={task.title} className="agent-group flex flex-col gap-2 border-t border-rule pt-3 first:border-t-0 first:pt-0">
            <header className="flex flex-col">
              <h4 className="text-meta font-bold text-ink">
                <button type="button" className="text-left hover:underline" onClick={() => openTask(task.id)} title="Open this task">
                  <span className="mr-1 tabular-nums text-muted">{taskRef(task.id)}</span>
                  {task.title}
                </button>
              </h4>
              <p className="text-meta text-muted">
                {mine ? <span className="rounded-[4px] bg-attention px-1 text-ink">{stateWords(task)}</span> : stateWords(task)}
                {humans.length > 0 && <> · with {humans.map((h) => (h === scenario.me ? "you" : h)).join(", ")}</>}
              </p>
            </header>
            <ul className="flex flex-col gap-3">{on.map(row)}</ul>
          </section>
        );
      })}
      {grouped.rest.length > 0 && (
        <section aria-label="Not on a task" className="agent-group flex flex-col gap-2 border-t border-rule pt-3">
          <h4 className="text-meta font-bold text-muted">Not on a task</h4>
          <ul className="flex flex-col gap-3">{grouped.rest.map(row)}</ul>
        </section>
      )}
    </div>
  );
}
