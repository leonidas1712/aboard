// EXPERIMENTAL, lab only: asks, as the Inbox and the board see them. An ask is a message
// with answer buttons that names the task it blocks. Answering sends an ordinary reply
// to the agent who asked, which wakes it, and records a decision. An ask blocks its
// task until answered, unless the agent is "going with X unless you say". The Inbox
// gathers every unanswered ask to the viewer from every board, blocking ones first, and
// below them what is worth a look.

import { type Message, post } from "@/app/api";
import { clockTime } from "@/app/words";
import { answeredByMe } from "../fake-api";
import { type Snapshot, type ScenarioMessage, rootOf } from "../scenario";
import { at, markAnswered, scenario } from "../store";
import { active, minutesSince } from "./common";
import { latestFrom } from "./links";

export type Ask = {
  id: string;
  board: string;
  boardTitle: string;
  from: string;
  question: string;
  body: string;
  options: string[];
  task?: string;
  ahead: boolean;
  /** goingWith is what the agent goes with unless the person says otherwise. */
  goingWith?: string;
  /** artifact is the evidence: a file on the scenario's board, or a summary of one elsewhere. */
  artifact?: { id?: string; name: string; summary: string };
  /** at is when it was asked, in ms. */
  at: number;
};

const titleOf = (board: string) =>
  board === scenario.board.name ? (scenario.board.title ?? board) : (scenario.otherBoards?.find((b) => b.name === board)?.title ?? board);

/** asksOf is every ask waiting on the person, blocking ones first, newest first. */
export function asksOf(snap: Snapshot, answered: Record<string, string>): Ask[] {
  const me = scenario.me;
  const replied = (m: ScenarioMessage) => !!answered[m.id] || answeredByMe(m.id) || snap.messages.some((r) => r.replyTo === m.id && r.from === me);
  const here: Ask[] = snap.messages
    .filter((m) => m.options && (m.ahead || m.to?.includes(`@${me}`)) && !replied(m))
    .map((m) => {
      const file = m.attach ? snap.artifacts.find((a) => a.id === m.attach) : undefined;
      return {
        id: m.id,
        board: scenario.board.name,
        boardTitle: titleOf(scenario.board.name),
        from: m.from,
        question: m.question ?? m.body,
        body: m.body,
        options: m.options!,
        task: m.task,
        ahead: !!m.ahead,
        goingWith: m.goingWith,
        artifact: file && { id: file.id, name: file.name, summary: file.summary ?? "" },
        at: at(m.t),
      };
    });
  const last = scenario.steps.at(-1)!.at;
  const there: Ask[] = (scenario.inbox ?? [])
    .filter((a) => !answered[a.id])
    .map((a) => ({ ...a, ahead: !!a.ahead, boardTitle: titleOf(a.board), at: at(last - a.ago) }));
  return [...here, ...there].sort((a, b) => Number(a.ahead) - Number(b.ahead) || b.at - a.at);
}

/**
 * Block is an open blocking ask on a task: who it waits on and what it asks. A task is
 * Blocked while it has one (Needs you when it waits on the viewer); nobody sets that
 * by hand. The busy scenario's "waiting" tasks stand for such asks.
 */
export type Block = { id: string; task: string; on: string; from: string; question: string; t: number };

export function blocksOf(snap: Snapshot, answered: Record<string, string>): Block[] {
  const out: Block[] = [];
  for (const m of snap.messages) {
    if (!m.options || m.ahead || !m.task) continue;
    const on = m.to?.find((x) => x.startsWith("@"))?.slice(1) ?? scenario.me;
    const done =
      !!answered[m.id] || (on === scenario.me && answeredByMe(m.id)) || snap.messages.some((r) => r.replyTo === m.id && r.from === on);
    if (!done) out.push({ id: m.id, task: m.task, on, from: m.from, question: m.question ?? m.body, t: m.t });
  }
  for (const t of snap.tasks) {
    if (t.state === "waiting" && t.waitingOn) out.push({ id: t.id, task: t.id, on: t.waitingOn, from: t.owner ?? "", question: t.reason ?? "", t: t.t });
  }
  return out;
}

export type Notice = { key: string; board: string; boardTitle: string; who: string; text: string; detail: string; late: boolean };

/**
 * noticesOf is what is worth a look on every board: an agent paused past the time it
 * gave (late), an agent idle with no task, and a task blocked on someone else for more
 * than a few hours. A normal pause, or a fresh block between agents, stays on the board.
 */
