// The few parts of Aboard's public API the UI reads (spec/openapi.yaml). The page logs
// in with the one-time code `aboard open` puts in the address's fragment, keeps the
// browser token it gets for it, which acts as that person, and sends it with every request. No
// cookie is involved.

export type Board = {
  name: string;
  charter: string;
  roles: Record<string, unknown>;
  policy: { preset: "starter" | "recommended" };
  head_seq: number;
};

export type MemberRef = { name: string; kind: "agent" | "human"; role: string | null; owner: string | null };

export type Member = MemberRef & { harness: string | null; status: string };

export type Message = {
  id: string;
  seq: number;
  at: string;
  from: MemberRef;
  to: string[];
  body: string;
  reply_to_seq: number | null;
  urgent: boolean;
};

export type MessagePage = { messages: Message[]; next_after: number | null; prev_before: number | null };

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

// The server sends a keepalive comment every 25 seconds; a stream silent for longer
// than this is taken as dead and reopened.
const silentLimit = 60_000;

/**
 * followHeads calls onHead each time a board's head moves, from the server's event
 * stream, read with fetch so it can send the token. It reconnects when the stream ends
 * or goes silent; the server then sends every head again, so nothing is missed. A
 * rejected token goes to onError and ends following. It returns a function that stops.
 */
export function followHeads(onHead: (board: string, seq: number) => void, onError: (e: ApiError) => void): () => void {
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
          onError(await failure(resp));
          return;
        }
        if (resp.ok && resp.body) {
          wait = 500; // connected: the next retry starts short again
          await readEvents(resp.body, () => {
            clearTimeout(silence);
            silence = setTimeout(stop, silentLimit);
          }, (event, data) => {
            if (event !== "head") return;
            const head = JSON.parse(data) as { board: string; seq: number };
            onHead(head.board, head.seq);
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
