// The parts of Aboard's public API the UI uses (spec/openapi.yaml). The page signs in
// with the one-time code `aboard open` puts in the address's fragment, or with an access
// key pasted on the login page. Either is exchanged for a browser session the server
// keeps in a cookie this page's scripts can't read; the page holds only the session's
// CSRF token, in memory, and sends it with every write. Nothing secret is stored.

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

/** DeliveryMode is when an agent's session is woken; auto is all's earlier name. */
export type DeliveryMode = "focused" | "all" | "humans" | "off" | "auto";

export type Member = MemberRef & {
  id: string;
  harness: string | null;
  access: "admin" | "member" | null;
  joined_at: string;
  presence: Presence | null;
  presence_since: string | null;
  /** delivery is the agent's delivery mode as its owner's daemon last reported it; null for people and when never reported. */
  delivery?: DeliveryMode | null;
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
  /** reactions are the emoji on the message, in the set's order; empty when there are none. */
  reactions: Reaction[];
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

/** send makes a write without a body, such as PUT or DELETE, with an Idempotency-Key. */
export async function send<T>(method: "PUT" | "DELETE", path: string, key: string = crypto.randomUUID()): Promise<T> {
  const resp = await fetch(path, { method, credentials: "same-origin", headers: { ...writeHeaders(), "Idempotency-Key": key } });
  if (resp.ok) return (await resp.json()) as T;
  throw await failure(resp);
}

/** react adds the person's reaction to a message, or takes it back, and returns the message. */
export function react(message: string, name: ReactionName, add: boolean): Promise<Message> {
  return send<Message>(add ? "PUT" : "DELETE", `/v1/messages/${encodeURIComponent(message)}/reactions/${name}`);
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
