// An agent's status: one word for what it is doing now, and a small mark in one of five
// calm colours (D218). The word comes from the API's facts: the server's state word
// where it sends one, else the session's presence, refined by the open blocking asks the
// agent is waiting on. The colour never stands alone: the word sits beside it, and the
// sentence is in its title and its accessible name.

import { cn } from "@/lib/utils";
import type { Member, MemberRef, Task } from "./api";

/** Tone is a status's colour: working, needs a person, on hold, idle, or off (no session). */
export type Tone = "working" | "needs" | "hold" | "idle" | "off";

export type Status = {
  tone: Tone;
  /** word is the short form shown beside the mark ("working", "waiting on you"). */
  word: string;
  /** sentence says what the word means, for a tooltip and assistive technology. */
  sentence: string;
};

/** Block is an open blocking ask the agent is waiting on, and who it asked. */
export type Block = { to: MemberRef };

type Agent = Pick<Member, "name" | "owner" | "presence"> & Partial<Pick<Member, "state" | "line">>;

/** blocksOf lists the open blocking asks each agent is waiting on, from the board's live tasks. */
export function blocksOf(tasks: Task[]): Map<string, Block[]> {
  const out = new Map<string, Block[]>();
  for (const t of tasks) {
    if (t.state !== "open" && t.state !== "in_progress") continue;
    for (const b of t.blocked_on ?? []) {
      const from = b.from ?? t.owner;
      if (!from || from.kind !== "agent") continue;
      out.set(from.name, [...(out.get(from.name) ?? []), { to: b.to }]);
    }
  }
  return out;
}

/**
 * agentStatus works an agent's status out in the API's order: waiting on a person in
 * its session, paused or late, disconnected, working, then, for an idle agent, the
 * blocking ask it is waiting on (a person first), else idle.
 */
export function agentStatus(agent: Agent, blocks: Block[] = [], me: string | null = null): Status {
  const presence = agent.presence ?? "no_session";
  const state = agent.state ?? null;
  const name = agent.name;
  const yours = me !== null && agent.owner === me;
  if (presence === "waiting" || state === "waiting") {
    return {
      tone: "needs",
      word: "waiting",
      sentence: `${name}'s session is waiting for ${yours ? "you" : agent.owner ? agent.owner : "its person"}, such as at a permission prompt.`,
    };
  }
  if (state === "paused" || state === "late") {
    const on = agent.line?.kind === "paused" && agent.line.text ? ` on ${agent.line.text}` : "";
    return state === "late"
      ? { tone: "hold", word: "late", sentence: `${name} paused${on} and is past the time it said it would be back.` }
      : { tone: "hold", word: "paused", sentence: `${name} is paused${on} and will be back.` };
  }
  if (presence === "no_session" || state === "disconnected") {
    return { tone: "off", word: "disconnected", sentence: `${name} has no session open.` };
  }
  if (presence === "working" || state === "working") {
    return { tone: "working", word: "working", sentence: `${name} is working: a turn is running in its session.` };
  }
  const person = blocks.find((b) => b.to.kind === "human");
  if (person) {
    const who = person.to.name === me ? "you" : person.to.name;
    return { tone: "needs", word: `waiting on ${who}`, sentence: `${name} asked ${who} and waits for the answer.` };
  }
  if (blocks.length > 0) {
    const who = blocks[0].to.name;
    return { tone: "hold", word: `blocked on ${who}`, sentence: `${name} asked ${who} and waits for the answer.` };
  }
  return { tone: "idle", word: "idle", sentence: `${name}'s session is open and waiting for messages.` };
}

/**
 * StatusDot is a status's mark: a filled dot for working, idle and needing a person (the
 * last with a soft halo), two bars for on hold, and a ring for no session, so the tones
 * differ in shape as well as colour. Decorative: the word beside it says the status.
 */
export function StatusDot({ tone, className }: { tone: Tone; className?: string }) {
  if (tone === "hold") {
    return (
      <span aria-hidden data-tone={tone} className={cn("status-dot inline-flex size-2 shrink-0 justify-between", className)}>
        <span className="h-full w-[3px] rounded-[1px] bg-status-hold" />
        <span className="h-full w-[3px] rounded-[1px] bg-status-hold" />
      </span>
    );
  }
  return (
    <span
      aria-hidden
      data-tone={tone}
      className={cn(
        "status-dot inline-block size-2 shrink-0 rounded-full transition-colors duration-200 ease-out",
        tone === "working" && "bg-status-working",
        tone === "needs" && "bg-status-needs shadow-[0_0_0_2px_color-mix(in_oklab,var(--status-needs)_28%,transparent)]",
        tone === "idle" && "bg-status-idle",
        tone === "off" && "border-[1.5px] border-muted",
        className,
      )}
    />
  );
}

/** StatusWord is a status's word, with its sentence on hover and for assistive technology. */
export function StatusWord({ status, dot = false, className }: { status: Status; dot?: boolean; className?: string }) {
  return (
    <span
      className={cn("status inline-flex min-w-0 items-center gap-1.5 text-meta", status.tone === "working" || status.tone === "needs" ? "text-ink" : "text-muted", className)}
      data-tone={status.tone}
      title={status.sentence}
    >
      {dot && <StatusDot tone={status.tone} />}
      <span className="presence truncate">{status.word}</span>
      <span className="sr-only">. {status.sentence}</span>
    </span>
  );
}
