// A scenario is a made-up board, and how it changes over a few time steps, written as
// plain data. The fake API (fake-api.ts) serves its board, members and messages to the
// real UI; the experimental views read the parts the API doesn't have yet (now lines,
// tasks, the brief) from the same steps. Times are minutes since the scenario starts.
//
// These types are the lab's mock of features that aren't in the API or the contract.

import type { Presence } from "@/app/api";

export type ScenarioPerson = { name: string; admin?: boolean };

export type ScenarioAgent = {
  name: string;
  /** harness is the harness id, as the API reports it: claude-code, codex, omp. */
  harness: string;
  /** owner is the person the agent belongs to; the viewer when left out. */
  owner?: string;
  role?: string;
  /** joined is when it joined, in minutes; it is on the board from the start when left out. */
  joined?: number;
};

export type ScenarioMessage = {
  id: string;
  t: number;
  from: string;
  /** to is "all", "@name" or "role:name" targets; everyone when left out. */
  to?: string[];
  body: string;
  replyTo?: string;
  asks?: boolean;
  urgent?: boolean;
};

/** TaskState is where a task is. Claimed has an owner who hasn't started on it. */
export type TaskState = "open" | "claimed" | "working" | "waiting" | "done";

export type ScenarioTask = {
  id: string;
  title: string;
  state: TaskState;
  owner?: string;
  /** with are the task's collaborators: agents or people. */
  with?: string[];
  /** waitingOn and reason say who a waiting task waits on, and why. */
  waitingOn?: string;
  reason?: string;
  label?: string;
  /** t is when the task last changed. */
  t: number;
};

/** NowLine is what an agent says it is working on, and when it said so. */
export type NowLine = { text: string; t: number };

/** Brief is the board's short pinned note: what it is for right now. */
export type Brief = { text: string; by: string; t: number };

/**
 * Step is one moment of a scenario. Each step changes only what it names: presence and
 * now lines by agent (null clears a line), tasks by id, the brief, and new messages.
 */
export type Step = {
  label: string;
  /** at is the step's time, in minutes since the scenario starts. */
  at: number;
  presence?: Record<string, Presence>;
  now?: Record<string, NowLine | null>;
  tasks?: ScenarioTask[];
  brief?: Brief | null;
  messages?: ScenarioMessage[];
};

export type Scenario = {
  id: string;
  title: string;
  /** summary says, in a sentence, what the scenario is for. */
  summary: string;
  /** me is the person viewing the board; they must be in people. */
  me: string;
  people: ScenarioPerson[];
  agents: ScenarioAgent[];
  board: {
    name: string;
    title?: string;
    charter?: string;
    policy?: "starter" | "recommended";
    roles?: Record<string, string>;
  };
  /** otherBoards fill the board list and the left panel; they hold no messages. */
  otherBoards?: { name: string; title?: string }[];
  /** staleAfter is how many minutes old a now line or brief may be before it is marked stale. */
  staleAfter?: number;
  steps: Step[];
};

export type Snapshot = {
  step: Step;
  presence: Record<string, Presence>;
  /** presenceSince is when each agent's presence last changed, in minutes. */
  presenceSince: Record<string, number>;
  now: Record<string, NowLine | null>;
  tasks: ScenarioTask[];
  brief: Brief | null;
  messages: ScenarioMessage[];
};

/** snapshot folds a scenario's steps up to and including step k. */
export function snapshot(s: Scenario, k: number): Snapshot {
  const presence: Record<string, Presence> = {};
  const presenceSince: Record<string, number> = {};
  const now: Record<string, NowLine | null> = {};
  const tasks = new Map<string, ScenarioTask>();
  let brief: Brief | null = null;
  const messages: ScenarioMessage[] = [];
  for (const a of s.agents) {
    presence[a.name] = "idle";
    presenceSince[a.name] = a.joined ?? 0;
  }
  for (const step of s.steps.slice(0, k + 1)) {
    for (const [name, p] of Object.entries(step.presence ?? {})) {
      if (presence[name] !== p) presenceSince[name] = step.at;
      presence[name] = p;
    }
    Object.assign(now, step.now ?? {});
    for (const t of step.tasks ?? []) tasks.set(t.id, t);
    if (step.brief !== undefined) brief = step.brief;
    messages.push(...(step.messages ?? []));
  }
  return { step: s.steps[k], presence, presenceSince, now, tasks: [...tasks.values()], brief, messages };
}
