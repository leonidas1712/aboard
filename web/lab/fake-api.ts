// An in-memory fake of the parts of Aboard's public API and event stream the web UI
// uses, served from window.fetch, so the real api.ts and every component above it run
// unchanged. It holds one scenario's board (plus a few empty ones for the board list),
// its members, messages and hash-chained events, and the person's own writes (messages,
// reactions, read position) until the page reloads. It is never a second server: when the
// UI needs something new from it, match spec/openapi.yaml.

import type {
  Board,
  BoardEvent,
  Member,
  MemberRef,
  Message,
  Presence,
  Reaction,
  ReactionName,
  Receipts,
  Session,
} from "@/app/api";
import { reactionSet } from "@/app/api";
import { canonical, genesisHash } from "@/app/record";
import { type ScenarioMessage, type Snapshot, snapshot } from "./scenario";
import { at, current, onStep, scenario } from "./store";

type Msg = {
  id: string;
  seq: number;
  atMs: number;
  from: string;
  to: string[];
  body: string;
  replyTo: string | null;
  asks: boolean;
  urgent: boolean;
  reactions: Map<ReactionName, string[]>;
};

type RawEvent = { seq: number; type: string; atMs: number; actor: string | null; data: Record<string, unknown> };

type World = {
  board: Board;
  members: Member[];
  messages: Msg[];
  events: RawEvent[];
  readUpTo: number;
  /** hashed is the events with their hashes, as far as they have been computed. */
  hashed: BoardEvent[];
};

const me = scenario.me;
const iso = (ms: number) => new Date(ms).toISOString();
const personId = (name: string) => `per_${name}`;
const memberId = (board: string, name: string) => `mem_${board}_${name}`;
const owners = new Set(scenario.agents.map((a) => a.owner ?? me));
const showOwner = owners.size > 1;

function policy(): Board["policy"] {
  return scenario.board.policy === "starter"
    ? { preset: "starter", visibility: "open", broadcast: "everyone", urgent: "everyone", overrides: [] }
    : { preset: "recommended", visibility: "addressed", broadcast: "granted", urgent: "granted", overrides: [] };
}

function newBoard(name: string, title: string | null, charter: string, createdMs: number): Board {
  return {
    id: `brd_lab_${name}`,
    name,
    title,
    charter,
    roles: Object.fromEntries(
      Object.entries(scenario.board.roles ?? { member: "Works on the board's tasks and talks to everyone." }).map(([r, c]) => [r, { charter: c, can: [] }]),
    ),
    policy: policy(),
    head_seq: 0,
    message_count: 0,
    last_message_at: null,
    created_at: iso(createdMs),
    created_by: { name: me, kind: "human", role: null, owner: null },
    visibility: scenario.people.length > 1 ? "open" : "private",
    on_board: true,
    read_up_to: 0,
    unread: 0,
    needs_reply: 0,
    lifecycle: "active",
    can_archive: true,
    can_delete: true,
  };
}

const worlds = new Map<string, World>();

function addEvent(w: World, type: string, atMs: number, actor: string | null, data: Record<string, unknown>) {
  const seq = w.events.length + 1;
  w.events.push({ seq, type, atMs, actor, data });
  w.board.head_seq = seq;
  return seq;
}

function join(w: World, m: Member, atMs: number) {
  w.members.push(m);
  addEvent(w, "member.joined", atMs, m.name, {
    name: m.name,
    kind: m.kind,
    role: m.role,
    harness: m.harness,
    access: m.access,
  });
}

function person(w: World, name: string, admin: boolean, atMs: number) {
  join(
    w,
    {
      id: memberId(w.board.name, name),
      name,
      kind: "human",
      role: null,
      owner: null,
      harness: null,
      access: admin ? "admin" : "member",
      server_role: "member",
      joined_at: iso(atMs),
      presence: null,
      presence_since: null,
      owner_id: null,
    },
    atMs,
  );
}

