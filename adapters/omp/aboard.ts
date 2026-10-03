/**
 * Aboard's extension for omp. `aboard init` installs this file into omp's extensions
 * folder, and omp's Bun runs it as it is, with no build step. It connects each omp
 * session to Aboard:
 *
 * - Identity: the main agent's commands carry ABOARD_SESSION=omp:<session id>, so an
 *   aboard command run in the session acts as that session's agent.
 * - Delivery: it holds the session's connection to Aboard's delivery daemon (the
 *   extension connection in Aboard's spec/control.md). While the session is idle the
 *   daemon sends bundles of messages over it; the extension adds each to the session,
 *   which starts a turn, and confirms it. It reports when turns start and end.
 * - The owner's messages mid-turn: after each step that ran tools, it asks the daemon for
 *   the owner's messages and adds them as an aside, which omp adds at the next step
 *   without interrupting a running tool.
 * - Subagents: a subagent never connects, and its aboard commands are marked with
 *   ABOARD_SUBAGENT, so they may read the board but never act as the main agent.
 *
 * It imports only omp's types, so it needs nothing installed. It never throws into omp:
 * a failure is written to omp-extension.log in Aboard's state folder.
 */
import type { ExtensionAPI, ExtensionContext } from "@oh-my-pi/pi-coding-agent";
import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import * as fs from "node:fs";
import * as net from "node:net";
import * as os from "node:os";
import * as path from "node:path";

// aboard init writes the aboard binary that installed this file, and the ABOARD_HOME it
// ran with, into these two strings. Left as they are, aboard comes from the PATH and
// ABOARD_HOME from omp's environment.
const INSTALLED_ABOARD = "{aboard_binary}";
const INSTALLED_HOME = "{aboard_home}";

/** The control protocol version this extension speaks. */
const PROTOCOL = 1;
/** This extension's version, sent in hello; it changes when the file does. */
const EXTENSION_VERSION = "1";
const HARNESS = "omp";

/** One id per omp process, so a bundle handed to an earlier process goes again. */
const BOOT = randomBytes(8).toString("hex");
/** Deliveries already added to a session in this process, by id. */
const ADDED = new Set<number>();

/** What a session id may contain, as Aboard's hooks accept it. */
const SESSION_ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;

/**
 * A shell command that runs aboard as a command: at the start, or after a separator, a
 * pipe, a subshell or a command substitution, optionally behind variable assignments, a
 * wrapper such as env, or a path ending in /aboard. The same pattern as Aboard's
 * Claude Code hook uses, so a command that only names a folder called aboard is left as
 * it is.
 */
