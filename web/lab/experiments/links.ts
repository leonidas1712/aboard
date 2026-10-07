// EXPERIMENTAL, lab only: how messages, threads, tasks and files link, many to many. A
// message is about the tasks it names (CHK-12 in its text) or blocks (an ask); a thread
// is about every task any of its messages is about; a file is for a task, and is posted
// with a message or mentioned by name. Read from the board as it is now, the person's
// own messages included, so a message sent from the lab links like any other.

import { type BoardMessage, boardMessages } from "../fake-api";
import { type Artifact, type Snapshot, taskIds } from "../scenario";

export type ThreadLink = {
  root: BoardMessage;
  /** replies is how many replies the thread has, and last when the newest came, in ms. */
  replies: number;
  last: number;
  /** tasks are every task the thread is about, in the order first named. */
  tasks: string[];
};

/** tasksOf is the tasks one message is about, of those on the board. */
export function tasksOf(m: Pick<BoardMessage, "body" | "about">, snap: Snapshot): string[] {
  const named = [...m.about, ...(m.body.match(taskIds) ?? [])];
  return [...new Set(named)].filter((id) => snap.tasks.some((t) => t.id === id));
}

function rootId(all: BoardMessage[], m: BoardMessage): string {
  let r = m;
  for (let i = 0; r.replyTo && i < 50; i++) {
    const up = all.find((x) => x.id === r.replyTo);
    if (!up) break;
    r = up;
  }
  return r.id;
}

/** threads is every thread on the board (a message with replies), newest activity first. */
export function threads(snap: Snapshot): ThreadLink[] {
  const all = boardMessages();
  const by = new Map<string, BoardMessage[]>();
  for (const m of all) by.set(rootId(all, m), [...(by.get(rootId(all, m)) ?? []), m]);
  const out: ThreadLink[] = [];
  for (const [id, ms] of by) {
    if (ms.length < 2) continue;
    const root = all.find((x) => x.id === id)!;
    const tasks: string[] = [];
    for (const m of ms) for (const t of tasksOf(m, snap)) if (!tasks.includes(t)) tasks.push(t);
    out.push({ root, replies: ms.length - 1, last: Math.max(...ms.map((m) => m.at)), tasks });
  }
  return out.sort((a, b) => b.last - a.last);
}

/** threadOf is the thread a message starts, if it has replies. */
export function threadOf(snap: Snapshot, id: string): ThreadLink | undefined {
  return threads(snap).find((t) => t.root.id === id);
}

/** aboutTask is the threads about a task, and the messages about it that stand alone. */
export function aboutTask(snap: Snapshot, task: string): { threads: ThreadLink[]; loose: BoardMessage[] } {
  const all = boardMessages();
  const ts = threads(snap).filter((t) => t.tasks.includes(task));
  const inThread = new Set(ts.map((t) => t.root.id));
  const loose = all.filter((m) => !m.replyTo && !inThread.has(m.id) && !threads(snap).some((t) => t.root.id === m.id) && tasksOf(m, snap).includes(task));
  return { threads: ts, loose };
}

/** filterIds is what a timeline filtered to a task keeps: the first messages of its threads and its own messages. */
export function filterIds(snap: Snapshot, task: string): string[] {
  const { threads: ts, loose } = aboutTask(snap, task);
  return [...ts.map((t) => t.root.id), ...loose.map((m) => m.id)];
}

/** byAgent is what a conversation narrowed to an agent keeps: the threads it wrote in, and its own messages. */
export function byAgent(snap: Snapshot, agent: string): { threads: number; loose: number; ids: string[] } {
  const all = boardMessages();
  const ts = threads(snap);
  const roots = new Set(all.filter((m) => m.from === agent).map((m) => rootId(all, m)));
  const inThreads = [...roots].filter((id) => ts.some((t) => t.root.id === id));
  return { threads: inThreads.length, loose: roots.size - inThreads.length, ids: [...roots] };
}

/** latestFrom is an agent's most recent message on the board. */
export function latestFrom(agent: string): BoardMessage | undefined {
  return boardMessages()
    .filter((m) => m.from === agent)
    .at(-1);
}

export type FileContext = {
  /** posted are the messages the file was posted with, each with its thread if it is in one. */
  posted: { message: BoardMessage; thread: ThreadLink | undefined }[];
  /** mentioned are the messages that name the file. */
  mentioned: BoardMessage[];
  /** tasks are the tasks the file is for, or that its messages are about. */
  tasks: string[];
};

/** contextOf is where a file is used: the messages it was posted with or named in, and its tasks. */
export function contextOf(snap: Snapshot, a: Artifact): FileContext {
  const all = boardMessages();
  const posting = snap.messages.filter((m) => m.attach === a.id).map((m) => m.id);
  const ts = threads(snap);
  const posted = all
    .filter((m) => posting.includes(m.id))
    .map((m) => ({ message: m, thread: ts.find((t) => t.root.id === rootId(all, m)) }));
  const mentioned = all.filter((m) => !posting.includes(m.id) && m.body.includes(a.name));
  const tasks = [...new Set([...(a.task ? [a.task] : []), ...posted.flatMap((p) => tasksOf(p.message, snap))])];
  return { posted, mentioned, tasks };
}