// The scenario's board, at minute 0, with the people on it from the start.
const main: World = { board: newBoard(scenario.board.name, scenario.board.title ?? null, scenario.board.charter ?? "", at(0)), members: [], messages: [], events: [], readUpTo: 0, hashed: [] };
addEvent(main, "board.created", at(0), me, { name: main.board.name, title: main.board.title });
for (const p of scenario.people) person(main, p.name, p.admin ?? p.name === me, at(0));
worlds.set(main.board.name, main);
for (const [i, o] of (scenario.otherBoards ?? []).entries()) {
  const w: World = { board: newBoard(o.name, o.title ?? null, "", at(-60 * 24 * (i + 2))), members: [], messages: [], events: [], readUpTo: 0, hashed: [] };
  addEvent(w, "board.created", at(-60 * 24 * (i + 2)), me, { name: o.name, title: w.board.title });
  person(w, me, true, at(-60 * 24 * (i + 2)));
  worlds.set(o.name, w);
}

function addMessage(w: World, m: Omit<Msg, "seq" | "reactions">) {
  const seq = addEvent(w, "message.posted", m.atMs, m.from, { message_id: m.id, to: m.to, body: m.body });
  w.messages.push({ ...m, seq, reactions: new Map() });
  w.board.message_count = w.messages.length;
  w.board.last_message_at = iso(m.atMs);
}

/** sync brings the scenario's board up to a snapshot: new agents, presence, messages. */
function sync(s: Snapshot): string[] {
  const w = main;
  const changed: string[] = [];
  const pending: ({ kind: "join"; t: number; name: string } | { kind: "message"; t: number; m: ScenarioMessage })[] = [];
  for (const a of scenario.agents) {
    if ((a.joined ?? 0) <= s.step.at && !w.members.some((m) => m.name === a.name)) pending.push({ kind: "join", t: a.joined ?? 0.5, name: a.name });
  }
  for (const m of s.messages) if (!w.messages.some((x) => x.id === m.id)) pending.push({ kind: "message", t: m.t, m });
  pending.sort((a, b) => a.t - b.t);
  for (const p of pending) {
    if (p.kind === "join") {
      const a = scenario.agents.find((x) => x.name === p.name)!;
      const owner = a.owner ?? me;
      join(
        w,
        {
          id: memberId(w.board.name, a.name),
          name: a.name,
          kind: "agent",
          role: a.role ?? "member",
          owner,
          harness: a.harness,
          access: null,
          server_role: null,
          joined_at: iso(at(p.t)),
          presence: "idle",
          presence_since: iso(at(p.t)),
          delivery: "focused",
          owner_id: personId(owner),
          delivery_mode: "focused",
          delivery_revision: 1,
        },
        at(p.t),
      );
    } else {
      const m = p.m;
      addMessage(w, {
        id: m.id,
        atMs: at(m.t),
        from: m.from,
        to: m.to ?? ["all"],
        body: m.body,
        replyTo: m.replyTo ?? null,
        asks: m.asks ?? false,
        urgent: m.urgent ?? false,
      });
    }
  }
  for (const m of w.members) {
    if (m.kind !== "agent") continue;
    const p: Presence = s.presence[m.name] ?? "idle";
    const since = iso(at(s.presenceSince[m.name] ?? 0));
    if (m.presence !== p) changed.push(m.name);
    m.presence = p;
    m.presence_since = since;
  }
  return changed;
}

// --- reading the world as the API does ---

function ref(w: World, name: string): MemberRef {
  const m = w.members.find((x) => x.name === name);
  return { name, kind: m?.kind ?? "agent", role: m?.role ?? null, owner: m?.owner ?? null, harness: m?.harness ?? null };
}

function rootOf(w: World, m: Msg): Msg | null {
  let r: Msg | undefined = m;
  while (r?.replyTo) {
    const up = w.messages.find((x) => x.id === r!.replyTo);
    if (!up) break;
    r = up;
  }
  return r && r.id !== m.id ? r : null;
}

function reactionsOf(m: Msg): Reaction[] {
  return reactionSet.flatMap((r) => {
    const by = m.reactions.get(r.name) ?? [];
    return by.length ? [{ name: r.name, emoji: r.emoji, count: by.length, by, mine: by.includes(me) }] : [];
  });
}

