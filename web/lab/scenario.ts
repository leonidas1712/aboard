// A scenario is a made-up board, and how it changes over a few time steps, written as
// plain data. The fake API (fake-api.ts) serves its board, members and messages to the
// real UI; the experimental views read the parts the API doesn't have yet (now lines,
// tasks, the brief, artifacts) from the same steps. Times are minutes since the
// scenario starts.
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
  /** about names the tasks the message is about, by id; its thread links to each. */
  about?: string[];
  /** attach is an artifact posted with the message, by id. */
  attach?: string;
  /** decision marks a message that records a decision, for the brief's freshness. */
  decision?: boolean;
  /** ahead marks an agent going ahead unless someone objects. */
  ahead?: boolean;
};

/**
 * Workspace is what a person on many boards sees across them. Each board's line is
 * either agent-written (its brief's summary, with who and when) or facts.
 */
export type Workspace = {
  /** sinceLooked is the facts since the person last looked, across every board. */
  sinceLooked: { messages: number; tasksDone: number; decisions: number; artifacts: number };
  boards: {
    name: string;
    title: string;
    /** brief is the board's brief summary and who updated it, how many minutes ago. */
    brief?: { summary: string; by: string; ago: number };
    /** needs are what the board needs from the person: blocking asks, or agents going ahead unless told. */
    needs?: { from: string; text: string; kind: "asks" | "ahead"; ago: number }[];
    /** stuck says what is stuck or stale on the board. */
    stuck?: string[];
    /** moving is the board's activity since the person last looked. */
    moving?: { messages: number; tasksDone: number };
  }[];
};

/** TaskState is where a task is. Claimed has an owner who hasn't started on it. */
export type TaskState = "open" | "claimed" | "working" | "waiting" | "done";

export type ScenarioTask = {
  /** id is t followed by a number (t12), which chips show as T12. */
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

/**
 * Brief is the board's maintained summary, the first thing a new agent reads: what the
 * project is, what's going on, who does what, blockers and next steps. summary is its
 * one line; body is Markdown.
 */
export type Brief = { summary: string; body: string; by: string; t: number };

/**
 * Artifact is a file on the board. An artifact is something agents made; content is
 * something people attached. A maintained artifact is pinned and kept current; a
 * one-off one answers one question. body is the file's text (HTML, Markdown or SVG).
 */
export type Artifact = {
  id: string;
  name: string;
  format: "html" | "md" | "svg";
  kind: "artifact" | "content";
  maintained?: boolean;
  by: string;
  version: number;
  t: number;
  /** summary says in a line what it is, on its card. */
  summary?: string;
  body: string;
};

/**
 * Step is one moment of a scenario. Each step changes only what it names: presence and
 * now lines by agent (null clears a line), tasks and artifacts by id, the brief, and new
 * messages.
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
  artifacts?: Artifact[];
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
  /**
   * otherBoards fill the board list and the left panel. They hold no messages; a
   * workspace scenario gives them agents (working, idle, the rest disconnected), people
   * and counts, so the list's facts read true.
   */
  otherBoards?: {
    name: string;
    title?: string;
    agents?: number;
    working?: number;
    idle?: number;
    people?: string[];
    messages?: number;
    unread?: number;
    needs?: number;
    /** lastAgo is how many minutes before the last step its last message was. */
    lastAgo?: number;
  }[];
  /** steward is the agent that keeps the brief current. */
  steward?: string;
  /** workspace is the cross-board overview's data, for a scenario of many boards. */
  workspace?: Workspace;
  /** staleAfter is how many minutes old a now line may be before it is marked stale. */
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
  /** briefVersion counts the brief's versions so far. */
  briefVersion: number;
  messages: ScenarioMessage[];
  /** artifacts are the board's files, the brief first when there is one. */
  artifacts: Artifact[];
};

/** snapshot folds a scenario's steps up to and including step k. */
export function snapshot(s: Scenario, k: number): Snapshot {
  const presence: Record<string, Presence> = {};
  const presenceSince: Record<string, number> = {};
  const now: Record<string, NowLine | null> = {};
  const tasks = new Map<string, ScenarioTask>();
  const artifacts = new Map<string, Artifact>();
  let brief: Brief | null = null;
  let briefVersion = 0;
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
    if (step.brief !== undefined) {
      brief = step.brief;
      if (brief) briefVersion++;
    }
    messages.push(...(step.messages ?? []));
    for (const a of step.artifacts ?? []) artifacts.set(a.id, a);
  }
  // The brief is also the board's first maintained artifact.
  const files = [...artifacts.values()];
  if (brief) {
    files.unshift({
      id: "brief",
      name: "brief.md",
      format: "md",
      kind: "artifact",
      maintained: true,
      by: brief.by,
      version: briefVersion,
      t: brief.t,
      summary: "What this board is for and where it stands. New agents read it first.",
      body: brief.body,
    });
  }
  return { step: s.steps[k], presence, presenceSince, now, tasks: [...tasks.values()], brief, briefVersion, messages, artifacts: files };
}

/** taskRef is how a chip names a task: t12 is T12. */
export function taskRef(id: string): string {
  return id.toUpperCase();
}

/** rootOf is the first message of a scenario message's thread. */
export function rootOf(messages: ScenarioMessage[], m: ScenarioMessage): ScenarioMessage {
  let r = m;
  for (let i = 0; r.replyTo && i < 50; i++) {
    const up = messages.find((x) => x.id === r.replyTo);
    if (!up) break;
    r = up;
  }
  return r;
}

/** threadsOf is every thread, by its first message, whose messages name a task. */
export function threadsOf(messages: ScenarioMessage[], task: string): ScenarioMessage[] {
  const roots = new Map<string, ScenarioMessage>();
  for (const m of messages) {
    if (!m.about?.includes(task)) continue;
    const r = rootOf(messages, m);
    roots.set(r.id, r);
  }
  return [...roots.values()].sort((a, b) => a.t - b.t);
}

/** tasksOfThread is every task the messages of a thread name, in the order first named. */
export function tasksOfThread(messages: ScenarioMessage[], root: string): string[] {
  const out: string[] = [];
  for (const m of messages) {
    if (rootOf(messages, m).id !== root) continue;
    for (const t of m.about ?? []) if (!out.includes(t)) out.push(t);
  }
  return out;
}
