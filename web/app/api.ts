// The parts of Aboard's public API the UI uses (spec/openapi.yaml). The page signs in
// with the one-time code `aboard open` puts in the address's fragment, or with an access
// key pasted on the login page. Either is exchanged for a browser session the server
// keeps in a cookie this page's scripts can't read; the page holds only the session's
// CSRF token, in memory, and sends it with every write. Nothing secret is stored.

import type { SettableMode } from "./delivery-modes.gen";

export type Policy = {
  preset: "starter" | "recommended";
  visibility: "open" | "addressed";
  broadcast: "everyone" | "granted";
  urgent: "everyone" | "granted";
  show_harness?: boolean;
  overrides: string[];
};

export type Role = { charter?: string; can: (string | { claim_tasks: string[] })[] };

export type MemberRef = { name: string; kind: "agent" | "human"; role: string | null; owner: string | null; harness?: string | null };

export type Board = {
  id: string;
  name: string;
  /** title is free text people read beside the name; null when the board has none. */
  title: string | null;
  charter: string;
  roles: Record<string, Role>;
  policy: Policy;
  head_seq: number;
  /** message_count and last_message_at are null for a reader who may not see them. */
  message_count: number | null;
  last_message_at: string | null;
  created_at: string;
  created_by: MemberRef;
  /** visibility is who can see the board: everyone on the server (open), or only the people on it (private). */
  visibility: Visibility;
  /** on_board is false for an open board the person sees but hasn't joined. */
  on_board: boolean;
  /** read_up_to is how far the person has read the board; unread counts the messages after it they didn't send. Absent when they aren't on it. */
  read_up_to?: number;
  unread?: number;
  /** needs_reply counts questions to this person without their own direct reply. */
  needs_reply?: number | null;
  asks_to_me?: { blocking: number; going_with: number };
  /** lifecycle is archived for a read-only board; absent means active. */
  lifecycle?: Lifecycle;
  /** can_archive, can_restore and can_delete are what this person may do now; absent means no. */
  can_archive?: boolean;
  can_restore?: boolean;
  can_delete?: boolean;
  task_prefix?: string | null;
  tasks_open?: number;
  /** added is set while someone else's add of this person to the board is new to them: no agent of theirs has joined since and they haven't read past it. */
  added?: BoardAdded;
};

/** BoardAdded is the person.added event that put the reader on a board. */
export type BoardAdded = {
  seq: number;
  at: string;
  by: { kind: "agent" | "human" | "system"; member_id: string | null; name: string | null; owner: string | null };
};

/** addedBy says who added the reader: "leo", or "leo's agent claude". */
export function addedBy(a: BoardAdded): string {
  const name = a.by.name ?? "someone";
  return a.by.kind === "agent" && a.by.owner ? `${a.by.owner}'s agent ${name}` : name;
}

export type Lifecycle = "active" | "archived";

/** isArchived says a board is read-only until someone restores it. */
export function isArchived(b: Board | null | undefined): boolean {
  return b?.lifecycle === "archived";
}

/** LifecycleResult is the server's receipt of an archive, restore or delete: only the board's id and lifecycle. */
export type LifecycleResult = { id: string; lifecycle: Lifecycle | "deleted"; changed: boolean };

/** changeLifecycle archives, restores or deletes a board, named by its name. */
export function changeLifecycle(board: string, action: "archive" | "restore" | "delete"): Promise<LifecycleResult> {
  return post<LifecycleResult>(`/v1/boards/${encodeURIComponent(board)}/${action}`, {});
}

/** Receipt is whether a message has reached one of its recipients; presence is an agent's now, null for a person. */
export type Receipt = { member: MemberRef; state: "pending" | "received" | "read"; presence: Presence | null };

/** Receipts are a message's recipients, fixed when it was posted; a message to everyone has none. */
export type Receipts = { board: string; seq: number; message_id: string; to: string[]; to_everyone: boolean; available: boolean; recipients: Receipt[] };

/** UnreadEvent is the person's own read position and unread count on one of their boards. */
export type UnreadEvent = { board: string; board_id?: string; read_up_to: number; unread: number };

/** ReadEvent says one of the person's own agents acknowledged its messages up to read_up_to. */
export type ReadEvent = { board: string; agent: string; read_up_to: number };

export type Visibility = "open" | "private";

