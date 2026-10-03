// The words the board view puts on screen, built from facts the API returns. Nothing
// here is generated prose: each sentence comes from a fixed form.

import type { Board, BoardEvent, Member, MemberRef, Message, Policy, Presence } from "./api";

const harnessNames: Record<string, string> = {
  "claude-code": "Claude Code",
  codex: "Codex",
  opencode: "OpenCode",
  pi: "Pi",
  openclaw: "OpenClaw",
  hermes: "Hermes",
};

/** harnessName is a harness as people know it ("Claude Code" for claude-code). */
export function harnessName(h: string | null | undefined): string | null {
  if (!h) return null;
  return harnessNames[h] ?? h;
}

export const presenceWords: Record<Presence, string> = {
  working: "working",
  idle: "idle",
  waiting: "waiting",
  no_session: "no session",
};

/** boardLabel is what people call a board: its title, else its name. */
export function boardLabel(b: Pick<Board, "name" | "title">): string {
  return b.title?.trim() || b.name;
}

/** identities is how many identity colours there are (--id-1 to --id-8 in globals.css). */
export const identities = 8;

/**
 * identityOf picks a member's identity colour, 1 to identities, from its id. The same
 * member always gets the same colour; the colour only tells senders apart.
 */
export function identityOf(id: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < id.length; i++) {
    h ^= id.charCodeAt(i);
    h = Math.imul(h, 0x01000193);
  }
  return ((h >>> 0) % identities) + 1;
}

/**
 * identitiesOf gives each member of a board its identity colour: the one its id picks,
 * or, when an earlier member already has that colour, the next free one. Members keep
 * their join order, so each keeps its colour; past eight members colours repeat.
 */
export function identitiesOf(ids: string[]): Map<string, number> {
  const out = new Map<string, number>();
  const taken = new Set<number>();
  for (const id of ids) {
    let n = identityOf(id);
    if (taken.size < identities) {
      while (taken.has(n)) n = (n % identities) + 1;
    }
    taken.add(n);
    out.set(id, n);
  }
  return out;
}

/** initialOf is the letter a sender mark shows. */
export function initialOf(name: string): string {
  return (name.match(/[a-z0-9]/i)?.[0] ?? "?").toUpperCase();
}

