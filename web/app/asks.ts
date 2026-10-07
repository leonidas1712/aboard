import type { Board, Member, Message, Task } from "./api";

export type Notice = { id: string; board: string; title: string; who: string; text: string; detail: string; task?: string };
export type BoardFacts = { board: Board; members: Member[]; tasks: Task[] };

/** The thresholds belong to this view; presence, ownership and blocking come from the API. */
export function worthALook(facts: BoardFacts[], me: string, now: number): Notice[] {
  const notices: Notice[] = [];
  for (const { board, members, tasks } of facts) {
    const agents = members.filter((m) => m.kind === "agent" && (!m.status || m.status === "active"));
    const active = tasks.filter((t) => t.state === "in_progress");
    const occupied = new Set(active.flatMap((t) => [t.owner?.name, ...t.with.map((m) => m.name)]).filter(Boolean));
    for (const agent of agents) {
      if (agent.presence !== "idle" || occupied.has(agent.name) || !agent.presence_since) continue;
      const minutes = Math.floor((now - Date.parse(agent.presence_since)) / 60_000);
      if (minutes < 30) continue;
      notices.push({ id: `${board.id}:${agent.id}:idle`, board: board.name, title: board.title ?? board.name, who: agent.name, text: `${agent.name} has been idle for ${minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h`}`, detail: "no task" });
    }
    for (const task of active) {
      for (const block of task.blocked_on) {
        if (block.to.kind === "human" && block.to.name === me) continue;
        const hours = Math.floor((now - Date.parse(block.since)) / 3_600_000);
        if (hours < 3) continue;
        notices.push({ id: `${board.id}:${task.id}:${block.ask_id}`, board: board.name, title: board.title ?? board.name, who: task.owner?.name ?? block.to.name, task: task.ref, text: `${task.ref} blocked on ${block.to.name} for ${hours}h`, detail: task.title });
      }
      const owner = agents.find((a) => a.name === task.owner?.name);
      if (owner?.presence !== "no_session" || !owner.presence_since) continue;
      const minutes = Math.floor((now - Date.parse(owner.presence_since)) / 60_000);
      if (minutes < 30) continue;
      notices.push({ id: `${board.id}:${task.id}:disconnected`, board: board.name, title: board.title ?? board.name, who: owner.name, task: task.ref, text: `${task.ref}'s owner ${owner.name} has been disconnected for ${minutes < 60 ? `${minutes}m` : `${Math.floor(minutes / 60)}h`}`, detail: task.title });
    }
  }
  return notices;
}

export function orderedAsks(asks: Message[]): Message[] {
  return [...asks].filter((m) => m.ask?.state === "open").sort((a, b) => Number(b.ask!.blocking) - Number(a.ask!.blocking) || b.at.localeCompare(a.at) || a.id.localeCompare(b.id));
}
export function askCount(board: Board): number {
  return (board.asks_to_me?.blocking ?? 0) + (board.asks_to_me?.going_with ?? 0);
}
export function attentionCount(board: Board): number {
  return Math.max(0, (board.needs_reply ?? askCount(board)) - (board.asks_to_me?.going_with ?? 0));
}