/** Me is who the browser's token acts as: always a person for a browser. */
export type Me = {
  id: string;
  kind: "human" | "agent";
  name: string;
  board: string | null;
  owner: string | null;
  browser: boolean;
  /** server_role is the person's role on the server; null for an agent. */
  server_role?: "admin" | "member" | "guest" | null;
};

export type Presence = "working" | "idle" | "waiting" | "no_session";

/** DeliveryMode is when an agent's session is woken; auto is all's earlier name. */
export type DeliveryMode = "focused" | "all" | "humans" | "off" | "auto";

export type Member = MemberRef & {
  id: string;
  harness: string | null;
  access: "admin" | "member" | null;
  /** server_role is a person's role on the server: a guest came in through a guest code. Null for agents. */
  server_role?: "admin" | "member" | "guest" | null;
  joined_at: string;
  presence: Presence | null;
  presence_since: string | null;
  /** delivery is the mode the agent's delivery daemon last reported applying; null for people and when never reported. */
  delivery?: DeliveryMode | null;
  /** owner_id is an agent's person's permanent id; null for people. */
  owner_id?: string | null;
  /** delivery_mode is the agent's delivery mode as its person set it, held by the server; null for people. */
  delivery_mode?: SettableMode | null;
  delivery_revision?: number | null;
  /** status is active for a member on the board now; removed or left for an agent whose seat ended. */
  status?: "active" | "removed" | "left";
  /** removed_at and removed_by are set for an agent whose seat ended. */
  removed_at?: string;
  removed_by?: RemovedBy;
  /** can_remove says this person may remove the agent now; absent means no. */
  can_remove?: boolean;
};

/** RemovedBy is who ended an agent's seat: its person, a board owner, a server admin, or itself. */
export type RemovedBy = "person" | "board_owner" | "admin" | "self";

/** removeAgent removes an agent from a board for good; its messages stay. */
export function removeAgent(board: string, agent: string): Promise<unknown> {
  return send("DELETE", `/v1/boards/${encodeURIComponent(board)}/members/${encodeURIComponent(agent)}`);
}

/** removedAgents lists the agents whose seats on a board ended. */
export async function removedAgents(board: string): Promise<Member[]> {
  const r = await get<{ members: Member[] }>(`/v1/boards/${encodeURIComponent(board)}/members`, { removed: true });
  return r.members.filter((m) => m.kind === "agent" && m.status !== undefined && m.status !== "active");
}

/** DeliverySetting is an agent's delivery mode after a person sets it. */
export type DeliverySetting = { board: string; agent: string; mode: SettableMode; revision: number; changed: boolean };

/** setDelivery sets the delivery mode of one of the person's own agents. */
export function setDelivery(board: string, agent: string, mode: SettableMode): Promise<DeliverySetting> {
  return put<DeliverySetting>(`/v1/boards/${encodeURIComponent(board)}/members/${encodeURIComponent(agent)}/delivery`, { mode });
}

export type Sender = "owner" | "owner_agent" | "other_person" | "other_agent" | "self";

export type Message = {
  board?: string;
  id: string;
  seq: number;
  at: string;
  from: MemberRef;
  to: string[];
  body: string;
  about?: TaskTag[];
  reply_to: string | null;
  reply_to_seq: number | null;
  /** thread_root is the first message of the reply's thread; null for a message that replies to nothing. */
  thread_root: string | null;
  thread_root_seq: number | null;
  /** reply_count and last_reply_at describe the thread a message starts, as the reader may see it. */
  reply_count: number;
  last_reply_at: string | null;
  urgent: boolean;
  expects_reply: boolean;
  sender: Sender;
  show_owner: boolean;
  /** reactions are the emoji on the message, in the set's order; empty when there are none. */
  reactions: Reaction[];
  /** mentions are the members the text mentions, as the server resolved them when it was posted. */
  mentions: Mention[];
  ask?: MessageAsk;
  answer?: { ask_id: string; ask_seq: number; option: number | null; option_text?: string | null; withdrawn: boolean };
};

export type MessageAsk = {
  to: MemberRef;
  options: string[];
  blocking: boolean;
  going_with: string | null;
  going_at: string | null;
  task: string | null;
  state: "open" | "answered" | "withdrawn" | "went_with";
  answer_seq: number | null;
  answer_option: number | null;
  can_answer?: boolean;
  can_withdraw?: boolean;
};
export type AskList = { asks: Message[]; more: boolean };

