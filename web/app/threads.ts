"use client";

// Threads in the board view: a message that replies to nothing starts a thread, and
// every reply joins the thread of the message it answers (the API's thread_root), so a
// thread is one level deep. This browser remembers, per board, which threads the person
// left open and the newest reply they have seen in each.

import { useCallback, useMemo, useState } from "react";
import type { Message } from "./api";
import { readStored, store } from "./prefs";

type Kept = { open: Record<string, boolean>; seen: Record<string, number> };

// Only the most recent threads are remembered, so the stored value stays small.
const keep = 300;

function trim(r: Record<string, number | boolean>): Record<string, never> {
  const keys = Object.keys(r);
  if (keys.length <= keep) return r as Record<string, never>;
  return Object.fromEntries(keys.slice(keys.length - keep).map((k) => [k, r[k]])) as Record<string, never>;
}

export type ThreadPrefs = {
  /** open is the person's choice for a thread: true open, false closed, undefined never chosen. */
  open: (root: string) => boolean | undefined;
  setOpen: (root: string, open: boolean) => void;
  /** seen is the newest reply seq the person has seen in a thread, if this browser knows. */
  seen: (root: string) => number | undefined;
  see: (root: string, seq: number) => void;
};

export function useThreadPrefs(board: string): ThreadPrefs {
  const key = `aboard.threads.${board}`;
  const [kept, setKept] = useState<Kept>(() => {
    try {
      const v = JSON.parse(readStored(key) ?? "") as Kept;
      return { open: v.open ?? {}, seen: v.seen ?? {} };
    } catch {
      return { open: {}, seen: {} };
    }
  });
  const update = useCallback(
    (change: (k: Kept) => Kept) =>
      setKept((k) => {
        const next = change(k);
        if (next === k) return k;
        store(key, JSON.stringify({ open: trim(next.open), seen: trim(next.seen) }));
        return next;
      }),
    [key],
  );
  return useMemo(
    () => ({
      open: (root: string) => kept.open[root],
      setOpen: (root: string, open: boolean) =>
        update((k) => (k.open[root] === open ? k : { ...k, open: { ...k.open, [root]: open } })),
      seen: (root: string) => kept.seen[root],
      see: (root: string, seq: number) =>
        update((k) => ((k.seen[root] ?? 0) >= seq ? k : { ...k, seen: { ...k.seen, [root]: seq } })),
    }),
    [kept, update],
  );
}

/** rootOf is the id of the thread a message is in: its own id when it replies to nothing. */
export function rootOf(m: Message): string {
  return m.thread_root ?? m.id;
}

/** threadsOf groups replies by the thread they are in, oldest first. */
export function threadsOf(messages: Message[]): Map<string, Message[]> {
  const out = new Map<string, Message[]>();
  for (const m of messages) {
    if (!m.thread_root) continue;
    const list = out.get(m.thread_root);
    if (list) list.push(m);
    else out.set(m.thread_root, [m]);
  }
  for (const list of out.values()) list.sort((a, b) => a.seq - b.seq);
  return out;
}
