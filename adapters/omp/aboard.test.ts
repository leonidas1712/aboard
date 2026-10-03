// Tests of Aboard's omp extension against a stand-in for omp's ExtensionAPI and a fake
// delivery daemon that speaks spec/control.md on a real Unix socket. Run with
// `make extension-test`, or `bun test adapters/omp`. They start no omp and no aboard: a
// stand-in aboard on the PATH records how the extension starts the daemon.
import { afterAll, afterEach, beforeAll, describe, expect, test } from "bun:test";
import * as fs from "node:fs";
import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";

// A short folder, so the daemon's socket path stays under the length a socket may have.
const home = fs.mkdtempSync("/tmp/abx-");
const state = path.join(home, "state");
const sockPath = path.join(state, "daemon.sock");
const bin = path.join(home, "bin");
const daemonStarts = path.join(home, "daemon-start.log");
process.env.ABOARD_HOME = home;
process.env.PATH = `${bin}:${process.env.PATH}`;
delete process.env.ABOARD_SESSION;
delete process.env.ABOARD_SUBAGENT;
fs.mkdirSync(bin, { recursive: true });
fs.writeFileSync(path.join(bin, "aboard"), `#!/bin/sh\necho "$* ABOARD_HOME=$ABOARD_HOME" >> ${daemonStarts}\n`, { mode: 0o755 });

const { default: aboard, RUNS_ABOARD, socketPath, stateDir } = await import("./aboard.ts");

/** A connection to the fake daemon, with every message the extension sent on it. */
interface Conn {
	sock: net.Socket;
	frames: Record<string, unknown>[];
	send(msg: Record<string, unknown>): void;
}

/** The fake delivery daemon: it records connections and lets each test answer. */
class Daemon {
	server: net.Server | undefined;
	conns: Conn[] = [];
	/** Answers a hello; by default, welcome. */
	onHello: (c: Conn, hello: Record<string, unknown>) => void = c => c.send({ v: 1, event: "welcome", boot: "b" });
	/** Answers a one-shot request such as boundary. */
	onRequest: (c: Conn, req: Record<string, unknown>) => void = c => c.send({ v: 1 });

	start(): Promise<void> {
		fs.mkdirSync(state, { recursive: true, mode: 0o700 });
		fs.rmSync(sockPath, { force: true });
		this.server = net.createServer(sock => {
			const c: Conn = { sock, frames: [], send: msg => sock.write(`${JSON.stringify(msg)}\n`) };
			this.conns.push(c);
			let buf = "";
			sock.setEncoding("utf8");
			sock.on("error", () => {});
			sock.on("data", (chunk: string) => {
				buf += chunk;
				for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
					const frame = JSON.parse(buf.slice(0, i));
					buf = buf.slice(i + 1);
					c.frames.push(frame);
					if (frame.op === "hello") this.onHello(c, frame);
					else if (c.frames.length === 1) this.onRequest(c, frame);
				}
			});
		});
		return new Promise(resolve => this.server!.listen(sockPath, resolve));
	}

	stop(): Promise<void> {
		for (const c of this.conns) c.sock.destroy();
		this.conns = [];
		return new Promise(resolve => (this.server ? this.server.close(() => resolve()) : resolve()));
	}

	hellos(): Conn[] {
		return this.conns.filter(c => c.frames[0]?.op === "hello");
	}
}

/** Waits until cond holds, for up to 5 seconds. */
async function until(what: string, cond: () => boolean): Promise<void> {
	const deadline = Date.now() + 5000;
	while (!cond()) {
		if (Date.now() > deadline) throw new Error(`timed out waiting for ${what}`);
		await Bun.sleep(5);
	}
}

/** A stand-in for omp: the ExtensionAPI and a context for each handler call. */
function fakeOmp() {
	const handlers = new Map<string, ((event: unknown, ctx: unknown) => unknown)[]>();
	const sent: { message: Record<string, unknown>; options: Record<string, unknown> | undefined }[] = [];
	const notes: string[] = [];
	const timers: { fn: () => void; ms: number }[] = [];
	const pi = {
		on(event: string, h: (event: unknown, ctx: unknown) => unknown) {
			handlers.set(event, [...(handlers.get(event) ?? []), h]);
		},
		sendMessage(message: Record<string, unknown>, options?: Record<string, unknown>) {
			sent.push({ message, options });
		},
		pi: { VERSION: "18.5.1" },
	};
	let idle = true;
	const ctx = (id: string, kind: "main" | "sub" = "main", entries: { type: string }[] = []) => ({
		cwd: "/work/project",
		sessionManager: { getSessionId: () => id, getEntries: () => entries },
		agent: kind === "main" ? { kind, id: "Main", name: "main", depth: 0 } : { kind, id: "0-Explore", name: "explore", depth: 1 },
		isIdle: () => idle,
		ui: { notify: (text: string) => notes.push(text) },
		setTimeout: (fn: () => void, ms: number) => {
			timers.push({ fn, ms });
			return timers.length;
		},
		clearTimer: () => {},
	});
	const emit = async (event: string, payload: unknown, c: unknown) => {
		let result: unknown;
		for (const h of handlers.get(event) ?? []) result = await h(payload, c);
		return result;
	};
	/** Runs the timers scheduled so far, as their time comes. */
	const runTimers = () => {
		for (const t of timers.splice(0)) t.fn();
	};
	return { pi, sent, notes, timers, ctx, emit, runTimers, setIdle: (v: boolean) => (idle = v) };
}