/**
 * Mention is one member a message mentions: `text` is how it was written ("@codex" or
 * "@role:reviewer"), and `wakes` whether it counts as addressing the agent.
 */
export type Mention = {
  id: string;
  kind: "agent" | "human";
  name: string;
  text: string;
  wakes: boolean;
  reason: "limit" | "cannot_read" | null;
};

export type ReactionName = "thumbsup" | "check" | "eyes" | "heart" | "tada" | "question";

/** Reaction is one emoji on a message: who reacted with it, earliest first, and whether you did. */
export type Reaction = { name: ReactionName; emoji: string; count: number; by: string[]; mine: boolean };

/** reactionSet is every reaction there is, in the order they are shown, with a word for each. */
export const reactionSet: { name: ReactionName; emoji: string; word: string }[] = [
  { name: "thumbsup", emoji: "👍", word: "thumbs up" },
  { name: "check", emoji: "✅", word: "done" },
  { name: "eyes", emoji: "👀", word: "looking" },
  { name: "heart", emoji: "❤️", word: "heart" },
  { name: "tada", emoji: "🎉", word: "celebrate" },
  { name: "question", emoji: "❓", word: "question" },
];

export type MessagePage = { messages: Message[]; next_after: number | null; prev_before: number | null };

/** ReplyPage is a thread: its first message (null when the reader may not see it) and replies, oldest first. */
export type ReplyPage = { message_id: string; root: Message | null; replies: Message[]; next_after: number | null };

export type Actor = { kind: "agent" | "human" | "system"; member_id: string | null; name: string | null; owner: string | null };

/** BoardEvent is one entry of a board's event log, with its hashes (spec/events.md). */
export type BoardEvent = {
  id: string;
  board_id: string;
  seq: number;
  type: string;
  at: string;
  actor: Actor;
  data?: unknown;
  data_withheld?: boolean;
  data_hash: string;
  prev_hash: string;
  hash: string;
};

export type EventPage = { events: BoardEvent[]; head_seq: number; next_after: number | null };

/** ApiError is an error response: {"error":{code,message,hint}}. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
    readonly hint: string,
  ) {
    super(message);
  }
}

/** Session is the browser session this page is signed in with: who, and with which key. */
export type Session = {
  id: string;
  key: { id: string; name: string };
  started_with: "login_code" | "access_key";
  created_at: string;
  expires_at: string;
  person: { id: string; handle: string; display_name: string | null; server_role: "admin" | "member" };
  csrf_token: string;
};

// legacyTokenKey is where pages from before browser sessions moved into cookies kept
// their browser token. The page copies such a token into the cookie (the same session,
// whose secret keeps working until it ends) and deletes it from storage, once.
const legacyTokenKey = "aboard.browserToken";

// The session this page is signed in with, kept in memory only: its CSRF token is read
// again from the server after a reload.
let current: Session | null = null;

/** session is the browser session this page is signed in with, or null. */
export function session(): Session | null {
  return current;
}

/** signedOutEvent fires on window when the server says this browser isn't signed in. */
export const signedOutEvent = "aboard:signed-out";

function writeHeaders(): Record<string, string> {
  return current ? { "X-Aboard-CSRF": current.csrf_token } : {};
}

async function failure(resp: Response): Promise<ApiError> {
  const body = await resp.json().catch(() => null);
  const e = body?.error ?? {};
  if (resp.status === 401 && current) {
    current = null;
    window.dispatchEvent(new Event(signedOutEvent));
  }
  return new ApiError(resp.status, e.code ?? "internal", e.message ?? `The server answered ${resp.status}.`, e.hint ?? "");
}

/**
 * secureEnough reports whether this page may send a secret to its server: over HTTPS,
 * or over plain HTTP only to this computer's own address (127.0.0.1, localhost), which
 * browsers count as secure. Anywhere else a key, code or token would cross the network
 * in the clear.
 */
export function secureEnough(): boolean {
  return typeof window === "undefined" || window.isSecureContext;
}

/** insecureError is what the page says instead of sending a secret over plain HTTP. */
export function insecureError(): ApiError {
  return new ApiError(
    0,
    "insecure_page",
    "Signing in with a key needs https.",
    "Use aboard open from a signed-in machine, or reach this server over https.",
  );
}

