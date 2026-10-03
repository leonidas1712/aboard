// Verifying a board's record in the browser, the same check as `aboard audit verify`
// (spec/events.md): every event follows the one before it, its hash recomputes over its
// envelope, and its data_hash recomputes over its payload where the payload is visible.

import type { BoardEvent } from "./api";

export const genesisHash = `sha256:${"0".repeat(64)}`;

/**
 * canonical returns the RFC 8785 (JCS) form of a JSON value: object keys sorted by
 * UTF-16 code units, no whitespace, and strings and numbers written the way
 * JSON.stringify writes them, which is what JCS specifies.
 */
export function canonical(v: unknown): string {
  if (v === null || typeof v !== "object") {
    if (typeof v === "number" && !Number.isFinite(v)) throw new Error("canonical JSON: number is not finite");
    return JSON.stringify(v);
  }
  if (Array.isArray(v)) return `[${v.map(canonical).join(",")}]`;
  const obj = v as Record<string, unknown>;
  const keys = Object.keys(obj)
    .filter((k) => obj[k] !== undefined)
    .sort();
  return `{${keys.map((k) => `${JSON.stringify(k)}:${canonical(obj[k])}`).join(",")}}`;
}

async function sha256(text: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  const hex = Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("");
  return `sha256:${hex}`;
}

/** Why an event failed verification. */
export type Failure = "seq_gap" | "prev_hash_mismatch" | "hash_mismatch" | "data_hash_mismatch" | "head_changed";

export type Problem = { seq: number; reason: Failure };

/** Chain is how far a board's record has been verified. */
export type Chain = { lastSeq: number; lastHash: string; checked: number };

export const emptyChain: Chain = { lastSeq: 0, lastHash: genesisHash, checked: 0 };

/**
 * verify checks the next events after chain, in order. It returns the chain after the
 * last good event and the first problem, if any.
 */
export async function verify(chain: Chain, events: BoardEvent[]): Promise<{ chain: Chain; problem: Problem | null }> {
  let c = chain;
  for (const e of events) {
    if (e.seq !== c.lastSeq + 1) return { chain: c, problem: { seq: c.lastSeq + 1, reason: "seq_gap" } };
    if (e.prev_hash !== c.lastHash) return { chain: c, problem: { seq: e.seq, reason: "prev_hash_mismatch" } };
    const header = {
      id: e.id,
      board_id: e.board_id,
      seq: e.seq,
      type: e.type,
      at: e.at,
      actor: {
        kind: e.actor.kind,
        member_id: e.actor.member_id ?? null,
        name: e.actor.name ?? null,
        owner: e.actor.owner ?? null,
      },
      data_hash: e.data_hash,
      prev_hash: e.prev_hash,
    };
    if ((await sha256(canonical(header))) !== e.hash) return { chain: c, problem: { seq: e.seq, reason: "hash_mismatch" } };
    if (!e.data_withheld && (await sha256(canonical(e.data ?? null))) !== e.data_hash) {
      return { chain: c, problem: { seq: e.seq, reason: "data_hash_mismatch" } };
    }
    c = { lastSeq: e.seq, lastHash: e.hash, checked: c.checked + 1 };
  }
  return { chain: c, problem: null };
}

/** problemText says, in a sentence, what a failed check found. */
export function problemText(p: Problem): string {
  switch (p.reason) {
    case "seq_gap":
      return `Event ${p.seq} is missing from the record.`;
    case "prev_hash_mismatch":
      return `Event ${p.seq} doesn't follow the event before it.`;
    case "hash_mismatch":
      return `Event ${p.seq} was changed after it was written.`;
    case "data_hash_mismatch":
      return `The content of event ${p.seq} doesn't match its hash.`;
    case "head_changed":
      return `Event ${p.seq} is different from when this browser last checked it.`;
  }
}

// The last head this browser verified for each board, so a record rewritten later is
// caught even if the rewritten chain is consistent. Kept per browser; a browser that
// refuses storage just skips this part of the check.
const headKey = (boardId: string) => `aboard.verifiedHead.${boardId}`;

export function rememberedHead(boardId: string): { seq: number; hash: string } | null {
  try {
    const raw = localStorage.getItem(headKey(boardId));
    return raw ? (JSON.parse(raw) as { seq: number; hash: string }) : null;
  } catch {
    return null;
  }
}

export function rememberHead(boardId: string, seq: number, hash: string) {
  try {
    localStorage.setItem(headKey(boardId), JSON.stringify({ seq, hash }));
  } catch {
    // Not kept; the next visit checks the chain without it.
  }
}
