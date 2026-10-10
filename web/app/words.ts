// The words the board view puts on screen, built from facts the API returns. Nothing
// here is generated prose: each sentence comes from a fixed form.

import type { Board, BoardEvent, DeliveryMode, Member, MemberRef, Message, Policy, Presence } from "./api";
import type { SettableMode } from "./delivery-modes.gen";

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
  // The API says no_session; people read "disconnected".
  no_session: "disconnected",
};

/** appliedMode names a mode a delivery daemon reports; auto is all's earlier name. */
export function appliedMode(m: DeliveryMode): SettableMode {
  return m === "auto" ? "all" : m;
}

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
 * identitiesOf gives each agent of a board its identity colour: the one its id picks,
 * or, when a person or an earlier agent already has that colour, the next free one.
 * Agents keep their join order, so each keeps its colour; past eight senders colours
 * repeat. taken holds the people's colours, which never move.
 */
export function identitiesOf(ids: string[], taken: Set<number> = new Set()): Map<string, number> {
  const out = new Map<string, number>();
  const used = new Set(taken);
  for (const id of ids) {
    let n = identityOf(id);
    if (used.size < identities) {
      while (used.has(n)) n = (n % identities) + 1;
    }
    used.add(n);
    out.set(id, n);
  }
  return out;
}

/**
 * personIdentity is a person's identity colour, the same on every board and page: from
 * their id when it is you (GET /v1/me), else from their name, which is unique on a server.
 */
export function personIdentity(name: string, me: { id: string; name: string } | null): number {
  return identityOf(me && me.name === name ? me.id : `human:${name}`);
}

// Names agents get from their harness, and the two letters their marks show.
const harnessMarks: Record<string, string> = {
  claude: "CL",
  codex: "CX",
  opencode: "OC",
  openclaw: "OW",
  hermes: "HE",
  pi: "PI",
};

/**
 * markOf is the one or two characters a sender mark shows. An agent named after its
 * harness shows the harness's two letters (claude CL, codex CX); a later seat shows the
 * harness's first letter and its number (claude-2 C2, agent-3 A3). Any other name shows
 * the initials of its first two words (docs-bot DB), or its first two letters (scout
 * SC). A person shows their initials: one letter for a one-word name (leo L).
 */