function message(w: World, m: Msg): Message {
  const from = ref(w, m.from);
  const root = rootOf(w, m);
  const replies = w.messages.filter((x) => x.id !== m.id && rootOf(w, x)?.id === m.id);
  const replyTo = m.replyTo ? w.messages.find((x) => x.id === m.replyTo) : undefined;
  const agent = scenario.agents.find((a) => a.name === m.from);
  return {
    id: m.id,
    seq: m.seq,
    at: iso(m.atMs),
    from,
    to: m.to,
    body: m.body,
    reply_to: m.replyTo,
    reply_to_seq: replyTo?.seq ?? null,
    thread_root: root?.id ?? null,
    thread_root_seq: root?.seq ?? null,
    reply_count: replies.length,
    last_reply_at: replies.length ? iso(replies.at(-1)!.atMs) : null,
    urgent: m.urgent,
    expects_reply: m.asks,
    sender:
      m.from === me
        ? "self"
        : from.kind === "human"
          ? "other_person"
          : (agent?.owner ?? me) === me
            ? "owner_agent"
            : "other_agent",
    show_owner: showOwner,
    reactions: reactionsOf(m),
    mentions: [],
  };
}

function toMe(m: Msg): boolean {
  return m.to.includes("all") || m.to.includes(`@${me}`);
}

function boardView(w: World): Board {
  const unread = w.messages.filter((m) => m.seq > w.readUpTo && m.from !== me).length;
  const needs = w.messages.filter(
    (m) => m.asks && m.to.includes(`@${me}`) && !w.messages.some((r) => r.replyTo === m.id && r.from === me),
  ).length;
  return { ...w.board, read_up_to: w.readUpTo, unread, needs_reply: needs };
}

async function sha256(text: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return `sha256:${Array.from(new Uint8Array(digest), (b) => b.toString(16).padStart(2, "0")).join("")}`;
}

// hashed returns the board's events with their hashes, computing the ones not done yet.
async function hashed(w: World): Promise<BoardEvent[]> {
  while (w.hashed.length < w.events.length) {
    const e = w.events[w.hashed.length];
    const prev = w.hashed.at(-1)?.hash ?? genesisHash;
    const actorMember = e.actor ? w.members.find((m) => m.name === e.actor) : undefined;
    const actor: BoardEvent["actor"] = e.actor
      ? { kind: actorMember?.kind ?? "human", member_id: actorMember?.id ?? null, name: e.actor, owner: actorMember?.owner ?? null }
      : { kind: "system", member_id: null, name: null, owner: null };
    const header = {
      id: `evt_${w.board.name}_${e.seq}`,
      board_id: w.board.id,
      seq: e.seq,
      type: e.type,
      at: iso(e.atMs),
      actor,
      data_hash: await sha256(canonical(e.data)),
      prev_hash: prev,
    };
    w.hashed.push({ ...header, data: e.data, hash: await sha256(canonical(header)) });
  }
  return w.hashed;
}

// --- the event stream ---

const enc = new TextEncoder();
const streams = new Set<ReadableStreamDefaultController<Uint8Array>>();

function emit(event: string, data: unknown) {
  const chunk = enc.encode(`event: ${event}\ndata: ${JSON.stringify(data)}\n\n`);
  for (const c of streams) {
    try {
      c.enqueue(chunk);
    } catch {
      streams.delete(c);
    }
  }
}

function heads() {
  for (const w of worlds.values()) emit("head", { board: w.board.name, seq: w.board.head_seq });
}

function stream(signal?: AbortSignal | null): Response {
  let ctl: ReadableStreamDefaultController<Uint8Array>;
  let keepalive: ReturnType<typeof setInterval>;
  const body = new ReadableStream<Uint8Array>({
    start(c) {
      ctl = c;
      streams.add(c);
      keepalive = setInterval(() => c.enqueue(enc.encode(": keepalive\n\n")), 25_000);
      queueMicrotask(heads);
    },
    cancel() {
      clearInterval(keepalive);
      streams.delete(ctl);
    },
  });
  signal?.addEventListener("abort", () => {
    clearInterval(keepalive);
    streams.delete(ctl);
    try {
      ctl.error(new DOMException("aborted", "AbortError"));
    } catch {
      // already closed
    }
  });
  return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
}

