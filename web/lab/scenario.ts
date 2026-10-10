// A scenario is a made-up board, and how it changes over a few time steps, written as
// plain data. The fake API (fake-api.ts) serves its board, members and messages to the
// real UI; the experimental views read the parts the API doesn't have yet (working
// and paused lines,
// tasks, asks, the brief, files) from the same steps. Times are minutes since the
// scenario starts.
//
// These types are the lab's mock of features that aren't in the API or the contract.

import type { Presence } from "@/app/api";
import { type Onboarding, type OnboardingStep, foldOnboarding, noOnboarding } from "./onboarding";

export type ScenarioPerson = {
  name: string;
  admin?: boolean;
  /** joined is when they joined the board, in minutes; on the board from the start when left out. */
  joined?: number;
  /**
   * authorization is who let them in when an agent did it for its person: the event's
   * data.authorization, with names for ids. via is the person's allowance or an approval.
   */
  authorization?: { agent: string; person: string; via: "allowance" | "approval"; as: "invited" | "added" };
};

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
  /** attach is an artifact posted with the message, by id. */
  attach?: string;
  /** decision marks a message that records a decision. */
  decision?: boolean;
  /**
   * An ask is a message with answer buttons: question is its headline, options its
   * buttons (the one asked can always reply with something else), task the task it
   * blocks. An ask blocks its task until answered, unless the agent is going with an
   * option anyway (goingWith, `aboard ask --going-with "X" --at 16:00`): then ahead
   * is set and nothing is blocked.
   */
  question?: string;
  options?: string[];
  task?: string;
  ahead?: boolean;
  goingWith?: string;
};

/** InboxAsk is an ask on a board the scenario only summarises (a workspace's other boards). */
export type InboxAsk = {
  id: string;
  board: string;
  from: string;
  question: string;
  body: string;
  options: string[];
  task?: string;
  ahead?: boolean;
  goingWith?: string;
  /** artifact is evidence attached to the ask: its name and what it is. */
  artifact?: { name: string; summary: string };
  /** ago is how many minutes before the last step it was asked. */
  ago: number;
};

/** Notice is a "worth a look" item on a board the scenario only summarises. */
export type Notice = { board: string; who: string; text: string; detail: string };

/**
 * TaskState is where a task is. Claimed has an owner who hasn't started on it. Blocked is
 * never a state: a task is blocked while it has an open blocking ask. "waiting" (with
 * waitingOn) is the busy scenario's shorthand for such an ask.
 */
export type TaskState = "open" | "claimed" | "working" | "waiting" | "done";

export type ScenarioTask = {
  /** id is the board's prefix and a number (CHK-16), which messages can mention. */
  id: string;
  title: string;
  state: TaskState;
  owner?: string;
  /** with are the task's collaborators: agents or people. */
  with?: string[];
  /** waitingOn and reason say who a waiting task waits on, and why. */
  waitingOn?: string;
  reason?: string;
  /** t is when the task last changed. */
  t: number;
  /** about says what the task is and why, written when it is opened; it rarely changes. */
  about?: string;
  /** note is where the task stands: the owner keeps it current, as the task's own brief. */
  note?: { text: string; by: string; t: number };
  /** by is who opened the task; the owner is who is responsible for it, which can differ. */
  by?: string;
};

/**
 * NowLine is what an agent is on, and when it said so: "Working on: …" (set by task start
 * and new, or the harness's todo or plan hook), or "Paused on: … · until 14:20"
 * (`aboard paused "…" --until 14:20`). until (minutes) makes paused and late different
 * facts. Idle and disconnected come from the server; an agent never sets them.
 */
export type NowLine = {
  text: string;
  t: number;
  paused?: boolean;
  until?: number;
  /** setBy is the person who set the line for their agent (`aboard working --as`), if not the agent. */
  setBy?: string;
};

/**
 * Brief is the board's maintained summary, written by its steward agent, the first thing
 * a new agent reads. summary is where things stand, in a sentence or two; the rest are
 * its sections. Task ids in the text (CHK-16) link to their tasks.
 */
export type Brief = {
  summary: string;
  by: string;
  t: number;
  goal: string;
  approach: string;
  who: string;
  blocked: string;
  next: string;
  /** sources says what the brief draws on. */
  sources: string;
};

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
  /** summary says in a line what it is. */
  summary?: string;
  body: string;
  /** task is the task it was made for. */
  task?: string;
  /** approved is the version the person approved, if any. */
  approved?: number;
};

