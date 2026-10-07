"use client";

import { ArrowDown, ArrowUp, Clock, CornerDownLeft } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type AskList, type Board, type Me, type Member, type Message, ackBoard, follow, get, listTasks } from "./api";
import { Account } from "./account";
import { AskAnswers, askChanged } from "./ask-ui";
import { type BoardFacts, orderedAsks, worthALook } from "./asks";
import { Header, Problem } from "./chrome";
import { BoardNav } from "./sidebars";
import { SenderMark } from "./timeline";
import { identityOf, relativeTime } from "./words";

export default function Inbox({ onSignOut }: { onSignOut: () => void }) {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [asks, setAsks] = useState<Message[] | null>(null);
  const [facts, setFacts] = useState<BoardFacts[]>([]);
  const [me, setMe] = useState("");
  const [more, setMore] = useState(false);
  const [unavailable, setUnavailable] = useState(0);
  const [picked, setPicked] = useState<string | null>(null);
  const [sent, setSent] = useState<Message | null>(null);
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
  const current = asks?.find((a) => a.id === picked) ?? asks?.[0] ?? null;
  const index = current ? asks!.indexOf(current) : -1;
  const move = useCallback((n: number) => setPicked(asks?.[n]?.id ?? null), [asks]);
  const boardHref = current ? `/?board=${encodeURIComponent(current.board!)}&message=${encodeURIComponent(current.id)}` : "/";
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (!current || e.ctrlKey || e.metaKey || e.altKey || (e.target as HTMLElement)?.closest("input, textarea, select, [role=dialog], [role=menu]")) return;
      if (e.key === "j" || e.key === "ArrowDown") move(Math.min(asks!.length - 1, index + 1));
      else if (e.key === "k" || e.key === "ArrowUp") move(Math.max(0, index - 1));
      else if (e.key === "Enter" && e.target === document.body) window.location.href = boardHref;
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [asks, current, index, move, boardHref]);
  const notices = worthALook(facts, me, now);
  const markRead = (b: Board) => { void ackBoard(b.name, b.head_seq).then(() => refresh.current(), setError); };
  const answered = (reply: Message) => { setSent(reply); refresh.current(); };
  return <TooltipProvider delayDuration={250}><div className="inbox-page flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
    <Header account={<Account onSignOut={onSignOut} />} />
    <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[252px_minmax(280px,360px)_minmax(0,1fr)]">
      <aside aria-label="Boards" className="quiet-scroll order-3 border-t border-rule bg-sidebar px-5 py-4 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:border-r"><BoardNav current="" boards={boards} onMarkRead={markRead} /></aside>
      <section aria-labelledby="inbox-title" className="quiet-scroll order-1 flex flex-col gap-1 px-3 py-4 lg:order-none lg:overflow-y-auto lg:border-r lg:border-rule">
        <h1 id="inbox-title" className="flex flex-wrap items-baseline gap-x-2 px-2.5 pb-2 text-headline font-bold">Inbox<span className="text-meta font-normal text-muted">{asks && `${asks.length} ${asks.length === 1 ? "needs" : "need"} you`}</span></h1>
        {error !== null && <Problem error={error} />}
        {asks === null && error === null && <p role="status" className="px-2.5 text-muted">Loading asks…</p>}
        <h2 className="px-2.5 pt-2 pb-1 text-meta font-bold">Needs you</h2>
        {asks?.length === 0 && <p className="px-2.5 text-meta text-muted">Nothing waits on you.</p>}
        <ul aria-label="Needs you asks" className="flex flex-col gap-0.5">{asks?.map((m) => <li key={m.id}><button type="button" onClick={() => { setPicked(m.id); setSent(null); }} aria-current={m.id === current?.id ? "true" : undefined} className={cn("grid w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-2.5 rounded-control px-2.5 py-2.5 text-left hover:bg-selected", m.id === current?.id && "bg-selected")}>
          <SenderMark name={m.from.name} kind={m.from.kind} identity={identityOf(`${m.from.kind}:${m.from.name}`)} className="size-7" /><span className="flex min-w-0 flex-col"><span className="line-clamp-2 font-bold">{m.body}</span><span className="truncate text-meta text-muted">{boards?.find((b) => b.name === m.board)?.title ?? m.board} · {m.from.name} · {m.ask?.blocking ? "blocking" : "going with"}</span></span>
        </button></li>)}</ul>
        {more && <p className="px-2.5 text-meta text-muted">Showing the first {asks?.length ?? 0} asks.</p>}
        {notices.length > 0 && <><h2 className="px-2.5 pt-5 pb-1 text-meta font-bold text-muted">Worth a look</h2><ul aria-label="Worth a look" className="flex flex-col gap-0.5">{notices.map((n) => <li key={n.id}><a href={`/?board=${encodeURIComponent(n.board)}${n.task ? `&task=${encodeURIComponent(n.task)}` : ""}`} className="block rounded-control px-2.5 py-2.5 text-ink no-underline hover:bg-selected"><span className="block"><Clock className="mr-1.5 inline size-3.5 text-muted" aria-hidden />{n.text}</span><span className="text-meta text-muted">{n.title} · {n.detail}</span></a></li>)}</ul></>}
        {unavailable > 0 && <p className="px-2.5 pt-4 text-meta text-muted">Some board details could not be read. Refresh the Inbox to try again.</p>}
      </section>
      <section aria-label="The selected ask" className="quiet-scroll order-2 border-t border-rule px-4 py-6 sm:px-8 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:px-10 lg:py-8">
        {sent && <p role="status" className="mb-5 text-meta text-muted">Your answer was sent.</p>}
        {current ? <article className="flex max-w-[640px] flex-col gap-4" key={current.id}>
          <p className="text-meta text-muted"><a href={boardHref}>{boards?.find((b) => b.name === current.board)?.title ?? current.board}</a>{current.ask?.task && ` · ${current.ask.task}`}</p>
          <p className="flex items-center gap-2"><SenderMark name={current.from.name} kind={current.from.kind} identity={identityOf(`${current.from.kind}:${current.from.name}`)} /><strong>{current.from.name}</strong><span className="text-meta text-muted">{current.from.owner && `(${current.from.owner}'s agent) `}asks you · {relativeTime(current.at, now)}</span></p>
          <h2 className="text-headline font-bold whitespace-pre-wrap break-words">{current.body}</h2>
          <AskAnswers message={current} board={current.board!} keyboard onAnswered={answered} />
          <p className="flex flex-wrap items-center gap-x-4 gap-y-2 text-meta text-muted"><a href={boardHref} className="inline-flex items-center gap-1.5">Open on the board<CornerDownLeft className="size-3" aria-label="Enter" /></a><span className="inline-flex items-center gap-1"><ArrowDown className="size-3" aria-hidden /><ArrowUp className="size-3" aria-hidden />or j and k to move</span></p>
        </article> : asks !== null && <div className="max-w-[560px]"><h2 className="text-title font-bold">Nothing waits on you.</h2><p className="mt-2 text-muted">Asks addressed to you appear here from every board.</p></div>}
      </section>
    </div>
  </div></TooltipProvider>;
}