// --- the routes ---

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
const notFound = (what: string) =>
  json({ error: { code: "not_found", message: `The UI lab doesn't fake ${what} yet.`, hint: "Add it to web/lab/fake-api.ts." } }, 404);

const session: Session = {
  id: "bs_lab",
  key: { id: "key_lab", name: "UI lab" },
  started_with: "access_key",
  created_at: iso(at(0)),
  expires_at: iso(at(60 * 24 * 30)),
  person: { id: personId(me), handle: me, display_name: null, server_role: "admin" },
  csrf_token: "lab",
};

function page(list: Msg[], w: World, q: URLSearchParams) {
  const limit = Number(q.get("limit") ?? 50);
  const before = q.get("before");
  const after = q.get("after");
  let l = list;
  if (before) l = l.filter((m) => m.seq < Number(before));
  if (q.get("newest") === "true") {
    const out = l.slice(-limit);
    return { messages: out.map((m) => message(w, m)), next_after: null, prev_before: l.length > out.length ? out[0].seq : null };
  }
  if (after) l = l.filter((m) => m.seq > Number(after));
  const out = l.slice(0, limit);
  return { messages: out.map((m) => message(w, m)), next_after: l.length > out.length ? out.at(-1)!.seq : null, prev_before: null };
}

async function route(method: string, path: string, q: URLSearchParams, body: Record<string, unknown>): Promise<Response> {
  const parts = path.split("/").slice(2).map(decodeURIComponent); // after "/v1"
  const [a, b, c, d, e] = parts;
  if (a === "me" && b === "browser-session") return method === "DELETE" ? json({}) : json(session);
  if (a === "browser-sessions") return json(session);
  if (a === "me" && !b) return json({ id: personId(me), kind: "human", name: me, board: null, owner: null, browser: true });
  if (a === "info") return json({ mode: scenario.people.length > 1 ? "team" : "local" });
  if (a === "boards" && !b) return json({ boards: [...worlds.values()].map(boardView) });
  if (a === "boards" && b) {
    const w = worlds.get(b);
    if (!w) return json({ error: { code: "board_not_found", message: `There is no board named ${b}.`, hint: "Pick one from the board list." } }, 404);
    if (!c) return json(boardView(w));
    if (c === "members" && !d) return json({ members: w.members });
    if (c === "members" && e === "delivery") {
      const m = w.members.find((x) => x.name === d)!;
      m.delivery_mode = body.mode as Member["delivery_mode"];
      m.delivery = m.delivery_mode;
      m.delivery_revision = (m.delivery_revision ?? 0) + 1;
      return json({ board: b, agent: d, mode: m.delivery_mode, revision: m.delivery_revision, changed: true });
    }
    if (c === "events") {
      const all = await hashed(w);
      const after = Number(q.get("after") ?? 0);
      const limit = Number(q.get("limit") ?? 200);
      const rest = all.filter((x) => x.seq > after);
      const out = rest.slice(0, limit);
      return json({ events: out, head_seq: w.board.head_seq, next_after: rest.length > out.length ? out.at(-1)!.seq : null });
    }
    if (c === "messages" && !d && method === "GET") {
      let list = w.messages;
      const from = q.get("from");
      const role = q.get("role");
      if (from) list = list.filter((m) => m.from === from);
      if (role) list = list.filter((m) => w.members.find((x) => x.name === m.from)?.role === role);
      if (q.get("to_me") === "true") list = list.filter(toMe);
      return json(page(list, w, q));
    }
    if (c === "messages" && !d && method === "POST") {
      const id = `msg_lab_${crypto.randomUUID().slice(0, 8)}`;
      addMessage(w, {
        id,
        atMs: Date.now(),
        from: me,
        to: (body.to as string[]) ?? ["all"],
        body: String(body.body ?? ""),
        replyTo: (body.reply_to as string) ?? null,
        asks: false,
        urgent: false,
      });
      w.readUpTo = w.board.head_seq;
      setTimeout(() => emit("head", { board: b, seq: w.board.head_seq }));
      return json(message(w, w.messages.at(-1)!));
    }
    if (c === "messages" && e === "receipts") {
      const m = w.messages.find((x) => x.seq === Number(d));
      const named = (m?.to ?? []).filter((t) => t.startsWith("@")).map((t) => t.slice(1));
      const receipts: Receipts = {
        board: b,
        seq: Number(d),
        message_id: m?.id ?? "",
        to: m?.to ?? [],
        to_everyone: m?.to.includes("all") ?? false,
        available: true,
        recipients: named.map((n) => ({ member: ref(w, n), state: "read", presence: w.members.find((x) => x.name === n)?.presence ?? null })),
      };
      return json(receipts);
    }
    if (c === "ack") {
      w.readUpTo = Math.max(w.readUpTo, Number(body.up_to ?? 0));
      const v = boardView(w);
      setTimeout(() => emit("unread", { board: b, board_id: w.board.id, read_up_to: v.read_up_to, unread: v.unread }));
      return json({ board: b, read_up_to: v.read_up_to, unread: v.unread });
    }
    if (c === "join-codes") {
      return json({ join_line: `aboard join abj_lab_${w.board.name}_example`, role: String(body.role ?? "member"), expires_at: iso(Date.now() + 3_600_000) });
    }
    if (["archive", "restore", "delete"].includes(c)) {
      w.board.lifecycle = c === "restore" ? "active" : "archived";
      setTimeout(heads);
      return json({ id: w.board.id, lifecycle: c === "delete" ? "deleted" : w.board.lifecycle, changed: true });
    }
  }
  if (a === "messages" && b) {
    for (const w of worlds.values()) {
      const m = w.messages.find((x) => x.id === b);
      if (!m) continue;
      if (c === "replies") {
        const after = Number(q.get("after") ?? 0);
        const replies = w.messages.filter((x) => rootOf(w, x)?.id === m.id && x.seq > after).map((x) => message(w, x));
        return json({ message_id: m.id, root: message(w, m), replies, next_after: null });
      }
      if (c === "reactions" && d) {
        const name = d as ReactionName;
        const by = (m.reactions.get(name) ?? []).filter((x) => x !== me);
        if (method === "PUT") by.push(me);
        m.reactions.set(name, by);
        return json(message(w, m));
      }
    }
  }
  return notFound(`${method} ${path}`);
}

