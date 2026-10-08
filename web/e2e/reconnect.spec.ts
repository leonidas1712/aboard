import { createServer, type Server } from "node:http";
import { expect, test } from "@playwright/test";
import { follow, reconnectDelay } from "../app/api";

test("stream reconnect delays spread clients without exceeding the backoff", () => {
  for (const base of [1000, 2000, 30_000]) {
    expect(reconnectDelay(base, 0)).toBe(base / 2);
    expect(reconnectDelay(base, 1)).toBe(base);
    expect(reconnectDelay(base, 0.5)).toBeGreaterThan(base / 2);
    expect(reconnectDelay(base, 0.5)).toBeLessThan(base);
  }
});

async function listen(server: Server): Promise<string> {
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("missing test listener");
  return `http://127.0.0.1:${address.port}`;
}

test("a reset stream reconnects and delivers a head", async () => {
  let requests = 0;
  const server = createServer((req, res) => {
    if (++requests === 1) {
      req.socket.destroy();
      return;
    }
    res.writeHead(200, { "Content-Type": "text/event-stream" });
    res.write('event: head\ndata: {"board":"test-board","seq":3}\n\n');
  });
  const address = await listen(server);
  const original = globalThis.fetch;
  globalThis.fetch = (input, init) => original(new URL(String(input), address), init);
  let stop: (() => void) | undefined;
  try {
    await new Promise<void>((done, fail) => {
      stop = follow({ head: (board, seq) => {
        expect(board).toBe("test-board");
        expect(seq).toBe(3);
        done();
      }, error: fail });
    });
    expect(requests).toBe(2);
  } finally {
    stop?.();
    globalThis.fetch = original;
    server.closeAllConnections();
    await new Promise<void>((done) => server.close(() => done()));
  }
});

test("a refused stream stops without reconnecting", async () => {
  let requests = 0;
  const server = createServer((_req, res) => {
    requests++;
    res.writeHead(403, { "Content-Type": "application/json" });
    res.end('{"error":{"code":"login_required","message":"refused","hint":"sign in"}}');
  });
  const address = await listen(server);
  const original = globalThis.fetch;
  globalThis.fetch = (input, init) => original(new URL(String(input), address), init);
  let stop: (() => void) | undefined;
  try {
    await new Promise<void>((done, fail) => {
      stop = follow({ head: () => fail(new Error("refused stream exposed a head")), error: (err) => {
        expect(err.code).toBe("login_required");
        done();
      } });
    });
    expect(requests).toBe(1);
  } finally {
    stop?.();
    globalThis.fetch = original;
    server.closeAllConnections();
    await new Promise<void>((done) => server.close(() => done()));
  }
});