/**
 * Step is one moment of a scenario. Each step changes only what it names: presence and
 * working and paused lines by agent (null clears one), tasks and artifacts by id, the brief, and new
 * messages.
 */
export type Step = {
  label: string;
  /** at is the step's time, in minutes since the scenario starts. */
  at: number;
  presence?: Record<string, Presence>;
  /** since sets when an agent's presence changed, in minutes, when it wasn't at the step's time. */
  since?: Record<string, number>;
  now?: Record<string, NowLine | null>;
  tasks?: ScenarioTask[];
  brief?: Brief | null;
  messages?: ScenarioMessage[];
  artifacts?: Artifact[];
  /** onboarding changes the allowance, approvals, invite notices and pairing requests. */
  onboarding?: OnboardingStep;
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
   * otherBoards fill the sidebar and the board list. They hold no messages; a workspace
   * scenario gives them agents (working, idle, the rest disconnected), people and
   * counts, so the facts read true.
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
    /** lastAgo is how many minutes before the last step its last message was. */
    lastAgo?: number;
  }[];
  /** steward is the agent that keeps the brief current; no brief shows without one. */
  steward?: string;
  /** inbox holds asks on the other boards, for the Inbox across boards. */
  inbox?: InboxAsk[];
  /** notices are "worth a look" items on the other boards. */
  notices?: Notice[];
  /** staleAfter is how many minutes old a working line may be before it is marked stale. */
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
  /** opened is when each task first appeared, in minutes. */
  opened: Record<string, number>;
  onboarding: Onboarding;
};

/** snapshot folds a scenario's steps up to and including step k. */
export function snapshot(s: Scenario, k: number): Snapshot {
  const presence: Record<string, Presence> = {};
  const presenceSince: Record<string, number> = {};
  const now: Record<string, NowLine | null> = {};
  const tasks = new Map<string, ScenarioTask>();
  const artifacts = new Map<string, Artifact>();
  const opened: Record<string, number> = {};
  let brief: Brief | null = null;
  let briefVersion = 0;
  let onboarding = noOnboarding;
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
    Object.assign(presenceSince, step.since ?? {});
    Object.assign(now, step.now ?? {});
    for (const t of step.tasks ?? []) {
      if (!tasks.has(t.id)) opened[t.id] = t.t;
      tasks.set(t.id, t);
    }
    if (step.brief !== undefined) {
      brief = step.brief;
      if (brief) briefVersion++;
    }
    messages.push(...(step.messages ?? []));
    for (const a of step.artifacts ?? []) artifacts.set(a.id, a);
    onboarding = foldOnboarding(onboarding, step.onboarding);
  }
  const files = [...artifacts.values()];
  // The brief is also the board's first maintained artifact.
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
      body: briefMarkdown(brief),
    });
  }
  return { step: s.steps[k], presence, presenceSince, now, tasks: [...tasks.values()], brief, briefVersion, messages, artifacts: files, opened, onboarding };
}

/** taskIds matches task ids in text, such as CHK-16. */
export const taskIds = /\b[A-Z]{2,5}-\d+\b/g;

/** briefMarkdown is the brief as the Markdown file the steward keeps. */
export function briefMarkdown(b: Brief): string {
  return `# Brief\n\n${b.summary}\n\n## Goal\n${b.goal}\n\n## Approach\n${b.approach}\n\n## Who's doing what\n${b.who}\n\n## Blocked on\n${b.blocked}\n\n## Next\n${b.next}\n\n## Sources\n${b.sources}`;
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

/** mentioning is every message whose text names a task, or that is an ask blocking it. */
export function mentioning(messages: ScenarioMessage[], task: string): ScenarioMessage[] {
  return messages.filter((m) => m.task === task || m.body.match(taskIds)?.includes(task) || m.question?.match(taskIds)?.includes(task));
}

/** threadsOf is every thread, by its first message, with a message that names a task. */
export function threadsOf(messages: ScenarioMessage[], task: string): ScenarioMessage[] {
  const roots = new Map<string, ScenarioMessage>();
  for (const m of mentioning(messages, task)) {
    const r = rootOf(messages, m);
    roots.set(r.id, r);
  }
  return [...roots.values()].sort((a, b) => a.t - b.t);
}