// startSession exchanges what signs a browser in for a session cookie. It sends nothing
// from a page that isn't secure enough.
async function startSession(
  body: { code: string; confirm_switch?: boolean } | { key: string } | { token: string },
): Promise<Session> {
  if (!secureEnough()) throw insecureError();
  const resp = await fetch("/v1/browser-sessions", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });
  if (!resp.ok) {
    const body = await resp.json().catch(() => null);
    const e = body?.error ?? {};
    throw new ApiError(resp.status, e.code ?? "internal", e.message ?? `The server answered ${resp.status}.`, e.hint ?? "");
  }
  current = (await resp.json()) as Session;
  return current;
}

// takeLegacyToken returns a browser token an older page kept in this browser's storage,
// and deletes it, so it never stays there.
function takeLegacyToken(): string | null {
  try {
    const t = localStorage.getItem(legacyTokenKey);
    localStorage.removeItem(legacyTokenKey);
    return t;
  } catch {
    return null;
  }
}

/** CodePreview is who a login code would sign this browser in as. */
export type CodePreview = { person: Session["person"]; key: { id: string; name: string }; expires_at: string };

/** Pending is a login code from the address that waits for the person to confirm it. */
export type Pending = { code: string; preview: CodePreview };

/**
 * Started is how the page starts: the board to show, the session (null when signed out),
 * a login code from the address that the person must confirm first, and a note to show
 * when a link from the address didn't work.
 */
export type Started = { board: string | null; session: Session | null; pending?: Pending; note?: string };

async function previewCode(code: string): Promise<CodePreview> {
  if (!secureEnough()) throw insecureError();
  const resp = await fetch("/v1/login-codes/preview", {
    method: "POST",
    credentials: "same-origin",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!resp.ok) {
    const body = await resp.json().catch(() => null);
    const e = body?.error ?? {};
    throw new ApiError(resp.status, e.code ?? "internal", e.message ?? `The server answered ${resp.status}.`, e.hint ?? "");
  }
  return (await resp.json()) as CodePreview;
}

// currentSession asks which session this browser has, moving a browser token an older
// page stored into a cookie first.
async function currentSession(): Promise<Session | null> {
  const legacy = takeLegacyToken();
  // Over plain HTTP the stored token is dropped unsent; the person signs in again.
  if (legacy && secureEnough()) {
    try {
      return await startSession({ token: legacy });
    } catch {
      // The stored login ended; the page signs in afresh.
    }
  }
  const resp = await fetch("/v1/me/browser-session", { credentials: "same-origin" });
  if (resp.status === 401) return null;
  if (!resp.ok) throw await failure(resp);
  current = (await resp.json()) as Session;
  return current;
}

/**
 * start finds the page's session. A one-time code in the address's fragment
 * (/#code=…&board=NAME) is removed from the address bar at once. Since anyone can send a
 * link, the code never signs the browser in by itself: start asks the server who the
 * code is for, and only when the browser is already signed in as that same person does it
 * use the code straight away. Otherwise it returns the code as pending, for the person to
 * confirm or cancel. A code that doesn't work throws.
 */
export async function start(): Promise<Started> {
  const frag = new URLSearchParams(window.location.hash.slice(1));
  const code = frag.get("code");
  let board = new URLSearchParams(window.location.search).get("board");
  if (code) {
    board = frag.get("board");
    history.replaceState(null, "", board ? `/?board=${encodeURIComponent(board)}` : "/");
  }
  const session = await currentSession();
  if (!code) return { board, session };
  // Over plain HTTP the code isn't sent; the login page says why.
  if (!secureEnough()) return { board: null, session };
  let preview: CodePreview;
  try {
    preview = await previewCode(code);
  } catch (e) {
    // A link that no longer works leaves the browser as it was, with a note.
    if (e instanceof ApiError && e.code === "login_code_invalid") {
      history.replaceState(null, "", "/");
      return { board: null, session, note: "That login link doesn't work: it is wrong, expired or already used." };
    }
    throw e;
  }
  // Only the permanent id of the session the server just confirmed counts as the same
  // person; then the link refreshes the session without asking.
  if (session && session.person.id === preview.person.id) {
    return { board, session: await startSession({ code }) };
  }
  return { board, session, pending: { code, preview } };
}

/**
 * confirmCode signs the browser in with a login code its person confirmed, switching it
 * from another person's session if it had one.
 */
export function confirmCode(p: Pending): Promise<Session> {
  return startSession({ code: p.code, confirm_switch: true });
}

/** signInWithKey signs the browser in with an access key, which the page never keeps. */
export function signInWithKey(key: string): Promise<Session> {
  return startSession({ key });
}

/** signOut ends this browser's session, and only this one. */
export async function signOut(): Promise<void> {
  const resp = await fetch("/v1/me/browser-session", { method: "DELETE", credentials: "same-origin", headers: writeHeaders() });
  if (!resp.ok && resp.status !== 401) throw await failure(resp);
  current = null;
}

export async function get<T>(path: string, query: Record<string, string | number | boolean | undefined> = {}): Promise<T> {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== "" && v !== false) params.set(k, String(v));
  }
  const qs = params.toString();
  const resp = await fetch(qs ? `${path}?${qs}` : path, { credentials: "same-origin" });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

