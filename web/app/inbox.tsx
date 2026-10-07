"use client";

import { Clock, CornerDownLeft } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type AskList, type Board, type Me, type Member, type Message, ackBoard, follow, get, listTasks } from "./api";
import { Account } from "./account";
import { AskAnswers, askChanged } from "./ask-ui";
import { type BoardFacts, askSummary, orderedAsks, worthALook } from "./asks";
import { Header, Problem } from "./chrome";
import { keyLabel, pressed, typing } from "./keys";
import { Kbd, KeysSheet } from "./keys-sheet";
import { usePref } from "./prefs";
import { BoardNav } from "./sidebars";
import { SenderMark } from "./timeline";
import { clockTime, identityOf, relativeTime } from "./words";

export default function Inbox({ onSignOut }: { onSignOut: () => void }) {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [asks, setAsks] = useState<Message[] | null>(null);
  const [facts, setFacts] = useState<BoardFacts[]>([]);
  const [me, setMe] = useState("");
  const [more, setMore] = useState(false);
  const [unavailable, setUnavailable] = useState(0);
  const [picked, setPicked] = useState<string | null>(null);
  const [sent, setSent] = useState<{ to: string; body: string; at: number } | null>(null);
  const [fading, setFading] = useState(false);
  const [keysOpen, setKeysOpen] = useState(false);
  const [snoozed, setSnoozed] = usePref<Record<string, number>>("aboard.inbox.snoozed", {});
  const slot = useRef(0);
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(Date.now());
  const refresh = useRef<() => void>(() => {});
  useEffect(() => {
    let live = true;
    let generation = 0;
    const load = async () => {
      const gen = ++generation;
      try {
        const [list, questions, person] = await Promise.all([get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }), get<AskList>("/v1/asks", { to_me: true, state: "open", limit: 200 }), get<Me>("/v1/me")]);
        if (!live || gen !== generation) return;
        setBoards(list.boards);
        setFacts((previous) => previous.filter((f) => list.boards.some((b) => b.id === f.board.id && b.on_board && b.lifecycle !== "archived")));
        setAsks(orderedAsks(questions.asks));
        setMore(questions.more);
        setMe(person.name);
        setError(null);
        const details = await Promise.allSettled(list.boards.filter((b) => b.on_board && b.lifecycle !== "archived").map(async (board) => {
          const [members, tasks] = await Promise.all([get<{ members: Member[] }>(`/v1/boards/${encodeURIComponent(board.name)}/members`), listTasks(board.name)]);
          return { board, members: members.members, tasks: tasks.tasks };
        }));
        if (!live || gen !== generation) return;
        setFacts(details.flatMap((r) => r.status === "fulfilled" ? [r.value] : []));
        setUnavailable(details.filter((r) => r.status === "rejected").length);
      } catch (e) {
        if (!live || gen !== generation) return;
        setAsks(null);
        setFacts([]);
        setError(e);
      }
    };
    refresh.current = () => { void load(); };
    void load();
    const stop = follow({ head: () => void load(), presence: () => void load(), unavailable: () => void load(), error: (e) => live && setError(e) });
    const changed = () => void load();
    window.addEventListener(askChanged, changed);
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => { live = false; stop(); clearInterval(tick); window.removeEventListener(askChanged, changed); };
  }, []);
  // Snoozing is kept by this browser only: the API has no per-person snooze yet.
  const waiting = asks?.filter((a) => !(snoozed[a.id] > now)) ?? null;
  const resting = (asks?.length ?? 0) - (waiting?.length ?? 0);
  // Answering or snoozing an ask advances in place: the ask now at its index opens.
  const current = waiting?.find((a) => a.id === picked) ?? waiting?.[Math.min(slot.current, waiting.length - 1)] ?? null;
  const index = current ? waiting!.indexOf(current) : -1;
  useEffect(() => {
    if (!current) return;
    slot.current = index;
    if (current.id !== picked) setPicked(current.id);
  }, [current, index, picked]);
  useEffect(() => {
    if (current) document.querySelector(`[data-ask="${CSS.escape(current.id)}"]`)?.scrollIntoView({ block: "nearest" });
  }, [current]);
  useEffect(() => {
    if (!sent) return;
    setFading(false);
    const fade = setTimeout(() => setFading(true), 4000);
    const gone = setTimeout(() => setSent(null), 4200);
    return () => { clearTimeout(fade); clearTimeout(gone); };
  }, [sent]);
  const move = useCallback((n: number) => { setPicked(waiting?.[n]?.id ?? null); setSent(null); }, [waiting]);
  const snooze = useCallback((m: Message) => {
    const at = Date.now();
    const kept = Object.fromEntries(Object.entries(snoozed).filter(([, until]) => until > at));
    setSnoozed({ ...kept, [m.id]: at + 3_600_000 });
    setPicked(null);
    setNow(at);
  }, [snoozed, setSnoozed]);
  const boardHref = current ? `/?board=${encodeURIComponent(current.board!)}&message=${encodeURIComponent(current.id)}&seq=${current.seq}` : "/";
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (typing(e)) return;
      if (pressed("help", e)) setKeysOpen(true);
      else if (!current) return;
      else if (pressed("next", e)) move(Math.min(waiting!.length - 1, index + 1));
      else if (pressed("previous", e)) move(Math.max(0, index - 1));
      else if (pressed("snooze", e)) snooze(current);
      else if (pressed("open", e) && e.target === document.body) window.location.href = boardHref;
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [waiting, current, index, move, snooze, boardHref]);
  const notices = worthALook(facts, me, now);
  const working = facts.map((f) => ({ board: f.board, agents: f.members.filter((m) => m.kind === "agent" && (!m.status || m.status === "active") && m.presence === "working").length })).filter((f) => f.agents > 0);
  const busiest = working.reduce<(typeof working)[number] | null>((a, b) => (a && a.agents >= b.agents ? a : b), null);
  const workingCount = working.reduce((n, f) => n + f.agents, 0);
  const markRead = (b: Board) => { void ackBoard(b.name, b.head_seq).then(() => refresh.current(), setError); };
  const answered = (reply: Message) => { if (current) setSent({ to: current.from.name, body: reply.body, at: Date.now() }); setPicked(null); refresh.current(); };
  const boardTitle = (name: string | null | undefined) => boards?.find((b) => b.name === name)?.title ?? name;
  const blocking = waiting?.filter((m) => m.ask?.blocking) ?? [];
  const going = waiting?.filter((m) => !m.ask?.blocking) ?? [];
  const row = (m: Message) => {
    const selected = m.id === current?.id;
    const ask = m.ask!;
    return <li key={m.id}><button type="button" data-ask={m.id} onClick={() => { setPicked(m.id); setSent(null); }} aria-current={selected ? "true" : undefined} className={cn("grid w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-2.5 rounded-control px-2.5 py-2 text-left transition-colors duration-[140ms] ease-out hover:bg-hover", selected && "bg-selected hover:bg-selected")}>
      <SenderMark name={m.from.name} kind={m.from.kind} identity={identityOf(`${m.from.kind}:${m.from.name}`)} className="mt-0.5 size-7" />
      <span className="flex min-w-0 flex-col">
        <span className="flex min-w-0 items-baseline gap-3"><span className={cn("min-w-0 flex-1 truncate", selected && "font-bold")}>{askSummary(m.body)}</span><span className="shrink-0 text-meta text-muted tabular-nums">{age(m.at, now)}</span></span>
        <span className="truncate text-meta text-muted">{boardTitle(m.board)} · {m.from.name} · {ask.blocking ? "blocking" : `going with ${ask.going_with}${ask.going_at ? ` at ${clockTime(ask.going_at)}` : ""}`}</span>
      </span>
    </button></li>;
  };
  // The headings stay in place while the list scrolls under them.
  const group = (title: string, label: string, list: Message[], first: boolean) => <>
    <h2 className={cn("glass glass-page z-[1] px-2.5 lg:sticky lg:top-0 pb-1 text-meta font-bold", first ? "pt-2" : "pt-5")}>{title}</h2>
    <ul aria-label={label} className="flex flex-col gap-0.5">{list.map(row)}</ul>
  </>;
  const rest = current ? askDetail(current.body) : "";
  return <TooltipProvider delayDuration={250}><div className="inbox-page flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
    <Header account={<Account onSignOut={onSignOut} />} />
    <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[252px_minmax(280px,360px)_minmax(0,1fr)]">
      <aside aria-label="Boards" className="quiet-scroll order-3 border-t border-rule bg-sidebar px-5 py-4 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:border-r"><BoardNav current="" boards={boards} onMarkRead={markRead} /></aside>
      <section aria-labelledby="inbox-title" className="quiet-scroll order-1 flex flex-col gap-1 px-3 pb-4 lg:order-none lg:overflow-y-auto lg:border-r lg:border-rule">
        <h1 id="inbox-title" className="flex flex-wrap items-baseline gap-x-2 px-2.5 pt-4 pb-1 text-headline font-bold">Inbox<span className="text-meta font-normal text-muted tabular-nums">{waiting && `${waiting.length} ${waiting.length === 1 ? "needs" : "need"} you`}</span></h1>
        {error !== null && <Problem error={error} />}
        {asks === null && error === null && <p role="status" className="px-2.5 text-muted">Loading asks…</p>}
        <div role="group" aria-label="Needs you asks" className="flex flex-col gap-0.5">
          {blocking.length > 0 && going.length > 0 ? <>{group("Blocking", "Blocking asks", blocking, true)}{group("Going ahead unless you say", "Asks going ahead unless you say", going, false)}</> : group("Needs you", "Asks", waiting ?? [], true)}
        </div>
        {waiting?.length === 0 && <p className="px-2.5 text-meta text-muted">Nothing waits on you.</p>}
        {resting > 0 && <p className="px-2.5 pt-2 text-meta text-muted tabular-nums">{resting} snoozed on this browser · <button type="button" className="text-link hover:underline" onClick={() => setSnoozed({})}>Show {resting === 1 ? "it" : "them"} now</button></p>}
        {more && <p className="px-2.5 text-meta text-muted">Showing the first {asks?.length ?? 0} asks.</p>}
        {notices.length > 0 && <><h2 className="px-2.5 pt-5 pb-1 text-meta font-bold text-muted">Worth a look</h2><ul aria-label="Worth a look" className="flex flex-col gap-0.5">{notices.map((n) => <li key={n.id}><a href={`/?board=${encodeURIComponent(n.board)}${n.task ? `&task=${encodeURIComponent(n.task)}` : ""}`} className="block rounded-control px-2.5 py-2.5 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-hover"><span className="block"><Clock className="mr-1.5 inline size-3.5 text-muted" aria-hidden />{n.text}</span><span className="text-meta text-muted">{n.title} · {n.detail}</span></a></li>)}</ul></>}
        {unavailable > 0 && <p className="px-2.5 pt-4 text-meta text-muted">Some board details could not be read. Refresh the Inbox to try again.</p>}
      </section>
      <section aria-label="The selected ask" className="quiet-scroll order-2 border-t border-rule px-4 py-6 sm:px-8 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:px-10 lg:py-8">
        {sent && <p role="status" key={sent.at} className={cn("mb-5 max-w-[640px] truncate text-meta text-muted", fading ? "animate-fade-out" : "animate-fade-in")}>Sent to @{sent.to}: &ldquo;{sent.body}&rdquo;</p>}
        {current ? <article className="flex max-w-[640px] flex-col gap-4" key={current.id}>
          <p className="text-meta text-muted"><a href={boardHref}>{boardTitle(current.board)}</a>{current.ask?.task && ` · ${current.ask.task}`}</p>
          <p className="flex items-center gap-2"><SenderMark name={current.from.name} kind={current.from.kind} identity={identityOf(`${current.from.kind}:${current.from.name}`)} /><strong>{current.from.name}</strong><span className="text-meta text-muted">{current.from.owner && `(${current.from.owner}'s agent) `}asks you · {relativeTime(current.at, now)}</span></p>
          <div className="flex flex-col gap-2"><h2 className="text-headline font-bold break-words">{askSummary(current.body)}</h2>{rest && <p className="text-now whitespace-pre-wrap break-words">{rest}</p>}</div>
          <AskAnswers message={current} board={current.board!} keyboard onAnswered={answered} />
          <p className="flex flex-wrap items-center gap-x-5 gap-y-1 text-meta text-muted">
            <a href={boardHref} className="inline-flex min-h-10 items-center gap-1.5">Open on the board<CornerDownLeft className="size-3" aria-label="Enter" /></a>
            <button type="button" className="inline-flex min-h-10 items-center gap-1.5 text-link hover:underline" onClick={() => snooze(current)}>Snooze for an hour on this browser<Kbd>{keyLabel("snooze")}</Kbd></button>
            <span className="inline-flex items-center gap-1.5"><Kbd>{keyLabel("next")}</Kbd><Kbd>{keyLabel("previous")}</Kbd>to move</span>
            <button type="button" className="inline-flex min-h-10 items-center gap-1.5 text-link hover:underline" onClick={() => setKeysOpen(true)}><Kbd>{keyLabel("help")}</Kbd>All keys</button>
          </p>
        </article> : asks !== null && <div className="max-w-[560px]"><h2 className="text-title font-bold">Nothing waits on you.</h2><p className="mt-2 text-muted">Asks addressed to you appear here from every board.{busiest && <> · <a href={`/?board=${encodeURIComponent(busiest.board.name)}`} className="tabular-nums" title={`Open ${busiest.board.title ?? busiest.board.name}, where most of them are`}>{workingCount} {workingCount === 1 ? "agent" : "agents"} working on {working.length} {working.length === 1 ? "board" : "boards"}</a></>}</p></div>}
      </section>
    </div>
    <KeysSheet open={keysOpen} onClose={() => setKeysOpen(false)} />
  </div></TooltipProvider>;
}

/** age is how long ago an ask came, short enough for a row's title line. */
function age(at: string, now: number): string {
  const words = relativeTime(at, now);
  return words === "just now" ? "now" : words.replace(/ ago$/, "");
}

/** askDetail is an ask's text after its first line, which the Inbox shows as its summary. */
function askDetail(body: string): string {
  const cut = body.indexOf("\n");
  return cut < 0 ? "" : body.slice(cut + 1).trim();
}
