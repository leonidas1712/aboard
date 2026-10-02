"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { type Board, type Member, type Message, type MessagePage, followHeads, get } from "./api";
import Problem, { StarterBadge } from "./problem";

const PAGE = 50;

type Filters = { from: string; role: string; to_me: boolean };

// BoardView shows one board: its timeline, live, and its crew.
export default function BoardView({ name }: { name: string }) {
  const path = `/v1/boards/${encodeURIComponent(name)}`;
  const [board, setBoard] = useState<Board | null>(null);
  const [members, setMembers] = useState<Member[]>([]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [prevBefore, setPrevBefore] = useState<number | null>(null);
  const [filters, setFilters] = useState<Filters>({ from: "", role: "", to_me: false });
  const [error, setError] = useState<unknown>(null);
  // Reads of the timeline run one at a time, so a live update never races the first
  // page; newest is the seq of the newest message shown.
  const queue = useRef<Promise<void>>(Promise.resolve());
  const newest = useRef(0);

  const loadCrew = useCallback(
    () => get<{ members: Member[] }>(`${path}/members`).then((r) => setMembers(r.members), setError),
    [path],
  );

  useEffect(() => {
    get<Board>(path).then(setBoard, setError);
  }, [path]);

  useEffect(() => {
    let live = true;
    const run = (read: () => Promise<void>) => {
      queue.current = queue.current.then(read).catch((e) => {
        if (live) setError(e);
      });
    };
    run(async () => {
      const page = await get<MessagePage>(`${path}/messages`, { ...filters, newest: true, limit: PAGE });
      if (!live) return;
      setMessages(page.messages);
      setPrevBefore(page.prev_before);
      newest.current = page.messages.at(-1)?.seq ?? 0;
    });
    // When the board moves, read the matching messages after the newest one shown.
    const catchUp = async () => {
      for (;;) {
        const page = await get<MessagePage>(`${path}/messages`, { ...filters, after: newest.current, limit: PAGE });
        if (!live) return;
        const last = page.messages.at(-1);
        if (last) {
          newest.current = last.seq;
          setMessages((shown) => [...shown, ...page.messages]);
        }
        if (page.next_after === null) return;
      }
    };
    loadCrew();
    const stop = followHeads((b) => {
      if (b !== name) return;
      run(catchUp);
      loadCrew();
    });
    return () => {
      live = false;
      stop();
    };
  }, [path, name, filters, loadCrew]);

  const loadEarlier = () => {
    if (prevBefore === null) return;
    queue.current = queue.current
      .then(async () => {
        const page = await get<MessagePage>(`${path}/messages`, { ...filters, newest: true, before: prevBefore, limit: PAGE });
        setMessages((shown) => [...page.messages, ...shown]);
        setPrevBefore(page.prev_before);
      })
      .catch(setError);
  };

  return (
    <>
      <p className="crumbs">
        <a href="/">Boards</a> /
      </p>
      <h1>
        {name} {board?.policy.preset === "starter" && <StarterBadge />}
      </h1>
      {board?.charter && <p className="muted">{board.charter}</p>}
      {error !== null && <Problem error={error} />}
      <div className="columns">
        <section className="timeline" aria-label="Timeline">
          <form className="filters" onSubmit={(e) => e.preventDefault()}>
            <label>
              From{" "}
              <select value={filters.from} onChange={(e) => setFilters({ ...filters, from: e.target.value })}>
                <option value="">anyone</option>
                {members.map((m) => (
                  <option key={m.name} value={m.name}>
                    @{m.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Role{" "}
              <select value={filters.role} onChange={(e) => setFilters({ ...filters, role: e.target.value })}>
                <option value="">any</option>
                {Object.keys(board?.roles ?? {}).map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <input type="checkbox" checked={filters.to_me} onChange={(e) => setFilters({ ...filters, to_me: e.target.checked })} />{" "}
              To me
            </label>
          </form>
          {prevBefore !== null && (
            <button type="button" onClick={loadEarlier}>
              Load earlier messages
            </button>
          )}
          {messages.length === 0 && <p className="muted">No messages yet.</p>}
          <ol className="messages">
            {messages.map((m) => (
              <MessageItem key={m.id} m={m} />
            ))}
          </ol>
        </section>
        <Crew members={members} />
      </div>
    </>
  );
}

// MessageItem shows one message. The body is untrusted text and is only ever
// rendered as text.
function MessageItem({ m }: { m: Message }) {
  const who = m.from.kind === "agent" ? `${m.from.role}, ${m.from.owner}` : "human";
  return (
    <li className="message">
      <div className="meta">
        <span className="seq">#{m.seq}</span> <strong>@{m.from.name}</strong> <span className="muted">({who})</span> →{" "}
        {m.to.join(", ")}
        {m.reply_to_seq !== null && <span className="muted"> · reply to #{m.reply_to_seq}</span>}
        {m.urgent && <span className="urgent"> · urgent</span>}
        <time className="muted" dateTime={m.at}>
          {new Date(m.at).toLocaleString()}
        </time>
      </div>
      <p className="body">{m.body}</p>
    </li>
  );
}

// Crew lists the board's members, grouped by the person who owns them.
function Crew({ members }: { members: Member[] }) {
  const owners = new Map<string, Member[]>();
  for (const m of members) {
    const owner = m.kind === "human" ? m.name : (m.owner ?? "");
    owners.set(owner, [...(owners.get(owner) ?? []), m]);
  }
  return (
    <aside className="crew" aria-label="Crew">
      <h2>Crew</h2>
      {[...owners].map(([owner, ms]) => (
        <section key={owner}>
          <h3>{owner}</h3>
          <ul>
            {ms.map((m) => (
              <li key={m.name}>
                @{m.name} <span className="muted">{m.kind === "agent" ? `${m.role} · agent` : "human"}</span>
              </li>
            ))}
          </ul>
        </section>
      ))}
    </aside>
  );
}