/**
 * install puts the fake API under window.fetch: requests to /v1/ are answered here, and
 * everything else (the page's own files) goes to the real fetch.
 */
export function install() {
  // The person has read everything before the step on screen; what that step brought is new.
  const { step, snap } = current();
  if (step > 0) sync(snapshot(scenario, step - 1));
  main.readUpTo = main.messages.at(-1)?.seq ?? 0;
  sync(snap);
  const real = window.fetch.bind(window);
  window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
    const href = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
    const url = new URL(href, window.location.href);
    if (!url.pathname.startsWith("/v1/")) return real(input, init);
    const method = (init?.method ?? "GET").toUpperCase();
    if (url.pathname === "/v1/stream") return stream(init?.signal);
    let body: Record<string, unknown> = {};
    if (typeof init?.body === "string") body = JSON.parse(init.body) as Record<string, unknown>;
    // A short wait, so loading states show for a moment as they would over a network.
    await new Promise((done) => setTimeout(done, 20));
    return route(method, url.pathname, url.searchParams, body);
  };
  onStep((snap) => {
    const changed = sync(snap);
    heads();
    for (const name of changed) {
      const m = main.members.find((x) => x.name === name)!;
      emit("presence", { board: main.board.name, agent: name, presence: m.presence, presence_since: m.presence_since });
    }
  });
}
