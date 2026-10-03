// The parts of Aboard's public API the UI uses (spec/openapi.yaml). The page logs in
// with the one-time code `aboard open` puts in the address's fragment, keeps the browser
// token it gets for it, which acts as that person, and sends it in a header with every
// request. No cookie is involved.

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
};

/** Me is who the browser's token acts as: always a person for a browser. */
export type Me = { id: string; kind: "human" | "agent"; name: string; board: string | null; owner: string | null; browser: boolean };

export type Presence = "working" | "idle" | "waiting" | "no_session";

export type Member = MemberRef & {
  id: string;
  harness: string | null;
  access: "admin" | "member" | null;
  joined_at: string;
  presence: Presence | null;
  presence_since: string | null;
};

export type Sender = "owner" | "owner_agent" | "other_person" | "other_agent" | "self";

export type Message = {
  id: string;
  seq: number;
  at: string;
  from: MemberRef;
  to: string[];
  body: string;
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
};

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

const tokenKey = "aboard.browserToken";

// The token lives in localStorage, which browsers keep per origin, port included. Some
// browsers refuse storage (private windows, blocked site data); the page then works
// until it is reloaded.
let memoryToken: string | null = null;

function token(): string | null {
  try {
    return localStorage.getItem(tokenKey) ?? memoryToken;
  } catch {
    return memoryToken;
  }
}

function keepToken(t: string | null) {
  memoryToken = t;
  try {
    if (t === null) localStorage.removeItem(tokenKey);
    else localStorage.setItem(tokenKey, t);
  } catch {
    // Kept in memory only.
  }
}

function headers(): HeadersInit {
  const t = token();
  return t ? { Authorization: `Bearer ${t}` } : {};
}

async function failure(resp: Response): Promise<ApiError> {
  const body = await resp.json().catch(() => null);
  const e = body?.error ?? {};
  if (resp.status === 401) keepToken(null);
  return new ApiError(resp.status, e.code ?? "internal", e.message ?? `The server answered ${resp.status}.`, e.hint ?? "");
}

/**
 * login reads the one-time code from the address's fragment (/#code=…&board=NAME),
 * removes it from the address bar, and exchanges it for a browser token. It returns
 * the board the fragment named, if any. Without a code it does nothing.
 */
export async function login(): Promise<string | null> {
  const frag = new URLSearchParams(window.location.hash.slice(1));
  const code = frag.get("code");
  if (!code) return null;
  const board = frag.get("board");
  history.replaceState(null, "", board ? `/?board=${encodeURIComponent(board)}` : "/");
  const resp = await fetch("/v1/browser-tokens", {
    method: "POST",
    credentials: "omit",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ code }),
  });
  if (!resp.ok) throw await failure(resp);
  keepToken(((await resp.json()) as { token: string }).token);
  return board;
}

export async function get<T>(path: string, query: Record<string, string | number | boolean | undefined> = {}): Promise<T> {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== "" && v !== false) params.set(k, String(v));
  }
  const qs = params.toString();
  const resp = await fetch(qs ? `${path}?${qs}` : path, { credentials: "omit", headers: headers() });
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
    credentials: "omit",
    headers: { ...headers(), "Content-Type": "application/json", "Idempotency-Key": key },
    body: JSON.stringify(body),
  });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

// The server sends a keepalive comment every 25 seconds; a stream silent for longer
// than this is taken as dead and reopened.
const silentLimit = 60_000;

export type PresenceEvent = { board: string; agent: string; presence: Presence; presence_since: string | null };

export type StreamHandlers = {
  /** head runs each time a board's head moves, and for every board when the stream (re)opens. */
  head: (board: string, seq: number) => void;
  /** presence runs each time an agent's presence changes. */
  presence?: (p: PresenceEvent) => void;
  /** open runs each time the stream connects, so a reader can reread what it may have missed. */
  open?: () => void;
  /** error gets a rejected token; following then ends. */
  error: (e: ApiError) => void;
};

/**
 * follow reads the server's event stream with fetch, so it can send the token. It
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
        const resp = await fetch("/v1/stream", { credentials: "omit", headers: headers(), signal: conn.signal });
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