const daemon = new Daemon();
beforeAll(() => daemon.start());
afterEach(async () => {
	await daemon.stop();
	await daemon.start();
	daemon.onHello = c => c.send({ v: 1, event: "welcome", boot: "b" });
	daemon.onRequest = c => c.send({ v: 1 });
});
afterAll(async () => {
	await daemon.stop();
	fs.rmSync(home, { recursive: true, force: true });
});

const ID = "0199a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d";

/** Starts a main session and waits until the daemon has its hello. */
async function started(entries: { type: string }[] = []) {
	const omp = fakeOmp();
	aboard(omp.pi as never);
	const c = omp.ctx(ID, "main", entries);
	await omp.emit("session_start", { type: "session_start" }, c);
	await until("the hello", () => daemon.hellos().length === 1);
	const conn = daemon.hellos()[0];
	await until("the welcome", () => conn.sock.readyState === "open");
	return { omp, c, conn };
}

describe("identity and hello", () => {
	test("the main session's commands carry its id, and its hello says who it is", async () => {
		const { conn } = await started();
		expect(process.env.ABOARD_SESSION).toBe(`omp:${ID}`);
		const hello = conn.frames[0];
		expect(hello).toMatchObject({
			v: 1,
			op: "hello",
			harness: "omp",
			session: ID,
			source: "startup",
			process: { pid: process.pid },
			cwd: "/work/project",
			harness_version: "18.5.1",
			extension_version: "1",
		});
		expect(hello.boot).toMatch(/^[0-9a-f]{16}$/);
		expect(hello.resumed).toBeUndefined();
	});

	test("a session that already has messages connects as resumed", async () => {
		const { conn } = await started([{ type: "model_change" }, { type: "message" }]);
		expect(conn.frames[0].source).toBe("resume");
	});

	test("a subagent never connects and never changes the session's environment", async () => {
		process.env.ABOARD_SESSION = "omp:the-main-session";
		const omp = fakeOmp();
		aboard(omp.pi as never);
		await omp.emit("session_start", { type: "session_start" }, omp.ctx("0199-sub", "sub"));
		await omp.emit("agent_start", { type: "agent_start" }, omp.ctx("0199-sub", "sub"));
		// A main session connecting afterwards is the only connection the daemon sees.
		const main = fakeOmp();
		aboard(main.pi as never);
		await main.emit("session_start", { type: "session_start" }, main.ctx(ID));
		await until("the main session's hello", () => daemon.hellos().length === 1);
		expect(daemon.conns.length).toBe(1);
		expect(daemon.hellos()[0].frames[0].session).toBe(ID);
	});

	test("switching to another session says goodbye for the first and connects the second", async () => {
		const { omp, conn } = await started();
		await omp.emit("session_switch", { type: "session_switch", reason: "new" }, omp.ctx("0199-second"));
		await until("goodbye", () => conn.frames.some(f => f.op === "goodbye"));
		await until("the second hello", () => daemon.hellos().length === 2);
		expect(daemon.hellos()[1].frames[0].session).toBe("0199-second");
		expect(process.env.ABOARD_SESSION).toBe("omp:0199-second");
	});
});