export function noticesOf(snap: Snapshot, now: number): Notice[] {
  const out: Notice[] = [];
  const board = scenario.board.name;
  const busy = new Set(snap.tasks.filter(active).flatMap((t) => [t.owner, ...(t.with ?? [])]));
  for (const a of scenario.agents) {
    const line = snap.now[a.name];
    const task = snap.tasks.find((t) => active(t) && (t.owner === a.name || t.with?.includes(a.name)));
    if (line?.paused && line.until !== undefined && now > at(line.until)) {
      const over = Math.round((now - at(line.until)) / 60_000);
      out.push({
        key: `late-${a.name}`,
        board,
        boardTitle: titleOf(board),
        who: a.name,
        text: `${a.name} paused on ${line.text} until ${clockTime(new Date(at(line.until)).toISOString())}, now ${over}m over`,
        detail: task ? task.id : "no task",
        late: true,
      });
    } else if (snap.presence[a.name] === "idle" && !line && !busy.has(a.name) && minutesSince(snap.presenceSince[a.name] ?? 0, now) >= 30) {
      const idle = Math.round(minutesSince(snap.presenceSince[a.name] ?? 0, now));
      out.push({ key: `idle-${a.name}`, board, boardTitle: titleOf(board), who: a.name, text: `${a.name} has been idle for ${idle < 60 ? `${idle}m` : `${Math.floor(idle / 60)}h`}`, detail: "no task", late: true });
    }
  }
  for (const b of blocksOf(snap, {})) {
    if (b.on === scenario.me || minutesSince(b.t, now) < 180) continue;
    out.push({ key: `blocked-${b.task}`, board, boardTitle: titleOf(board), who: b.from || b.on, text: `${b.task} blocked on ${b.on} for ${Math.floor(minutesSince(b.t, now) / 60)}h`, detail: b.question, late: true });
  }
  for (const n of scenario.notices ?? []) out.push({ key: `${n.board}-${n.who}`, ...n, boardTitle: titleOf(n.board), late: true });
  return out;
}

/** Status is what an agent is on, in the UI's words; late marks a passed "until" or a long idle, said quietly. */
export type Status = { text: string; tone: "late" | "quiet" | "plain" };

/**
 * AgentState is the one word for what an agent is doing, which its dot and its line
 * agree on: paused when it said what it is paused on (late once past its time),
 * waiting on you only for a permission prompt in its session (detecting one is a
 * future, harness-dependent hook), working when it said what it works on or was busy
 * just now, idle with neither, disconnected with no session.
 */
export type AgentState = "working" | "paused" | "late" | "waiting on you" | "idle" | "disconnected";

export function agentState(snap: Snapshot, name: string, now: number): AgentState {
  const line = snap.now[name];
  const presence = snap.presence[name];
  if (presence === "waiting") return "waiting on you";
  if (line?.paused) return line.until !== undefined && now > at(line.until) ? "late" : "paused";
  if (presence === "no_session") return "disconnected";
  if (line || presence === "working") return "working";
  return "idle";
}

/** stateDot is the dot beside an agent's mark for each state. */
export const stateDot: Record<AgentState, string> = {
  working: "bg-accent",
  paused: "border-2 border-accent bg-sidebar",
  late: "bg-muted",
  "waiting on you": "bg-attention border border-ink",
  idle: "border border-muted bg-sidebar",
  disconnected: "bg-sidebar border border-dashed border-muted",
};

/** statusOf is what an agent is on right now, in the UI's words, and how loudly to say it. */
export function statusOf(snap: Snapshot, name: string, _asks: Ask[], now: number): Status {
  const line = snap.now[name];
  const state = agentState(snap, name, now);
  const time = (m: number) => clockTime(new Date(at(m)).toISOString());
  const by = line?.setBy ? ` · set by ${line.setBy === scenario.me ? "you" : line.setBy}` : "";
  switch (state) {
    case "late":
      return { text: `Paused on: ${line!.text} · ${Math.round((now - at(line!.until!)) / 60_000)}m over${by}`, tone: "late" };
    case "paused":
      return { text: `Paused on: ${line!.text}${line!.until !== undefined ? ` · until ${time(line!.until)}` : ""}${by}`, tone: "plain" };
    case "waiting on you":
      return { text: "waiting on you in its session, such as a permission prompt", tone: "plain" };
    case "disconnected":
      return { text: "disconnected", tone: "quiet" };
    case "idle": {
      const busy = snap.tasks.some((t) => active(t) && (t.owner === name || t.with?.includes(name)));
      const idle = Math.round(minutesSince(snap.presenceSince[name] ?? 0, now));
      const span = idle < 60 ? `${idle}m` : `${Math.floor(idle / 60)}h`;
      return busy ? { text: `idle ${span}`, tone: "quiet" } : { text: `idle ${span}, on no task`, tone: "late" };
    }
    default: {
      if (line) return { text: `Working on: ${line.text}${by}`, tone: "plain" };
      // Busy but nothing said: its last message, labelled as such, so the row never goes blank.
      const last = latestFrom(name);
      return last ? { text: `last said: ${last.body.split("\n")[0]}`, tone: "quiet" } : { text: "nothing said yet", tone: "quiet" };
    }
  }
}

export const toneClass: Record<Status["tone"], string> = {
  late: "late text-muted",
  quiet: "text-muted",
  plain: "text-muted",
};

/** answer sends the person's answer to an ask as a reply to the agent, and takes it off the Inbox. */
export async function answer(a: Ask, text: string): Promise<Message> {
  markAnswered(a.id, text);
  const main = a.board === scenario.board.name;
  return post<Message>(`/v1/boards/${encodeURIComponent(a.board)}/messages`, {
    body: text,
    to: [`@${a.from}`],
    ...(main ? { reply_to: a.id } : {}),
  });
}

/** askOfMessage is the ask a timeline message is, if any. */
export function isAsk(snap: Snapshot, id: string): ScenarioMessage | undefined {
  const m = snap.messages.find((x) => x.id === id);
  return m?.options ? m : undefined;
}

export { rootOf };