export function markOf(name: string, kind: "agent" | "human"): string {
  const words = name.split(/[-_.\s]+/).filter(Boolean);
  const first = (w: string) => (w.match(/[a-z0-9]/i)?.[0] ?? "?").toUpperCase();
  if (kind === "human") return words.slice(0, 2).map(first).join("") || "?";
  const [base, seat] = words;
  if (words.length === 1 && harnessMarks[base]) return harnessMarks[base];
  if (words.length === 2 && /^\d+$/.test(seat)) return first(base) + seat;
  if (words.length >= 2) return first(words[0]) + first(words[1]);
  return (base ?? "?").replace(/[^a-z0-9]/gi, "").slice(0, 2).toUpperCase() || "?";
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

/**
 * recipient turns a target into words: all is "everyone", @claude is "claude", and
 * role:member is "role member", so a role never reads like everyone.
 */
export function recipient(t: string): string {
  if (t === "all") return "everyone";
  if (t.startsWith("@")) return t.slice(1);
  if (t.startsWith("owner:")) return `${t.slice(6)}’s agents`;
  if (t.startsWith("role:")) return `role ${t.slice(5)}`;
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
  /** newReplies counts replies the person hasn't seen in closed threads; target is the first. */
  newReplies?: { count: number; threads: number; target: string } | null;
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
    const disconnected = by("no_session");
    if (working > 0) parts.push({ text: `${count(working, "agent", "agents")} working` });
    if (idle > 0) parts.push({ text: `${count(idle, "agent", "agents")} idle` });
    if (disconnected > 0) parts.push({ text: `${count(disconnected, "agent", "agents")} disconnected` });
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
  if (f.newReplies) {
    const n = f.newReplies;
    parts.push({
      text: `${count(n.count, "new reply", "new replies")} in ${n.threads === 1 ? "a thread" : `${n.threads} threads`}`,
      target: n.target,
    });
  }
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
export function eventLine(e: BoardEvent, creator: string | null, solo: boolean, names?: AuthNames): string | null {
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
      const joined = solo ? `${name} joined` : `${name} joined as ${d.access === "admin" ? "admin" : "member"}`;
      return joined + authorizedBy(d.authorization, names);
    }
    case "person.added": {
      // A person an agent brought in for its person, so the record says who acted on whose authority.
      const by = authorizedBy(d.authorization, names);
      return by ? `${String(d.name ?? "")} joined as member${by}` : null;
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
    case "board.archived":
      return `${who} archived the board`;
    case "board.restored":
      return `${who} restored the board`;
    case "agent.delivery_changed":
      return `${who} set ${String(d.name ?? "")}'s delivery mode to ${String(d.after ?? "")}`;
    case "agent.removed": {
      // The record names who removed the agent, so its person sees who did it.
      const name = String(d.name ?? "");
      const owner = String(d.owner ?? "");
      const whose = owner && owner !== e.actor.name ? `${owner}'s agent ${name}` : name;
      return d.pruned ? `${who} removed ${whose}, disconnected for a while` : `${who} removed ${whose}`;
    }
    case "agent.left":
      return `${String(d.name ?? "")} left the board`;
    default:
      return null;
  }
}

/** AuthNames names the agent and person an event's authorization holds by id. */
export type AuthNames = { agent: (id: string) => string | null; person: (id: string) => string | null };

/**
 * authorizedBy is how the record reads a join an agent did for its person:
 * " · invited by writer, approved by alex", or " · added by writer, on alex's allowance".
 * Empty for a join nobody's agent did.
 */
function authorizedBy(raw: unknown, names?: AuthNames): string {
  const a = raw as { kind?: string; agent_id?: string; person_id?: string; via?: string } | undefined;
  if (!a?.via || !a.agent_id || !a.person_id) return "";
  const agent = names?.agent(a.agent_id) ?? "an agent";
  const person = names?.person(a.person_id) ?? "its person";
  const verb = a.kind === "invited" ? "invited" : a.kind === "role_changed" ? "changed" : "added";
  return a.via === "allowance" ? ` · ${verb} by ${agent}, on ${person}'s allowance` : ` · ${verb} by ${agent}, approved by ${person}`;
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

/** A charter block: a paragraph, or a list of the lines that start with "- ". */
export type CharterBlock = { kind: "paragraph"; text: string } | { kind: "list"; items: string[] };

/**
 * charterBlocks reads a charter the way its author wrote it in YAML: single line
 * breaks inside a paragraph join with a space, a blank line starts a new paragraph,
 * and lines starting with "- " are list items.
 */
export function charterBlocks(text: string): CharterBlock[] {
  const out: CharterBlock[] = [];
  for (const chunk of text.replace(/\r\n?/g, "\n").split(/\n\s*\n/)) {
    let words: string[] = [];
    let items: string[] = [];
    const flushWords = () => {
      if (words.length) out.push({ kind: "paragraph", text: words.join(" ") });
      words = [];
    };
    const flushItems = () => {
      if (items.length) out.push({ kind: "list", items });
      items = [];
    };
    for (const raw of chunk.split("\n")) {
      const line = raw.trim();
      if (!line) continue;
      if (/^[-*] /.test(line)) {
        flushWords();
        items.push(line.slice(2).trim());
      } else if (items.length && /^\s{2,}/.test(raw)) {
        items[items.length - 1] += ` ${line}`;
      } else {
        flushItems();
        words.push(line);
      }
    }
    flushWords();
    flushItems();
  }
  return out;
}
