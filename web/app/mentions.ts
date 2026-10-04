// Mentions: `@name` and `@role:R` in a message's text. In the message box they set who
// the message goes to; in the timeline they show as names. A mention counts only when
// it names someone on the board or one of its roles, so an email address or a stray
// "@" stays plain text.

import type { Member, Message } from "./api";
import { recipient } from "./words";

// A mention starts after a space, a line start or punctuation, never inside a word
// (so "a@b.dev" is not one), and ends where a name can't go on.
const mentionPattern = /(^|[^a-z0-9_@.-])@(role:[a-z][a-z0-9-]{0,31}|[a-z0-9][a-z0-9-]{0,39})(?![a-z0-9-])/g;

/** targetOf turns what follows "@" into a target: "role:reviewer" stays, "codex" is "@codex". */
function targetOf(word: string): string {
  return word.startsWith("role:") ? word : `@${word}`;
}

/** A text segment: plain text, or a mention with the target it names. */
export type Segment = { text: string; target?: string };

/**
 * segments splits text into plain runs and mentions of known targets ("@codex",
 * "role:reviewer"). Joined back, the segments are the text unchanged.
 */
export function segments(text: string, known: Set<string>): Segment[] {
  const out: Segment[] = [];
  let last = 0;
  for (const m of text.matchAll(mentionPattern)) {
    const target = targetOf(m[2]);
    if (!known.has(target)) continue;
    const at = m.index + m[1].length;
    if (at > last) out.push({ text: text.slice(last, at) });
    out.push({ text: `@${m[2]}`, target });
    last = at + 1 + m[2].length;
  }
  if (last < text.length) out.push({ text: text.slice(last) });
  return out;
}

/** mentionsIn lists the known targets a text mentions, each once, in the order written. */
export function mentionsIn(text: string, known: Set<string>): string[] {
  const out: string[] = [];
  for (const s of segments(text, known)) {
    if (s.target && !out.includes(s.target)) out.push(s.target);
  }
  return out;
}

/** knownTargets is every target a mention may name on a board: its members and its roles. */
export function knownTargets(members: Pick<Member, "name">[], roles: string[]): Set<string> {
  return new Set([...members.map((m) => `@${m.name}`), ...roles.map((r) => `role:${r}`)]);
}

/** Query is a mention being typed: where its "@" is and what follows it so far. */
export type Query = { start: number; text: string };

/** queryAt finds the mention being typed just before the caret, if any. */
export function queryAt(text: string, caret: number): Query | null {
  const m = /(^|[^a-z0-9_@.-])@((?:role:)?[a-z0-9-]*)$/i.exec(text.slice(0, caret));
  if (!m) return null;
  return { start: m.index + m[1].length, text: m[2].toLowerCase() };
}

/** Candidate is one suggestion the mention list offers. */
export type Candidate = {
  /** target is what the message's `to` gets: "@codex" or "role:reviewer". */
  target: string;
  /** label is what the list shows and what is written after "@": "codex" or "role:reviewer". */
  label: string;
  kind: "agent" | "human" | "role";
  member?: Member;
};

/**
 * candidates lists who a mention can name, filtered by what has been typed after "@":
 * agents, then people, then roles; names that start with it before names that merely
 * contain it. The person writing is left out.
 */
export function candidates(members: Member[], roles: string[], me: string | null, typed: string): Candidate[] {
  const all: Candidate[] = [
    ...members.filter((m) => m.kind === "agent").map((m) => ({ target: `@${m.name}`, label: m.name, kind: "agent" as const, member: m })),
    ...members
      .filter((m) => m.kind === "human" && m.name !== me)
      .map((m) => ({ target: `@${m.name}`, label: m.name, kind: "human" as const, member: m })),
    ...roles.map((r) => ({ target: `role:${r}`, label: `role:${r}`, kind: "role" as const })),
  ];
  if (!typed) return all;
  const words = (c: Candidate) => (c.kind === "role" ? [c.label, c.label.slice(5)] : [c.label]);
  const starts = all.filter((c) => words(c).some((w) => w.startsWith(typed)));
  const contains = all.filter((c) => !starts.includes(c) && words(c).some((w) => w.includes(typed)));
  return [...starts, ...contains];
}

/**
 * replyRecipients is who a reply goes to unless the person changes it, the same set the
 * server picks for a reply without `to`: the author of the message it answers, then
 * the authors of the thread's first message and its replies in order, and every member
 * one of them was addressed to by name; never the person writing, and only members
 * still on the board. Empty when no one else is in the thread.
 */
export function replyRecipients(replyTo: Message, thread: Message[], members: Pick<Member, "name">[], me: string | null): string[] {
  const here = new Set(members.map((m) => m.name));
  const out: string[] = [];
  const add = (name: string) => {
    if (name !== me && here.has(name) && !out.includes(`@${name}`)) out.push(`@${name}`);
  };
  for (const m of [replyTo, ...thread.filter((t) => t.id !== replyTo.id)]) {
    add(m.from.name);
    for (const t of m.to) if (t.startsWith("@")) add(t.slice(1));
  }
  return out;
}

/**
 * toSummary is the "To" label's words for a set of targets: "everyone" for none,
 * "claude", "codex, claude", then "codex and 2 others".
 */
export function toSummary(to: string[]): string {
  if (to.length === 0 || (to.length === 1 && to[0] === "all")) return "everyone";
  if (to.length <= 2) return to.map(recipient).join(", ");
  return `${recipient(to[0])} and ${to.length - 1} others`;
}

/** toWords lists every target in words for the field's label: "claude and role reviewer". */
export function toWords(to: string[]): string {
  const words = to.length === 0 ? ["everyone"] : to.map(recipient);
  if (words.length <= 1) return words.join("");
  return `${words.slice(0, -1).join(", ")} and ${words.at(-1)}`;
}