export const RUNS_ABOARD =
	/(^|[;&|(`\n]|\$\()\s*(?:(?:[A-Za-z_][A-Za-z0-9_]*=\S*|env|command|exec|nohup|time)\s+)*(?:\S*\/)?aboard(?:$|[\s;&|)])/;

const filled = (v: string): boolean => v !== "" && !v.startsWith("{");

/** The aboard binary to run. */
function aboardBinary(): string {
	return filled(INSTALLED_ABOARD) ? INSTALLED_ABOARD : "aboard";
}

/** The environment aboard runs with: omp's own, with the ABOARD_HOME aboard init used. */
function aboardEnv(): NodeJS.ProcessEnv {
	const env = { ...process.env };
	if (filled(INSTALLED_HOME)) env.ABOARD_HOME = INSTALLED_HOME;
	// A command started from here isn't one of the session's own commands.
	delete env.ABOARD_SESSION;
	delete env.ABOARD_SUBAGENT;
	return env;
}

/** Aboard's state folder, where the daemon's socket and this extension's log live. */
export function stateDir(env: NodeJS.ProcessEnv): string {
	if (env.ABOARD_HOME && path.isAbsolute(env.ABOARD_HOME)) return path.join(env.ABOARD_HOME, "state");
	if (env.XDG_STATE_HOME && path.isAbsolute(env.XDG_STATE_HOME)) return path.join(env.XDG_STATE_HOME, "aboard");
	return path.join(env.HOME || os.homedir(), ".local", "state", "aboard");
}

/**
 * The daemon's socket for a state folder: in the folder, or, when that path is longer
 * than a socket path may be, under /tmp/aboard-<uid>/, named after a hash of the folder.
 */
export function socketPath(state: string): string {
	const p = path.join(state, "daemon.sock");
	if (Buffer.byteLength(p) <= 100) return p;
	const sum = createHash("sha256").update(state).digest("hex").slice(0, 16);
	return `/tmp/aboard-${process.getuid?.() ?? 0}/${sum}.sock`;
}

/** Writes one line to the extension's log. Never throws. */
function log(msg: string, fields: Record<string, unknown> = {}): void {
	try {
		const dir = stateDir(aboardEnv());
		fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
		fs.appendFileSync(
			path.join(dir, "omp-extension.log"),
			`${JSON.stringify({ time: new Date().toISOString(), pid: process.pid, msg, ...fields })}\n`,
		);
	} catch {
		// Nowhere left to say it.
	}
}

/** Runs f, logging anything it throws instead of passing it to omp. */
async function guard<T>(what: string, f: () => T | Promise<T>): Promise<T | undefined> {
	try {
		return await f();
	} catch (err) {
		log(`${what} failed`, { error: String(err) });
		return undefined;
	}
}

/** Quotes s for a shell command line when it needs quoting. */
function shellWord(s: string): string {
	return /^[A-Za-z0-9_./-]+$/.test(s) ? s : `'${s.replaceAll("'", `'\\''`)}'`;
}

/** One message from the daemon. */
interface Frame {
	v?: number;
	event?: string;
	id?: number;
	bundle?: string;
	notice?: string;
	boot?: string;
	agents?: { server: string; board: string; name: string }[];
	reopened?: boolean;
	lost?: { server: string; board: string; name: string };
	error?: { code: string; message: string; hint: string };
}

/** Sends one request on a connection of its own and returns the daemon's answer. */
function ask(request: Record<string, unknown>, timeoutMs = 10_000): Promise<Frame> {
	return new Promise(resolve => {
		let buf = "";
		let done = false;
		const finish = (f: Frame) => {
			if (done) return;
			done = true;
			sock.destroy();
			resolve(f);
		};
		const sock = net.createConnection(socketPath(stateDir(aboardEnv())));
		sock.setEncoding("utf8");
		sock.setTimeout(timeoutMs, () => finish({}));
		sock.on("connect", () => sock.write(`${JSON.stringify({ v: PROTOCOL, ...request })}\n`));
		sock.on("data", (chunk: string) => {
			buf += chunk;
			const i = buf.indexOf("\n");
			if (i < 0) return;
			try {
				finish(JSON.parse(buf.slice(0, i)) as Frame);
			} catch {
				finish({});
			}
		});
		sock.on("error", () => finish({}));
		sock.on("close", () => finish({}));
	});
}

/** The main agent's link to the daemon: one connection, for the session omp runs now. */
class Link {
	readonly #pi: ExtensionAPI;
	#ctx: ExtensionContext | undefined;
	#session = "";
	#source = "startup";
	#sock: net.Socket | undefined;
	/** The session's connection was welcomed before, so a new one reconnects. */
	#welcomed = false;
	#connected = false;
	/** The daemon said another connection serves the session, or the session ended. */
	#stopped = false;
	#busy = false;
	#attempt = 0;
	/** The daemon speaks another protocol: retry less often. */
	#mismatch = false;
	#notified = false;
	#timer: unknown;

	constructor(pi: ExtensionAPI) {
		this.#pi = pi;
	}

	/** Takes the session omp runs now, connecting it, or moving to it from another. */
	open(ctx: ExtensionContext): void {
		this.#ctx = ctx;
		const id = ctx.sessionManager.getSessionId();
		if (!SESSION_ID.test(id)) {
			log("the session id isn't usable", { session: id });
			return;
		}
		process.env.ABOARD_SESSION = `${HARNESS}:${id}`;
		if (id === this.#session && !this.#stopped) return;
		if (this.#session !== "") this.goodbye();
		this.#session = id;
		this.#source = ctx.sessionManager.getEntries().some(e => e.type === "message") ? "resume" : "startup";
		this.#welcomed = this.#connected = this.#stopped = this.#busy = this.#mismatch = false;
		this.#attempt = 0;
		this.#connect();
	}

	/** Says the session closed, and doesn't connect again. */
	goodbye(): void {
		this.#stopped = true;
		this.#clearTimer();
		const sock = this.#sock;
		this.#sock = undefined;
		if (sock && this.#connected) sock.write(`${JSON.stringify({ v: PROTOCOL, op: "goodbye" })}\n`);
		sock?.end();
		this.#connected = false;
		log("session closed", { session: this.#session });
	}

	/** A turn started (busy) or ended for good (idle). */
	turn(busy: boolean): void {
		this.#busy = busy;
		this.#send({ op: busy ? "prompt" : "turn_end" });
	}

	/** At a tool boundary of a busy turn, adds the owner's messages and the waiting notice. */
	async boundary(): Promise<void> {
		if (!this.#connected) return;
		const answer = await ask({
			op: "boundary",
			harness: HARNESS,
			session: this.#session,
			boot: BOOT,
			started: new Date().toISOString(),
		});
		if (answer.error) {
			log("tool boundary refused", { code: answer.error.code });
			return;
		}
		const text = [answer.bundle, answer.notice].filter(Boolean).join("\n\n").trim();
		if (text === "") return;
		this.#pi.sendMessage(
			{ customType: "aboard", content: text, display: true, attribution: "agent" },
			{ deliverAs: "aside" },
		);
		log("tool boundary", { session: this.#session, bytes: text.length });
	}

	#send(request: Record<string, unknown>): void {
		if (this.#sock && this.#connected) this.#sock.write(`${JSON.stringify({ v: PROTOCOL, ...request })}\n`);
	}

	#connect(): void {
		if (this.#stopped || this.#session === "") return;
		const sock = net.createConnection(socketPath(stateDir(aboardEnv())));
		this.#sock = sock;
		let buf = "";
		let failure = "";
		sock.setEncoding("utf8");
		sock.on("connect", () => {
			const ctx = this.#ctx;
			sock.write(
				`${JSON.stringify({
					v: PROTOCOL,
					op: "hello",
					harness: HARNESS,
					session: this.#session,
					boot: BOOT,
					source: this.#source,
					resumed: this.#welcomed || undefined,
					process: { pid: process.pid },
					cwd: ctx?.cwd,
					harness_version: (this.#pi as { pi?: { VERSION?: string } }).pi?.VERSION,
					extension_version: EXTENSION_VERSION,
				})}\n`,
			);
		});
		sock.on("data", (chunk: string) => {
			buf += chunk;
			for (let i = buf.indexOf("\n"); i >= 0; i = buf.indexOf("\n")) {
				const line = buf.slice(0, i);
				buf = buf.slice(i + 1);
				void guard("handle a message from the daemon", () => this.#onFrame(sock, line));
			}
		});
		sock.on("error", (err: NodeJS.ErrnoException) => {
			failure = err.code ?? String(err);
		});
		sock.on("close", () => {
			if (this.#sock !== sock) return;
			this.#sock = undefined;
			const wasConnected = this.#connected;
			this.#connected = false;
			if (this.#stopped) return;
			if (wasConnected) log("connection to the daemon closed; connecting again", { session: this.#session });
			void guard("connect again", async () => {
				if (!wasConnected && (failure === "ENOENT" || failure === "ECONNREFUSED")) await this.#startDaemon();
				this.#retry();
			});
		});
	}

	#onFrame(sock: net.Socket, line: string): void {
		if (sock !== this.#sock || line.trim() === "") return;
		const f = JSON.parse(line) as Frame;
		if (f.error) {
			const { code, message, hint } = f.error;
			log("the daemon refused the connection", { code, message });
			if (code === "daemon_protocol_mismatch" || code === "invalid_request") {
				// A newer or older aboard than this extension: running the aboard that installed
				// it replaces the daemon, so keep trying, less often.
				this.#mismatch = true;
				this.#notifyOnce(`Aboard: ${message} ${hint}`);
			} else if (code === "subagent_session") {
				this.#stopped = true;
			}
			return;
		}
		switch (f.event) {
			case "welcome":
				this.#connected = true;
				this.#attempt = 0;
				this.#mismatch = false;
				log("connected", { session: this.#session, source: this.#source, resumed: this.#welcomed, reopened: !!f.reopened });
				this.#welcomed = true;
				this.#note(f);
				// Reconnected during a turn: the daemon took the session for idle.
				if (this.#busy) this.#send({ op: "prompt" });
				return;
			case "deliver":
				if (typeof f.id === "number" && typeof f.bundle === "string") this.#deliver(f.id, f.bundle);
				return;
			case "release":
				log("the daemon released this connection: another one serves the session", { session: this.#session });
				this.#stopped = true;
				this.#connected = false;
				this.#sock = undefined;
				sock.end();
				return;
		}
	}

	/** Adds a bundle to the session once, and confirms it. */
	#deliver(id: number, bundle: string): void {
		if (!ADDED.has(id)) {
			const message = { customType: "aboard", content: bundle, display: true, attribution: "agent" as const };
			if (this.#idle()) {
				this.#pi.sendMessage(message, { triggerTurn: true });
			} else {
				// A turn started as the bundle came: the owner's messages go in at the next step,
				// anyone else's once the turn ends.
				this.#pi.sendMessage(message, { deliverAs: bundle.includes('sender="owner"') ? "aside" : "followUp" });
			}
			ADDED.add(id);
			log("delivered", { session: this.#session, delivery: id, bytes: bundle.length });
		}
		this.#send({ op: "received", id });
	}

	#idle(): boolean {
		try {
			return this.#ctx ? this.#ctx.isIdle() : !this.#busy;
		} catch {
			return !this.#busy;
		}
	}

	/** Tells a session that came back which agent it is again, or which it lost. */
	#note(f: Frame): void {
		let text = "";
		const agent = f.agents?.[0];
		if (f.reopened && f.agents?.length === 1 && agent) {
			text = `Aboard: this session is ${agent.name} on ${agent.board} again, as it was before it closed.`;
		} else if (f.lost) {
			const a = f.lost;
			text =
				`Aboard: this session was ${a.name} on ${a.board} until another session resumed ${a.name}; it has no agent now. ` +
				`To act as ${a.name} here again, run aboard resume ${a.name}, which leaves the other session without it.`;
		}
		if (text !== "") this.#pi.sendMessage({ customType: "aboard", content: text, display: true, attribution: "agent" });
	}

	#notifyOnce(text: string): void {
		if (this.#notified) return;
		this.#notified = true;
		try {
			this.#ctx?.ui.notify(text, "warning");
		} catch (err) {
			log("notify failed", { error: String(err) });
		}
	}

	/** Connects again after 200 ms, then doubling up to 5 seconds, or 30 for a mismatch. */
	#retry(): void {
		if (this.#stopped) return;
		const delay = Math.min(200 * 2 ** this.#attempt, this.#mismatch ? 30_000 : 5_000);
		this.#attempt++;
		this.#clearTimer();
		const run = () => void guard("connect", () => this.#connect());
		const ctx = this.#ctx;
		this.#timer = ctx ? ctx.setTimeout(run, delay) : setTimeout(run, delay);
	}

	#clearTimer(): void {
		if (this.#timer === undefined) return;
		try {
			if (this.#ctx) this.#ctx.clearTimer(this.#timer as ReturnType<ExtensionContext["setTimeout"]>);
			else clearTimeout(this.#timer as ReturnType<typeof setTimeout>);
		} catch {
			// The timer is gone already.
		}
		this.#timer = undefined;
	}

	/** Starts the delivery daemon, which returns once it answers, as aboard's hooks do. */
	#startDaemon(): Promise<void> {
		return new Promise(resolve => {
			const child = spawn(aboardBinary(), ["daemon", "start"], { env: aboardEnv(), stdio: "ignore" });
			const timer = setTimeout(() => child.kill(), 15_000);
			child.on("error", err => {
				log("couldn't start the delivery daemon", { aboard: aboardBinary(), error: String(err) });
				clearTimeout(timer);
				resolve();
			});
			child.on("exit", code => {
				if (code !== 0) log("aboard daemon start failed", { aboard: aboardBinary(), code });
				clearTimeout(timer);
				resolve();
			});
		});
	}
}

/** Marks a subagent's bash command that runs aboard as the subagent's, or returns nothing. */
function markSubagent(input: Record<string, unknown>, agentId: string): Record<string, unknown> | undefined {
	const command = input.command;
	if (typeof command !== "string" || !RUNS_ABOARD.test(command)) return undefined;
	const prefix = `export ABOARD_SUBAGENT=${shellWord(agentId)}; `;
	if (command.startsWith(prefix)) return undefined;
	return { ...input, command: prefix + command };
}

const isSubagent = (ctx: ExtensionContext): boolean => ctx.agent?.kind === "sub";

export default function aboard(pi: ExtensionAPI): void {
	// omp binds this factory again for every subagent session; each binding has its own
	// link, and only the main agent's ever connects.
	const link = new Link(pi);
	const session = (ctx: ExtensionContext) =>
		guard("connect the session", () => {
			if (!isSubagent(ctx)) link.open(ctx);
		});
	pi.on("session_start", (_event, ctx) => session(ctx));
	pi.on("session_switch", (_event, ctx) => session(ctx));
	pi.on("session_branch", (_event, ctx) => session(ctx));
	pi.on("agent_start", (_event, ctx) =>
		guard("report a turn's start", () => {
			if (!isSubagent(ctx)) link.turn(true);
		}),
	);
	pi.on("agent_end", (event, ctx) =>
		guard("report a turn's end", () => {
			// omp continues by itself (a retry, a compaction): the turn isn't over.
			if (!isSubagent(ctx) && !event.willContinue) link.turn(false);
		}),
	);
	pi.on("turn_end", (event, ctx) =>
		guard("add the owner's messages", async () => {
			if (!isSubagent(ctx) && (event.toolResults?.length ?? 0) > 0) await link.boundary();
		}),
	);
	pi.on("tool_call", async (event, ctx) => {
		// A failure here would block the tool, so it never throws.
		const result = await guard("mark a subagent's command", () => {
			if (!isSubagent(ctx) || event.toolName !== "bash") return undefined;
			const input = markSubagent(event.input as Record<string, unknown>, ctx.agent.id);
			return input ? { input } : undefined;
		});
		return result ?? undefined;
	});
	pi.on("session_shutdown", (_event, ctx) =>
		guard("say goodbye", () => {
			if (!isSubagent(ctx)) link.goodbye();
		}),
	);
}
