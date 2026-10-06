"use client";

// EXPERIMENTAL, lab only: the board panel's agents grouped by the task they share, so
// who works with whom shows at a glance. Each group is headed by its task; an agent on
// more than one task sits under the one it owns. It only groups once grouping says
// something: four or more agents, and at least one task with two of them on it.
// Otherwise the list is the plain one. Fed by the scenario's tasks, not the API.

import type { ReactNode } from "react";
import type { Member } from "@/app/api";
import { cn } from "@/lib/utils";
import type { ScenarioTask } from "../scenario";
import { scenario, useLab } from "../store";
import { active, onBoard } from "./common";

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

export function AgentGroups({ board, agents, item }: { board: string; agents: Member[]; item: (a: Member) => ReactNode }) {
  const { snap } = useLab();
  const grouped = onBoard(board) ? groupsOf(snap.tasks, agents) : null;
  if (!grouped) {
    return (
      <ul className="flex flex-col gap-5" aria-label="Agents">
        {agents.map(item)}
      </ul>
    );
  }
  const people = (t: ScenarioTask) => [t.owner, ...(t.with ?? [])].filter((n) => n && scenario.people.some((p) => p.name === n));
  return (
    <div className="agent-groups flex flex-col gap-5" aria-label="Agents, by the task they share" role="group">
      {grouped.groups.map(({ task, agents: on }) => {
        const mine = task.state === "waiting" && task.waitingOn === scenario.me;
        const humans = people(task);
        return (
          <section key={task.id} aria-label={task.title} className="agent-group flex flex-col gap-3 border-t border-rule pt-3 first:border-t-0 first:pt-0">
            <header className="flex flex-col">
              <h4 className="text-meta font-bold text-ink">{task.title}</h4>
              <p className="text-meta text-muted">
                {mine ? <span className="rounded-[4px] bg-attention px-1 text-ink">{stateWords(task)}</span> : stateWords(task)}
                {humans.length > 0 && <> · with {humans.map((h) => (h === scenario.me ? "you" : h)).join(", ")}</>}
              </p>
            </header>
            <ul className="flex flex-col gap-5">{on.map(item)}</ul>
          </section>
        );
      })}
      {grouped.rest.length > 0 && (
        <section aria-label="Not on a task" className={cn("agent-group flex flex-col gap-3 border-t border-rule pt-3")}>
          <h4 className="text-meta font-bold text-muted">Not on a task</h4>
          <ul className="flex flex-col gap-5">{grouped.rest.map(item)}</ul>
        </section>
      )}
    </div>
  );
}
