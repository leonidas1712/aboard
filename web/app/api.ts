// The few parts of Aboard's public API the UI reads (spec/openapi.yaml). The browser
// sends its login cookie with every request; the server accepts it only for reads.

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

export async function get<T>(path: string, query: Record<string, string | number | boolean | undefined> = {}): Promise<T> {
  const params = new URLSearchParams();
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== "" && v !== false) params.set(k, String(v));
  }
  const qs = params.toString();
  const resp = await fetch(qs ? `${path}?${qs}` : path, { credentials: "same-origin" });
  if (resp.ok) return (await resp.json()) as T;
  const body = await resp.json().catch(() => null);
  const e = body?.error ?? {};
  throw new ApiError(resp.status, e.code ?? "internal", e.message ?? `The server answered ${resp.status}.`, e.hint ?? "");
}

/**
 * followHeads calls onHead each time a board's head moves, from the server's event
 * stream. The browser reconnects on its own; the stream then sends every head again,
 * so nothing is missed. It returns a function that closes the stream.
 */
export function followHeads(onHead: (board: string, seq: number) => void): () => void {
  const stream = new EventSource("/v1/stream");
  stream.addEventListener("head", (e) => {
    const head = JSON.parse((e as MessageEvent<string>).data) as { board: string; seq: number };
    onHead(head.board, head.seq);
  });
  return () => stream.close();
}
