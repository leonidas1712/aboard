// EXPERIMENTAL, lab only: asks, as the Inbox and the board see them. An ask is a message
// with answer buttons that names the task it blocks. Answering sends an ordinary reply
// to the agent who asked. The Inbox gathers every unanswered ask from every board, and
// below them what is worth a look: agents late on their "back by", idle with no task,
// or with a now line gone stale.

import { type Message, post } from "@/app/api";
import { clockTime } from "@/app/words";
import { answeredByMe } from "../fake-api";
import { type Snapshot, type ScenarioMessage, rootOf } from "../scenario";
import { at, markAnswered, scenario } from "../store";
import { active, minutesSince, staleAfter } from "./common";

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

export type Notice = { key: string; board: string; boardTitle: string; who: string; text: string; detail: string; late: boolean };

/** noticesOf is what is worth a look: late, idle or stale, on every board. */
export function noticesOf(snap: Snapshot, now: number): Notice[] {
  const out: Notice[] = [];
  const board = scenario.board.name;
  const busy = new Set(snap.tasks.filter(active).flatMap((t) => [t.owner, ...(t.with ?? [])]));
  for (const a of scenario.agents) {
    const line = snap.now[a.name];
    const task = snap.tasks.find((t) => active(t) && (t.owner === a.name || t.with?.includes(a.name)));
    if (line?.backBy !== undefined && now > at(line.backBy)) {
      const over = Math.round((now - at(line.backBy)) / 60_000);
      out.push({
        key: `late-${a.name}`,
        board,
        boardTitle: titleOf(board),
        who: a.name,
        text: `${a.name} said back by ${clockTime(new Date(at(line.backBy)).toISOString())}, now ${over}m over`,
        detail: task ? task.id : "no task",
        late: true,
      });
    } else if (snap.presence[a.name] === "idle" && !busy.has(a.name) && minutesSince(snap.presenceSince[a.name] ?? 0, now) >= 30) {
      const idle = Math.round(minutesSince(snap.presenceSince[a.name] ?? 0, now));
      out.push({ key: `idle-${a.name}`, board, boardTitle: titleOf(board), who: a.name, text: `${a.name} has been idle for ${idle < 60 ? `${idle}m` : `${Math.floor(idle / 60)}h`}`, detail: "no task", late: true });
    } else if (line && minutesSince(line.t, now) > staleAfter && snap.presence[a.name] === "working") {
      out.push({ key: `stale-${a.name}`, board, boardTitle: titleOf(board), who: a.name, text: `${a.name} hasn't said what it's on for ${Math.floor(minutesSince(line.t, now) / 60)}h`, detail: task ? task.id : "no task", late: false });
    }
  }
  for (const n of scenario.notices ?? []) out.push({ key: `${n.board}-${n.who}`, ...n, boardTitle: titleOf(n.board), late: true });
  return out;
}

export type Status = { text: string; tone: "needs" | "late" | "quiet" | "plain" };

/** statusOf is what an agent is doing right now, in a few words, and how loudly to say it. */
export function statusOf(snap: Snapshot, name: string, asks: Ask[], now: number): Status {
  if (asks.some((a) => a.from === name && !a.ahead && a.board === scenario.board.name)) return { text: "waiting on you", tone: "needs" };
  const line = snap.now[name];
  const presence = snap.presence[name];
  if (line?.backBy !== undefined && now > at(line.backBy)) {
    return { text: `${line.text}, ${Math.round((now - at(line.backBy)) / 60_000)}m over`, tone: "late" };
  }
  if (presence === "no_session") return { text: "disconnected", tone: "quiet" };
  if (presence === "idle") {
    const busy = snap.tasks.some((t) => active(t) && (t.owner === name || t.with?.includes(name)));
    const idle = Math.round(minutesSince(snap.presenceSince[name] ?? 0, now));
    const span = idle < 60 ? `${idle}m` : `${Math.floor(idle / 60)}h`;
    if (!busy) return { text: `idle ${span}`, tone: "late" };
    return { text: line?.text ?? "idle", tone: "quiet" };
  }
  if (line?.backBy !== undefined) return { text: `${line.text}, back by ${clockTime(new Date(at(line.backBy)).toISOString())}`, tone: "plain" };
  return { text: line?.text ?? "no now line", tone: line ? "plain" : "quiet" };
}

export const toneClass: Record<Status["tone"], string> = {
  needs: "text-[var(--needs)]",
  late: "text-[var(--late)]",
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