/**
 * post sends a write as the person, with an Idempotency-Key so a retried request
 * never posts twice.
 */
export async function post<T>(path: string, body: unknown, key: string = crypto.randomUUID()): Promise<T> {
  const resp = await fetch(path, {
    method: "POST",
    credentials: "same-origin",
    headers: { ...writeHeaders(), "Content-Type": "application/json", "Idempotency-Key": key },
    body: JSON.stringify(body),
  });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

/** put replaces something as the person, with an Idempotency-Key. */
async function put<T>(path: string, body: unknown, key: string = crypto.randomUUID()): Promise<T> {
  const resp = await fetch(path, {
    method: "PUT",
    credentials: "same-origin",
    headers: { ...writeHeaders(), "Content-Type": "application/json", "Idempotency-Key": key },
    body: JSON.stringify(body),
  });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

/** send makes a write without a body, such as PUT or DELETE, with an Idempotency-Key. */
export async function send<T>(method: "PUT" | "DELETE", path: string, key: string = crypto.randomUUID()): Promise<T> {
  const resp = await fetch(path, { method, credentials: "same-origin", headers: { ...writeHeaders(), "Idempotency-Key": key } });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

/**
 * ackBoard moves the person's read position on a board forward to upTo, for what the
 * page showed them. It never moves back.
 */
export function ackBoard(board: string, upTo: number): Promise<{ board: string; read_up_to: number; unread: number }> {
  return post(`/v1/boards/${encodeURIComponent(board)}/ack`, { up_to: upTo });
}

/** react adds the person's reaction to a message, or takes it back, and returns the message. */
export function react(message: string, name: ReactionName, add: boolean): Promise<Message> {
  return send<Message>(add ? "PUT" : "DELETE", `/v1/messages/${encodeURIComponent(message)}/reactions/${name}`);
}

// The server sends a keepalive comment every 25 seconds; a stream silent for longer
// than this is taken as dead and reopened.
const silentLimit = 60_000;

export type PresenceEvent = {
  board: string;
  agent: string;
  presence: Presence;
  presence_since: string | null;
  /** delivery is the mode the agent's delivery daemon reports applying. */
  delivery?: DeliveryMode | null;
};

export type StreamHandlers = {
  /** head runs each time a board's head moves, and for every board when the stream (re)opens. */
  head: (board: string, seq: number) => void;
  /** presence runs each time an agent's presence changes. */
  presence?: (p: PresenceEvent) => void;
  /** unread runs with the person's read position and unread count on a board, for each board when the stream opens and on every change. */
  unread?: (u: UnreadEvent) => void;
  /** read runs when one of the person's own agents acknowledges its messages. */
  read?: (r: ReadEvent) => void;
  /**
   * unavailable runs when a board this stream showed may no longer be open to the person,
   * with only its id. It is a hint to read the board again, never proof it is gone.
   */
  unavailable?: (boardId: string) => void;
  /** open runs each time the stream connects, so a reader can reread what it may have missed. */
  open?: () => void;
  /** error gets a refused session; following then ends. */
  error: (e: ApiError) => void;
};

/**
 * follow reads the server's event stream with fetch, with the session cookie. It
 * reconnects when the stream ends or goes silent; the server then sends every head
 * again, so nothing is missed. It returns a function that stops.
 */
export function follow(on: StreamHandlers): () => void {
  const stopped = new AbortController();
  (async () => {
    for (let wait = 1000; !stopped.signal.aborted; wait = Math.min(wait * 2, 30_000)) {
      const conn = new AbortController();
      const stop = () => conn.abort();
      stopped.signal.addEventListener("abort", stop);
      let silence = setTimeout(stop, silentLimit);
      try {
        const resp = await fetch("/v1/stream", { credentials: "same-origin", signal: conn.signal });
        if (resp.status === 401 || resp.status === 403) {
          on.error(await failure(resp));
          return;
        }
        if (resp.ok && resp.body) {
          wait = 500; // connected: the next retry starts short again
          on.open?.();
          await readEvents(resp.body, () => {
            clearTimeout(silence);
            silence = setTimeout(stop, silentLimit);
          }, (event, data) => {
            if (event === "head") {
              const head = JSON.parse(data) as { board: string; seq: number };
              on.head(head.board, head.seq);
            } else if (event === "presence") {
              on.presence?.(JSON.parse(data) as PresenceEvent);
            } else if (event === "unread") {
              on.unread?.(JSON.parse(data) as UnreadEvent);
            } else if (event === "read") {
              on.read?.(JSON.parse(data) as ReadEvent);
            } else if (event === "board_unavailable") {
              const hint = JSON.parse(data) as { board_id?: string };
              if (hint.board_id) on.unavailable?.(hint.board_id);
            }
          });
        }
      } catch {
        // The connection failed or was cut; retry below.
      } finally {
        clearTimeout(silence);
        stopped.signal.removeEventListener("abort", stop);
      }
      if (stopped.signal.aborted) return;
      await new Promise((done) => setTimeout(done, wait));
    }
  })();
  return () => stopped.abort();
}

// readEvents parses a server-sent event stream until it ends: "event:" and "data:"
// lines build an event, a blank line dispatches it, and lines starting with ":" are
// comments (keepalives). onChunk runs for every chunk read.
async function readEvents(body: ReadableStream<Uint8Array>, onChunk: () => void, onEvent: (event: string, data: string) => void) {
  const reader = body.getReader();
  const decoder = new TextDecoder();
  let rest = "";
  let event = "";
  let data: string[] = [];
  for (;;) {
    const { done, value } = await reader.read();
    if (done) return;
    onChunk();
    const lines = (rest + decoder.decode(value, { stream: true })).split(/\r\n|\r|\n/);
    rest = lines.pop() ?? "";
    for (const line of lines) {
      if (line === "") {
        if (data.length > 0) onEvent(event || "message", data.join("\n"));
        event = "";
        data = [];
      } else if (!line.startsWith(":")) {
        const i = line.indexOf(":");
        const field = i < 0 ? line : line.slice(0, i);
        const val = i < 0 ? "" : line.slice(i + 1).replace(/^ /, "");
        if (field === "event") event = val;
        else if (field === "data") data.push(val);
      }
    }
  }
}

export type TaskTag = { id: string; ref: string; how: "given" | "thread" | "current" | "named" };
export type TaskText = { text: string; by: MemberRef; at: string; version: number; messages_since?: number };
export type Task = {
  id: string; ref: string; number: number; board: string; title: string;
  about: TaskText | null; stands: TaskText | null;
  state: "open" | "in_progress" | "done" | "cancelled";
  owner: MemberRef | null; with: MemberRef[];
  blocked: boolean; blocked_count: number;
  blocked_on: { ask_id: string; ask_seq: number; to: MemberRef; since: string }[];
  opened_by: MemberRef; opened_at: string; updated_at: string;
  closed_at?: string | null; closed_note?: string | null;
  message_count: number; thread_count: number;
};
export type TaskList = { board: string; tasks: Task[]; counts: Record<Task["state"] | "blocked", number>; more: boolean };

export function listTasks(board: string): Promise<TaskList> {
  return get(`/v1/boards/${encodeURIComponent(board)}/tasks`, { state: "all", limit: 200 });
}
export function getTask(board: string, task: string): Promise<Task> {
  return get(`/v1/boards/${encodeURIComponent(board)}/tasks/${encodeURIComponent(task)}`);
}