/** clockTime is a time of day, as in a chat's gutter ("14:05"). */
export function clockTime(at: string): string {
  return new Date(at).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

/** policyName names a policy preset as the board view says it. */
export function policyName(p: Policy): string {
  return p.preset === "starter" ? "Starter policy" : "Recommended policy";
}

/** displayName is a member's name, with its owner once a second owner is on the board. */
export function displayName(m: MemberRef, showOwner: boolean): string {
  return showOwner && m.kind === "agent" && m.owner ? `${m.name} · ${m.owner}` : m.name;
}

/** recipient turns a target into words: all is "everyone", @claude is "claude". */
export function recipient(t: string): string {
  if (t === "all") return "everyone";
  if (t.startsWith("@")) return t.slice(1);
  if (t.startsWith("role:")) return `every ${t.slice(5)}`;
  return t;
}

export function recipients(to: string[]): string {
  return to.map(recipient).join(", ");
}

const minute = 60_000;
const hour = 60 * minute;
const day = 24 * hour;

/** relativeTime says how long ago a time was, in the form people use in chats. */
export function relativeTime(at: string, now: number): string {
  const t = new Date(at).getTime();
  const ago = Math.max(0, now - t);
  if (ago < minute) return "just now";
  if (ago < hour) return `${Math.floor(ago / minute)} min ago`;
  if (ago < day) return `${Math.floor(ago / hour)} h ago`;
  if (ago < 2 * day) return "yesterday";
  const d = new Date(t);
  const sameYear = d.getFullYear() === new Date(now).getFullYear();
  return d.toLocaleDateString(undefined, sameYear ? { day: "numeric", month: "short" } : { day: "numeric", month: "short", year: "numeric" });
}

export function exactTime(at: string): string {
  return new Date(at).toLocaleString(undefined, { dateStyle: "full", timeStyle: "medium" });
}

export function count(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

/** roleNamesWith lists the roles on a board that have a permission. */
function roleNamesWith(board: Board, permission: string): string[] {
  return Object.entries(board.roles)
    .filter(([, r]) => r.can.includes(permission))
    .map(([name]) => name)
    .sort();
}

function list(words: string[]): string {
  if (words.length <= 1) return words.join("");
  return `${words.slice(0, -1).join(", ")} and ${words.at(-1)}`;
}

/** rules says a board's policy in plain sentences. */
export function rules(board: Board): string[] {
  const p: Policy = board.policy;
  const out: string[] = [];
  out.push(
    p.visibility === "open"
      ? "Everyone here can read every message."
      : "Agents read only the messages they send or that are sent to them. People here read every message.",
  );
  if (p.broadcast === "everyone") {
    out.push("Any agent can message everyone.");
  } else {
    const roles = roleNamesWith(board, "broadcast");
    out.push(
      roles.length > 0
        ? `Only agents with the ${list(roles)} ${roles.length === 1 ? "role" : "roles"} can message everyone; the rest message names or roles.`
        : "Agents message names or roles; only people can message everyone.",
    );
  }
  out.push("Each agent is woken when a message arrives for it, as its owner's delivery setting allows.");
  if (p.show_harness === false) out.push("Agents don't see which harness other agents run.");
  return out;
}

export type Facts = {
  agents: Member[];
  /** questions are messages to the person that ask for a reply and have none from them yet. */
  questions: Message[];
  lastActivity: string | null;
};

export type NowPart = { text: string; attention?: boolean; target?: string };

/**
 * nowLine builds the "Now:" line from facts, in a fixed form, for example
 * "1 agent working · 2 agents idle · nothing waiting on you".
 */
export function nowLine(f: Facts, now: number): NowPart[] {
  const parts: NowPart[] = [];
  if (f.agents.length === 0) {
    parts.push({ text: "no agents here yet" });
  } else {
    const by = (p: Presence) => f.agents.filter((a) => (a.presence ?? "no_session") === p).length;
    const working = by("working");
    const idle = by("idle");
    const away = by("no_session");
    if (working > 0) parts.push({ text: `${count(working, "agent", "agents")} working` });
    if (idle > 0) parts.push({ text: `${count(idle, "agent", "agents")} idle` });
    if (away > 0) parts.push({ text: `${count(away, "agent", "agents")} with no session` });
  }
  const waiting = f.agents.filter((a) => a.presence === "waiting");
  for (const a of waiting) parts.push({ text: `${a.name} is waiting for you in its session`, attention: true });
  if (f.questions.length > 0) {
    const q = f.questions[0];
    parts.push({
      text:
        f.questions.length === 1
          ? `${q.from.name} asked you a question`
          : `${count(f.questions.length, "question", "questions")} waiting for your reply`,
      attention: true,
      target: q.id,
    });
  }
  if (waiting.length === 0 && f.questions.length === 0) parts.push({ text: "nothing waiting on you" });
  if (f.lastActivity) parts.push({ text: `last message ${relativeTime(f.lastActivity, now)}` });
  return parts;
}

/** addressedTo says whether a message reached a person by name or by everyone. */
export function addressedTo(m: Message, person: string | null): boolean {
  return m.to.includes("all") || (person !== null && m.to.includes(`@${person}`));
}

/**
 * eventLine is the short line a board event shows in the timeline, or null for events
 * the timeline doesn't show (join codes, messages, types this page doesn't know).
 */
export function eventLine(e: BoardEvent, creator: string | null, solo: boolean): string | null {
  if (e.data_withheld) return null;
  const who = e.actor.name ?? "The server";
  const d = (e.data ?? {}) as Record<string, unknown>;
  switch (e.type) {
    case "board.created":
      return `${who} created the board`;
    case "member.joined": {
      const name = String(d.name ?? "");
      if (d.kind === "agent") {
        const harness = harnessName(d.harness as string | null);
        return `${name} joined as ${d.role}${harness ? `, on ${harness}` : ""}`;
      }
      // The creator's own join follows the board's creation and adds nothing to it.
      if (name === creator) return null;
      return solo ? `${name} joined` : `${name} joined as ${d.access === "admin" ? "admin" : "member"}`;
    }
    case "board.policy_changed": {
      const preset = d.preset_applied as string | null;
      if (preset) return `${who} switched the board to the ${preset} policy`;
      const before = (d.before ?? {}) as Record<string, unknown>;
      const after = (d.after ?? {}) as Record<string, unknown>;
      const changed = Object.keys(after).filter((k) => k !== "overrides" && JSON.stringify(after[k]) !== JSON.stringify(before[k]));
      return changed.length > 0
        ? `${who} changed the rules: ${changed.map((k) => `${k.replace("_", " ")} is now ${String(after[k])}`).join(", ")}`
        : `${who} changed the rules`;
    }
    case "board.titled": {
      const after = d.after as string | null;
      return after ? `${who} titled the board “${after}”` : `${who} removed the board's title`;
    }
    default:
      return null;
  }
}

/**
 * eventMatches says whether a board event belongs in a timeline filtered to one sender
 * or role: the member's own actions and its join.
 */
export function eventMatches(e: BoardEvent, from: string | undefined, role: string | undefined): boolean {
  const d = (e.data ?? {}) as Record<string, unknown>;
  if (from) return e.actor.name === from || (e.type === "member.joined" && d.name === from);
  if (role) return e.type === "member.joined" && d.role === role;
  return true;
}