describe("delivery", () => {
	test("a bundle for an idle session starts a turn, once, and is confirmed by id", async () => {
		const { omp, conn } = await started();
		conn.send({ v: 1, event: "deliver", id: 41, bundle: '<aboard-messages board="docs" count="1">hi</aboard-messages>' });
		await until("received", () => conn.frames.some(f => f.op === "received" && f.id === 41));
		expect(omp.sent).toHaveLength(1);
		expect(omp.sent[0].options).toEqual({ triggerTurn: true });
		expect(omp.sent[0].message).toMatchObject({ customType: "aboard", display: true, attribution: "agent" });
		expect(omp.sent[0].message.content).toContain("hi");
		// Sent again after a reconnect: confirmed again, never added twice.
		conn.send({ v: 1, event: "deliver", id: 41, bundle: "<aboard-messages>hi</aboard-messages>" });
		await until("received again", () => conn.frames.filter(f => f.op === "received").length === 2);
		expect(omp.sent).toHaveLength(1);
	});

	test("a bundle that arrives as a turn starts goes in as an aside for the owner, a follow-up for anyone else", async () => {
		const { omp, c, conn } = await started();
		omp.setIdle(false);
		await omp.emit("agent_start", { type: "agent_start" }, c);
		conn.send({ v: 1, event: "deliver", id: 42, bundle: '<aboard-message sender="owner">stop</aboard-message>' });
		conn.send({ v: 1, event: "deliver", id: 43, bundle: '<aboard-message sender="other_agent">later</aboard-message>' });
		await until("both received", () => conn.frames.filter(f => f.op === "received").length === 2);
		expect(omp.sent.map(s => s.options)).toEqual([{ deliverAs: "aside" }, { deliverAs: "followUp" }]);
	});

	test("turns report prompt and turn_end, except an end omp continues past", async () => {
		const { omp, c, conn } = await started();
		await omp.emit("agent_start", { type: "agent_start" }, c);
		await omp.emit("agent_end", { type: "agent_end", messages: [], willContinue: true }, c);
		await omp.emit("agent_end", { type: "agent_end", messages: [] }, c);
		await until("turn_end", () => conn.frames.some(f => f.op === "turn_end"));
		expect(conn.frames.slice(1).map(f => f.op)).toEqual(["prompt", "turn_end"]);
	});

	test("after a step that ran tools, the owner's messages and the notice go in as an aside", async () => {
		const { omp, c } = await started();
		let request: Record<string, unknown> | undefined;
		daemon.onRequest = (conn, req) => {
			request = req;
			conn.send({ v: 1, bundle: "Aboard: your owner sent this", notice: "<aboard-notice>1 waiting</aboard-notice>" });
		};
		await omp.emit("turn_end", { type: "turn_end", turnIndex: 0, message: {}, toolResults: [] }, c);
		expect(request).toBeUndefined();
		await omp.emit("turn_end", { type: "turn_end", turnIndex: 1, message: {}, toolResults: [{}] }, c);
		expect(request).toMatchObject({ v: 1, op: "boundary", harness: "omp", session: ID });
		expect(request?.boot).toMatch(/^[0-9a-f]{16}$/);
		expect(Date.parse(String(request?.started))).not.toBeNaN();
		expect(omp.sent).toHaveLength(1);
		expect(omp.sent[0].options).toEqual({ deliverAs: "aside" });
		expect(omp.sent[0].message.content).toBe("Aboard: your owner sent this\n\n<aboard-notice>1 waiting</aboard-notice>");
	});

	test("a session that comes back is told which agent it is again", async () => {
		daemon.onHello = c =>
			c.send({ v: 1, event: "welcome", boot: "b", reopened: true, agents: [{ server: "s", board: "docs", name: "reviewer" }] });
		const { omp } = await started([{ type: "message" }]);
		await until("the note", () => omp.sent.length === 1);
		expect(omp.sent[0].message.content).toContain("this session is reviewer on docs again");
		expect(omp.sent[0].options).toBeUndefined();
	});
});

describe("the connection", () => {
	test("a dropped connection reconnects after 200 ms with the same session and boot, as resumed", async () => {
		const { omp, conn } = await started();
		conn.sock.destroy();
		await until("a reconnect scheduled", () => omp.timers.length === 1);
		expect(omp.timers[0].ms).toBe(200);
		omp.runTimers();
		await until("the second hello", () => daemon.hellos().length === 2);
		const again = daemon.hellos()[1].frames[0];
		expect(again).toMatchObject({ session: ID, boot: conn.frames[0].boot, source: "startup", resumed: true });
	});

	test("after release it never connects again", async () => {
		const { omp, conn } = await started();
		conn.send({ v: 1, event: "release" });
		await until("the connection closed", () => conn.sock.destroyed || conn.sock.readyState !== "open");
		await Bun.sleep(50);
		expect(omp.timers).toHaveLength(0);
	});

	test("a daemon from another aboard is shown once, and tried again less often", async () => {
		daemon.onHello = c => {
			c.send({
				v: 1,
				error: { code: "daemon_protocol_mismatch", message: "The running delivery daemon speaks control protocol 2.", hint: "Run aboard down." },
			});
			c.sock.end();
		};
		const omp = fakeOmp();
		aboard(omp.pi as never);
		await omp.emit("session_start", { type: "session_start" }, omp.ctx(ID));
		await until("a retry scheduled", () => omp.timers.length === 1);
		expect(omp.notes).toEqual(["Aboard: The running delivery daemon speaks control protocol 2. Run aboard down."]);
		const delays: number[] = [omp.timers[0].ms];
		for (let i = 0; i < 8; i++) {
			omp.runTimers();
			await until("another retry", () => omp.timers.length === 1);
			delays.push(omp.timers[0].ms);
		}
		expect(omp.notes).toHaveLength(1);
		expect(delays[0]).toBe(200);
		expect(Math.max(...delays)).toBe(30_000);
	});

	test("with no daemon running it starts one with aboard daemon start, then connects", async () => {
		await daemon.stop();
		fs.rmSync(sockPath, { force: true });
		const omp = fakeOmp();
		aboard(omp.pi as never);
		await omp.emit("session_start", { type: "session_start" }, omp.ctx(ID));
		await until("the daemon started", () => fs.existsSync(daemonStarts));
		expect(fs.readFileSync(daemonStarts, "utf8")).toContain(`daemon start ABOARD_HOME=${home}`);
		await until("a reconnect scheduled", () => omp.timers.length === 1);
		await daemon.start();
		omp.runTimers();
		await until("the hello", () => daemon.hellos().length === 1);
	});

	test("goodbye when omp shuts down, and no reconnect", async () => {
		const { omp, c, conn } = await started();
		await omp.emit("session_shutdown", { type: "session_shutdown" }, c);
		await until("goodbye", () => conn.frames.some(f => f.op === "goodbye"));
		await Bun.sleep(50);
		expect(omp.timers).toHaveLength(0);
	});
});

describe("subagents' commands", () => {
	test("a subagent's bash command that runs aboard is marked; nothing else changes", async () => {
		const omp = fakeOmp();
		aboard(omp.pi as never);
		const sub = omp.ctx(ID, "sub");
		const call = (command: string, c = sub, toolName = "bash") =>
			omp.emit("tool_call", { type: "tool_call", toolCallId: "t", toolName, input: { command, timeout: 30 } }, c);
		expect(await call("aboard say hi")).toEqual({
			input: { command: "export ABOARD_SUBAGENT=0-Explore; aboard say hi", timeout: 30 },
		});
		expect(await call("ls aboard/")).toBeUndefined();
		expect(await call("export ABOARD_SUBAGENT=0-Explore; aboard read")).toBeUndefined();
		expect(await call("aboard say hi", omp.ctx(ID, "main"))).toBeUndefined();
		expect(await call("aboard say hi", sub, "python")).toBeUndefined();
	});

	test("it never throws into omp", async () => {
		const omp = fakeOmp();
		aboard(omp.pi as never);
		const broken = { ...omp.ctx(ID), sessionManager: { getSessionId: () => { throw new Error("boom"); } } };
		await omp.emit("session_start", { type: "session_start" }, broken);
		expect(await omp.emit("tool_call", { type: "tool_call", toolName: "bash", input: null }, omp.ctx(ID, "sub"))).toBeUndefined();
	});
});

describe("matching aboard's own rules", () => {
	test("a command runs aboard by the same pattern as Aboard's Claude Code hook", () => {
		const cases: [string, boolean][] = [
			["aboard status --json", true],
			["  aboard read", true],
			["cd /tmp && aboard say hi", true],
			["git log | aboard say -", true],
			["ABOARD_AGENT=writer aboard inbox", true],
			["env FOO=1 aboard inbox", true],
			["/Users/leo/go/bin/aboard status", true],
			["echo $(aboard status --json)", true],
			["(aboard read)", true],
			["true;aboard read", true],
			["cd /Users/leo/aboard && git status", false],
			["cat /Users/leo/aboard/README.md", false],
			["go test ./... # in aboard", false],
			["grep -r aboard .", false],
			["ls aboard-notes", false],
			["aboardx status", false],
			["git status", false],
		];
		for (const [command, runs] of cases) expect([command, RUNS_ABOARD.test(command)]).toEqual([command, runs]);
	});

	test("the daemon's socket is where spec/control.md says", () => {
		expect(stateDir({ ABOARD_HOME: "/h/aboard" })).toBe("/h/aboard/state");
		expect(stateDir({ XDG_STATE_HOME: "/x", HOME: "/h" })).toBe("/x/aboard");
		expect(stateDir({ HOME: "/h" })).toBe("/h/.local/state/aboard");
		expect(socketPath("/h/aboard/state")).toBe("/h/aboard/state/daemon.sock");
		// Too long for a socket: the first 16 hex digits of the SHA-256 of the folder, which
		// shasum -a 256 gives for this path too.
		const long = `/Users/someone/${"a".repeat(80)}/state`;
		expect(socketPath(long)).toBe(`/tmp/aboard-${process.getuid?.()}/8ca8a7a9906d368a.sock`);
	});
});
