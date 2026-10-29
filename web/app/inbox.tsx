"use client";

import { ArrowLeft, Clock, CornerDownLeft, Menu } from "lucide-react";
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type AskList, type Board, type Me, type Member, type Message, type RepliesWaiting, ackBoard, follow, get, listTasks, repliesWaiting } from "./api";
import { Account } from "./account";
import { AskAnswers, askChanged } from "./ask-ui";
import { Attachments } from "./attachments";
import { type BoardFacts, askSummary, attentionCount, orderedAsks, worthALook } from "./asks";
import { Sheet, useBackEntry, useWide } from "./sheet";
import { StatusWord, agentStatus, blocksOf } from "./status";
import { Header, Problem } from "./chrome";
import { keyLabel, pressed, rememberInbox, takeInbox, typing } from "./keys";
import { Kbd, KeysSheet } from "./keys-sheet";
import { usePref } from "./prefs";
import { BoardNav, useSidebarKeys } from "./sidebars";
import { Finder } from "./finder";
import { SenderMark } from "./agent-mark";
import { andList, clockTime, identityOf, relativeTime } from "./words";
import { useOnboarding } from "./onboarding-data";
import { type OnboardingItem, OnboardingDetail, OnboardingRow, inviteItem, onboardingGroups } from "./inbox-onboarding";
import { ReplyDetail, ReplyRow } from "./inbox-replies";

export default function Inbox({ onSignOut }: { onSignOut: () => void }) {
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [asks, setAsks] = useState<Message[] | null>(null);
  // Messages waiting for the person's reply follow the asks; one replied to here leaves
  // the list at once, before the Inbox reads again.
  const [replies, setReplies] = useState<RepliesWaiting | null>(null);
  const [repliesFailed, setRepliesFailed] = useState(false);
  const [replied, setReplied] = useState<string[]>([]);
  const [facts, setFacts] = useState<BoardFacts[]>([]);
  const [me, setMe] = useState("");
  const [more, setMore] = useState(false);
  const [unavailable, setUnavailable] = useState(0);
  // Coming back from a board opened from here, the same ask is selected and the list is
  // where it was. If that ask was answered meanwhile, the one now at its place opens.
  const [returned] = useState(takeInbox);
  const [picked, setPicked] = useState<string | null>(returned?.id ?? null);
  // An onboarding item (an approval, an invite notice or a pairing request) picked in the
  // list takes the detail's place; ?item=ID opens one.
  // The Inbox has one stream; its events read onboarding again too.
  const ob = useOnboarding({ follows: false });
  const obRefresh = useRef(ob.refresh);
  obRefresh.current = ob.refresh;
  const [obPicked, setObPicked] = useState<string | null>(() => (typeof window === "undefined" ? null : new URLSearchParams(window.location.search).get("item")));
  const [askChosen, setAskChosen] = useState(returned !== null);
  // On a phone, an address naming an item opens it to read, once the page has drawn.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).has("item")) setReading(true);
  }, []);
  const [sent, setSent] = useState<{ to: string; body: string; at: number } | null>(null);
  const [fading, setFading] = useState(false);
  const [keysOpen, setKeysOpen] = useState(false);
  // On a phone the list and the selected ask take turns on the screen: a tap on an ask
  // reads it, Back or the back gesture returns to the list, and so does an answer.
  const wide = useWide();
  const [reading, setReading] = useState(false);
  const [boardsOpen, setBoardsOpen] = useState(false);
  useBackEntry(!wide && reading, () => setReading(false));
  // Board adds and arrivals are hidden by this browser only: the API has no per-person dismiss.
  const [dismissed, setDismissed] = usePref<Record<string, true>>("aboard.inbox.dismissed", {});
  const [snoozed, setSnoozed] = usePref<Record<string, number>>("aboard.inbox.snoozed", {});
  const slot = useRef(returned?.slot ?? 0);
  const listing = useRef<HTMLElement>(null);
  const [error, setError] = useState<unknown>(null);
  const [now, setNow] = useState(Date.now());
  const refresh = useRef<() => void>(() => {});
  useEffect(() => {
    let live = true;
    // A stream burst must not invalidate a read already in flight or launch another
    // copy of it. Each section publishes as soon as its own request answers.
    const failures = new Map<string, unknown>();
    const problem = (section: string, e?: unknown) => {
      if (!live) return;
      if (e === undefined) failures.delete(section); else failures.set(section, e);
      setError(failures.values().next().value ?? null);
    };
    const coalesce = (read: () => Promise<void>) => {
      let running = false;
      let pending = false;
      return () => {
        pending = true;
        if (running) return;
        running = true;
        void (async () => {
          try {
            while (live && pending) { pending = false; await read(); }
          } finally { running = false; }
        })();
      };
    };
    let currentBoards = new Map<string, Board>();
    const detailsPending = new Set<string>();
    const detailsRunning = new Set<string>();
    const detailsFailed = new Set<string>();
    const detailsLoaded = new Map<string, string>();
    const readDetails = () => {
      if (!live) return;
      for (const name of detailsPending) {
        if (detailsRunning.size >= 3) break;
        if (detailsRunning.has(name)) continue;
        detailsPending.delete(name);
        const board = currentBoards.get(name);
        if (!board?.on_board || board.lifecycle === "archived") continue;
        detailsRunning.add(name);
        void Promise.all([get<{ members: Member[] }>(`/v1/boards/${encodeURIComponent(name)}/members`), listTasks(name)]).then(([members, tasks]) => {
          if (!live || currentBoards.get(name)?.id !== board.id) return;
          detailsFailed.delete(name);
          detailsLoaded.set(name, board.id);
          setFacts((previous) => {
            const facts = new Map(previous.map((f) => [f.board.id, f]));
            facts.set(board.id, { board, members: members.members, tasks: tasks.tasks });
            return [...currentBoards.values()].flatMap((b) => facts.get(b.id) ?? []);
          });
        }, () => {
          if (!live || currentBoards.get(name)?.id !== board.id) return;
          detailsFailed.add(name);
          setFacts((previous) => previous.filter((f) => f.board.id !== board.id));
        }).finally(() => {
          detailsRunning.delete(name);
          if (!live) return;
          setUnavailable(detailsFailed.size);
          readDetails();
        });
      }
    };
    const refreshDetails = (name: string) => { detailsPending.add(name); readDetails(); };
    const loadBoards = coalesce(async () => {
      try {
        const list = await get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" });
        if (!live) return;
        setBoards(list.boards);
        currentBoards = new Map(list.boards.filter((b) => b.on_board && b.lifecycle !== "archived").map((b) => [b.name, b]));
        setFacts((previous) => previous.filter((f) => currentBoards.get(f.board.name)?.id === f.board.id));
        for (const name of detailsFailed) if (!currentBoards.has(name)) detailsFailed.delete(name);
        setUnavailable(detailsFailed.size);
        for (const [name, board] of currentBoards) if (detailsLoaded.get(name) !== board.id) detailsPending.add(name);
        readDetails();
        problem("boards");
      } catch (e) { problem("boards", e); }
    });
    const loadAsks = coalesce(async () => {
      try {
        const questions = await get<AskList>("/v1/asks", { to_me: true, state: "open", limit: 200 });
        if (!live) return;
        setAsks(orderedAsks(questions.asks));
        setMore(questions.more);
        problem("asks");
      } catch (e) { problem("asks", e); }
    });
    const loadReplies = coalesce(async () => {
      try {
        const waitingReplies = await repliesWaiting(50);
        if (!live) return;
        setReplies(waitingReplies); setReplied([]); setRepliesFailed(false);
      } catch { if (live) setRepliesFailed(true); }
    });
    const loadPerson = coalesce(async () => {
      try {
        const person = await get<Me>("/v1/me");
        if (live) setMe(person.name);
        problem("person");
      } catch (e) { problem("person", e); }
    });
    const load = () => { loadAsks(); loadReplies(); loadBoards(); loadPerson(); };
    refresh.current = load;
    load();
    const stop = follow({
      open: () => problem("stream"),
      head: (board) => { loadAsks(); loadReplies(); loadBoards(); refreshDetails(board); obRefresh.current(); },
      presence: (p) => refreshDetails(p.board),
      unavailable: () => { load(); obRefresh.current(); },
      error: (e) => problem("stream", e),
    });
    const changed = () => load();
    window.addEventListener(askChanged, changed);
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => { live = false; stop(); clearInterval(tick); window.removeEventListener(askChanged, changed); };
  }, []);
  // Snoozing is kept by this browser only: the API has no per-person snooze yet.
  const waiting = asks?.filter((a) => !(snoozed[a.id] > now)) ?? null;
  const resting = (asks?.length ?? 0) - (waiting?.length ?? 0);
  // Answering or snoozing an ask advances in place: the ask now at its index opens.
  const replyList = replies?.messages.filter((m) => !replied.includes(m.id)) ?? [];
  // J and K move through the asks, then the replies waiting, in the order listed.
  const items = waiting === null ? null : [...waiting, ...replyList];
  // A saved reply may arrive after the asks; keep its selection until that read settles.
  const returning = picked === returned?.id && replies === null && !repliesFailed && !items?.some((item) => item.id === picked);
  const current = returning ? null : items?.find((a) => a.id === picked) ?? items?.[Math.min(slot.current, items.length - 1)] ?? null;
  const index = current ? items!.indexOf(current) : -1;
  const obGroups = onboardingGroups(ob, dismissed);
  const obAll = [...obGroups.needs, ...obGroups.news, ...obGroups.pairing, ...obGroups.invites, ...obGroups.decided];
  // What waits on the person opens first: an approval or request before an ask, unless an ask was picked.
  const obItem = obAll.find((i) => i.id === obPicked) ?? (!askChosen && (obGroups.needs.length > 0 || (items?.length ?? 0) === 0) ? (obGroups.needs[0] ?? (items?.length === 0 ? obAll[0] : undefined) ?? null) : null);
  // The item on screen stays there when deciding it moves it to another group.
  useEffect(() => {
    if (obItem && obPicked !== obItem.id) setObPicked(obItem.id);
  }, [obItem, obPicked]);
  useEffect(() => {
    if (!current) return;
    slot.current = index;
    if (current.id !== picked) setPicked(current.id);
  }, [current, index, picked]);
  const restored = useRef(false);
  useEffect(() => {
    if (!returned || restored.current || items === null || returning) return;
    restored.current = true;
    if (listing.current && listing.current.scrollHeight > listing.current.clientHeight) listing.current.scrollTop = returned.scroll;
    else window.scrollTo({ top: returned.scroll });
  }, [returned, items, returning]);
  useEffect(() => {
    if (current) document.querySelector(`[data-ask="${CSS.escape(current.id)}"], [data-reply="${CSS.escape(current.id)}"]`)?.scrollIntoView({ block: "nearest" });
  }, [current]);
  useEffect(() => {
    if (!sent) return;
    setFading(false);
    const fade = setTimeout(() => setFading(true), 4000);
    const gone = setTimeout(() => setSent(null), 4200);
    return () => { clearTimeout(fade); clearTimeout(gone); };
  }, [sent]);
  const move = useCallback((n: number) => { setPicked(items?.[n]?.id ?? null); setSent(null); }, [items]);
  const readItem = useCallback((m: Message) => { setPicked(m.id); setObPicked(null); setAskChosen(true); setSent(null); }, []);
  const obNeeds = obGroups.needs;
  const snooze = useCallback((m: Message) => {
    const at = Date.now();
    const kept = Object.fromEntries(Object.entries(snoozed).filter(([, until]) => until > at));
    setSnoozed({ ...kept, [m.id]: at + 3_600_000 });
    setPicked(null);
    setNow(at);
  }, [snoozed, setSnoozed]);
  const boardHref = current ? `/?board=${encodeURIComponent(current.board!)}&message=${encodeURIComponent(current.id)}&seq=${current.seq}&from=inbox` : "/";
  // Opening a board keeps what is selected and how far the list is scrolled, for the way back.
  const leave = useCallback(() => {
    if (!current) return;
    const list = listing.current;
    rememberInbox({ id: current.id, slot: index, scroll: list && list.scrollHeight > list.clientHeight ? list.scrollTop : window.scrollY });
  }, [current, index]);
  // A layout effect, so the keys work from the first painted frame: a passive effect can
  // run after the paint, and a key pressed in between would be dropped.
  useLayoutEffect(() => {
    const key = (e: KeyboardEvent) => {
      if (typing(e)) return;
      if (pressed("help", e)) setKeysOpen(true);
      else if (obItem && items?.length) {
        // From an approval or request, J and K walk back into the list: the Needs you
        // items sit above the asks, the rest below the replies waiting.
        const above = obNeeds.some((i) => i.id === obItem.id);
        if (above && pressed("next", e)) readItem(items[0]);
        else if (!above && pressed("previous", e)) readItem(items[items.length - 1]);
        else return;
      }
      else if (!current || obItem) return;
      else if (pressed("next", e)) move(Math.min(items!.length - 1, index + 1));
      else if (pressed("previous", e)) move(Math.max(0, index - 1));
      else if (pressed("snooze", e) && current.ask) snooze(current);
      else if (pressed("open", e) && (e.target === document.body || (e.target as HTMLElement).closest("[data-ask], [data-reply]"))) { leave(); window.location.href = boardHref; }
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [items, current, index, move, snooze, boardHref, leave, obItem, obNeeds, readItem]);
  const notices = worthALook(facts, me, now);
  const working = facts.map((f) => ({ board: f.board, agents: f.members.filter((m) => m.kind === "agent" && (!m.status || m.status === "active") && m.presence === "working").length })).filter((f) => f.agents > 0);
  const busiest = working.reduce<(typeof working)[number] | null>((a, b) => (a && a.agents >= b.agents ? a : b), null);
  const workingCount = working.reduce((n, f) => n + f.agents, 0);
  const markRead = (b: Board) => { void ackBoard(b.name, b.head_seq).then(() => refresh.current(), setError); };
  const answered = (reply: Message) => { if (current) setSent({ to: `@${current.from.name}`, body: reply.body, at: Date.now() }); setPicked(null); setReading(false); refresh.current(); };
  // The sent line names whom the server sent the reply to.
  const sentReply = (reply: Message) => { if (current) { setSent({ to: andList(reply.to.length > 0 ? reply.to : [`@${current.from.name}`]), body: reply.body, at: Date.now() }); setReplied((r) => [...r, current.id]); } setPicked(null); setReading(false); refresh.current(); };
  const read = (m: Message) => { setPicked(m.id); setObPicked(null); setAskChosen(true); setSent(null); if (!wide) { setReading(true); window.scrollTo({ top: 0 }); } };
  const readOb = (id: string) => { setObPicked(id); setAskChosen(false); setSent(null); if (!wide) { setReading(true); window.scrollTo({ top: 0 }); } };
  // The asker's status on its board, from the same facts Worth a look reads.
  const askerStatus = (m: Message) => {
    if (m.from.kind !== "agent") return null;
    const f = facts.find((x) => x.board.name === m.board);
    const agent = f?.members.find((x) => x.kind === "agent" && x.name === m.from.name);
    return f && agent ? agentStatus(agent, blocksOf(f.tasks).get(agent.name), me) : null;
  };
  // Whether name is on the board, by the facts read; unknown until they are read.
  const onBoardOf = (board: string | undefined, name: string) => {
    const f = facts.find((x) => x.board.name === board);
    return !f || f.members.some((x) => x.name === name && (!x.status || x.status === "active"));
  };
  const phoneReading = !wide && reading && (current !== null || obItem !== null);
  const boardTitle = (name: string | null | undefined) => boards?.find((b) => b.name === name)?.title ?? name;
  const blocking = waiting?.filter((m) => m.ask?.blocking) ?? [];
  const going = waiting?.filter((m) => !m.ask?.blocking) ?? [];
  const row = (m: Message) => {
    // On a phone no row is selected: a tap reads the ask on its own.
    const selected = wide && !obItem && m.id === current?.id;
    const ask = m.ask!;
    return <li key={m.id}><button type="button" data-ask={m.id} onClick={() => read(m)} aria-current={selected ? "true" : undefined} className={cn("grid min-h-14 w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-3 rounded-control px-2.5 py-3 text-left transition-colors duration-[140ms] ease-out hover:bg-hover active:bg-selected", selected && "bg-selected hover:bg-selected")}>
      <SenderMark name={m.from.name} kind={m.from.kind} harness={m.from.harness} identity={identityOf(`${m.from.kind}:${m.from.name}`)} className="mt-0.5 size-7" />
      <span className="flex min-w-0 flex-col">
        <span className="flex min-w-0 items-baseline gap-3"><span className={cn("min-w-0 flex-1 truncate", selected && "font-bold")}>{askSummary(m.body)}</span><span className="shrink-0 text-meta text-muted tabular-nums">{age(m.at, now)}</span></span>
        <span className="truncate text-meta text-muted">{boardTitle(m.board)} · {m.from.name} · {ask.blocking ? "blocking" : `going with ${ask.going_with}${ask.going_at ? ` at ${clockTime(ask.going_at)}` : ""}`}</span>
      </span>
    </button></li>;
  };
  // The headings stay in place while the list scrolls under them.
  const group = (title: string, label: string, list: Message[], first: boolean) => <>
    <h2 className={cn("glass glass-page z-[1] px-2.5 lg:sticky lg:top-0 pb-2 text-meta font-bold", first ? "pt-3" : "pt-7")}>{title}</h2>
    <ul aria-label={label} className="flex flex-col gap-1">{list.map(row)}</ul>
  </>;
  const obGroup = (title: string, list: OnboardingItem[], first: boolean) => list.length > 0 && <>
    <h2 className={cn("glass glass-page z-[1] px-2.5 lg:sticky lg:top-0 pb-2 text-meta font-bold", first ? "pt-3" : "pt-7 text-muted")}>{title}</h2>
    <ul aria-label={title} className="flex flex-col gap-1">{list.map((i) => <OnboardingRow key={i.id} item={i} ob={ob} now={now} selected={wide && obItem?.id === i.id} onRead={() => readOb(i.id)} />)}</ul>
  </>;
  const rest = current ? askDetail(current.body) : "";
  const moveKeys = <>
    <span className="inline-flex items-center gap-1.5 pointer-coarse:hidden"><Kbd>{keyLabel("next")}</Kbd><Kbd>{keyLabel("previous")}</Kbd>to move</span>
    <button type="button" className="inline-flex min-h-11 items-center gap-1.5 text-link hover:underline pointer-coarse:hidden" onClick={() => setKeysOpen(true)}><Kbd>{keyLabel("help")}</Kbd>All keys</button>
  </>;
  const senderStatus = current && !current.ask ? (() => { const status = askerStatus(current); return status && <StatusWord status={status} dot className="asker-status" />; })() : null;
  const sentLine = sent && <p role="status" key={sent.at} className={cn("max-w-[640px] truncate text-meta text-muted", wide ? "mb-5" : "px-2.5 pb-1", fading ? "animate-fade-out" : "animate-fade-in")}>Sent to {sent.to}: &ldquo;{sent.body}&rdquo;</p>;
  useSidebarKeys("", boards);
  const nav = <BoardNav current="" boards={boards} onMarkRead={markRead} />;
  const boardsWaiting = (boards ?? []).some((b) => attentionCount(b) > 0);
  return <TooltipProvider delayDuration={250}><div className="inbox-page flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
    <Header
      lead={!wide && <button type="button" className="relative -ml-1.5 inline-flex size-11 shrink-0 items-center justify-center rounded-control text-ink transition-colors duration-[140ms] ease-out hover:bg-hover" aria-label="Boards" aria-haspopup="dialog" onClick={() => setBoardsOpen(true)}>
        <Menu className="size-5" strokeWidth={1.75} aria-hidden />
        {boardsWaiting && <span aria-hidden className="absolute top-2 right-2 size-2.5 rounded-full border-2 border-surface bg-status-needs" />}
      </button>}
      account={<Account onSignOut={onSignOut} onHelp={() => setKeysOpen(true)} />}
    />
    <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[252px_minmax(280px,360px)_minmax(0,1fr)]">
      {wide ? <aside aria-label="Boards" className="quiet-scroll border-rule bg-sidebar px-5 py-4 lg:overflow-y-auto lg:border-r">{nav}</aside>
        : <Sheet open={boardsOpen} onClose={() => setBoardsOpen(false)} side="left" title="Boards" back="Inbox">{nav}</Sheet>}
      <section ref={listing} aria-labelledby="inbox-title" className={cn("quiet-scroll flex flex-col gap-1 px-3 pb-[max(1rem,env(safe-area-inset-bottom))] lg:overflow-y-auto lg:border-r lg:border-rule", phoneReading && "hidden")}>
        <h1 id="inbox-title" className="flex flex-wrap items-baseline gap-x-2 px-2.5 pt-6 pb-2 text-headline font-bold">Inbox<span className="text-meta font-normal text-muted tabular-nums">{items && `${items.length + obGroups.needs.length} ${items.length + obGroups.needs.length === 1 ? "needs" : "need"} you`}</span></h1>
        {!wide && sentLine}
        {error !== null && <Problem error={error} />}
        {asks === null && error === null && <p role="status" className="px-2.5 text-muted">Loading asks…</p>}
        {obGroups.needs.length > 0 && <div role="group" aria-label="Needs you approvals and requests" className="flex flex-col gap-1">{obGroup("Needs you", obGroups.needs, true)}</div>}
        <div role="group" aria-label="Needs you asks" className="flex flex-col gap-1">
          {blocking.length > 0 && going.length > 0 ? <>{group("Blocking", "Blocking asks", blocking, obGroups.needs.length === 0)}{group("Going ahead unless you say", "Asks going ahead unless you say", going, false)}</> : (waiting?.length ?? 0) > 0 || (obGroups.needs.length === 0 && replyList.length === 0) ? group(obGroups.needs.length > 0 ? "Asks" : "Needs you", "Asks", waiting ?? [], obGroups.needs.length === 0) : null}
        </div>
        {replyList.length > 0 && <div role="group" aria-label="Replies waiting" className="flex flex-col gap-1">
          <h2 className={cn("glass glass-page z-[1] px-2.5 lg:sticky lg:top-0 pb-2 text-meta font-bold", obGroups.needs.length === 0 && (waiting?.length ?? 0) === 0 ? "pt-3" : "pt-7")}>Replies waiting</h2>
          <ul aria-label="Messages waiting for your reply" className="flex flex-col gap-1">{replyList.map((m) => <ReplyRow key={m.id} m={m} board={boardTitle(m.board)} age={age(m.at, now)} selected={wide && !obItem && m.id === current?.id} onRead={() => read(m)} />)}</ul>
        </div>}
        {replies?.more && <p className="px-2.5 pt-2 text-meta text-muted tabular-nums">Showing the newest {replies.messages.length} of {replies.count} replies waiting.</p>}
        {repliesFailed && <p className="px-2.5 pt-2 text-meta text-muted">Replies waiting could not be read. They show again once the server answers.</p>}
        {waiting?.length === 0 && obGroups.needs.length === 0 && replyList.length === 0 && <p className="px-2.5 text-meta text-muted">Nothing waits on you.</p>}
        {resting > 0 && <p className="px-2.5 pt-2 text-meta text-muted tabular-nums">{resting} snoozed on this browser · <button type="button" className="text-link hover:underline" onClick={() => setSnoozed({})}>Show {resting === 1 ? "it" : "them"} now</button></p>}
        {more && <p className="px-2.5 text-meta text-muted">Showing the first {asks?.length ?? 0} asks.</p>}
        {obGroup("Added and arrived", obGroups.news, false)}
        {obGroup("Pairing", obGroups.pairing, false)}
        {obGroup("Invites your agents made", obGroups.invites, false)}
        {obGroup("Decided", obGroups.decided, false)}
        {ob.error !== null && <p className="px-2.5 pt-4 text-meta text-muted">Approvals and pairing requests could not be read. They show again once the server answers.</p>}
        {notices.length > 0 && <><h2 className="px-2.5 pt-7 pb-2 text-meta font-bold text-muted">Worth a look</h2><ul aria-label="Worth a look" className="flex flex-col gap-1">{notices.map((n) => <li key={n.id}><a href={`/?board=${encodeURIComponent(n.board)}${n.task ? `&task=${encodeURIComponent(n.task)}` : ""}&from=inbox`} onClick={leave} className="block rounded-control px-2.5 py-3 text-ink no-underline transition-colors duration-[140ms] ease-out hover:bg-hover"><span className="block"><Clock className="mr-1.5 inline size-3.5 text-muted" aria-hidden />{n.text}</span><span className="text-meta text-muted">{n.title} · {n.detail}</span></a></li>)}</ul></>}
        {unavailable > 0 && <p className="px-2.5 pt-4 text-meta text-muted">Some board details could not be read. Refresh the Inbox to try again.</p>}
      </section>
      <section aria-label="The selected ask" className={cn("quiet-scroll px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-8 lg:overflow-y-auto lg:px-10 lg:py-8", !wide && !phoneReading && "hidden")}>
        {wide && sentLine}
        {!wide && <button type="button" onClick={() => setReading(false)} className="inbox-back -ml-2.5 mb-2 inline-flex min-h-11 items-center gap-1.5 rounded-control px-2.5 text-link transition-colors duration-[140ms] ease-out hover:bg-hover"><ArrowLeft className="size-[18px]" strokeWidth={1.75} aria-hidden />Inbox</button>}
        {obItem ? <OnboardingDetail key={obItem.id} item={obItem} ob={ob} now={now} onShowInvite={(id) => readOb(inviteItem(obGroups, id))} onDismiss={(id) => { setDismissed({ ...dismissed, [id]: true }); setReading(false); }} /> : current && !current.ask ? <ReplyDetail key={current.id} m={current} me={me} onBoard={(name) => onBoardOf(current.board, name)} boardTitle={boardTitle(current.board) ?? ""} boardHref={boardHref} status={senderStatus} now={now} onLeave={leave} onSent={sentReply} keys={moveKeys} /> : current ? <article className="flex max-w-[640px] flex-col gap-5" key={current.id}>
          <p className="text-meta text-muted"><a href={boardHref} onClick={leave} className="tap">{boardTitle(current.board)}</a>{current.ask?.task && ` · ${current.ask.task.ref} ${current.ask.task.title}`}</p>
          {(() => {
            const status = askerStatus(current);
            return <p className="flex flex-wrap items-center gap-x-2 gap-y-1"><SenderMark name={current.from.name} kind={current.from.kind} harness={current.from.harness} identity={identityOf(`${current.from.kind}:${current.from.name}`)} /><strong>{current.from.name}</strong><span className="text-meta text-muted">{current.from.owner && `(${current.from.owner}'s agent) `}asks you · {relativeTime(current.at, now)}</span>{status && <StatusWord status={status} dot className="asker-status" />}</p>;
          })()}
          <div className="flex flex-col gap-2"><h2 className="text-headline font-bold break-words">{askSummary(current.body)}</h2>{rest && <p className="text-now whitespace-pre-wrap break-words">{rest}</p>}<Attachments board={current.board!} files={current.files} /></div>
          <AskAnswers message={current} board={current.board!} keyboard onAnswered={answered} />
          <p className="flex flex-wrap items-center gap-x-5 gap-y-1 text-meta text-muted">
            <a href={boardHref} onClick={leave} className="inline-flex min-h-11 items-center gap-1.5">Open on the board<CornerDownLeft className="size-3 pointer-coarse:hidden" aria-label="Enter" /></a>
            <button type="button" className="inline-flex min-h-11 items-center gap-1.5 text-link hover:underline" onClick={() => { snooze(current); setReading(false); }}>Snooze for an hour<span className="max-sm:hidden"> on this browser</span><Kbd>{keyLabel("snooze")}</Kbd></button>
            {moveKeys}
          </p>
        </article> : asks !== null && <div className="max-w-[560px]"><h2 className="text-title font-bold">Nothing waits on you.</h2><p className="mt-2 text-muted">Asks addressed to you, and messages waiting for your reply, appear here from every board.{busiest && <> · <a href={`/?board=${encodeURIComponent(busiest.board.name)}`} className="tabular-nums" title={`Open ${busiest.board.title ?? busiest.board.name}, where most of them are`}>{workingCount} {workingCount === 1 ? "agent" : "agents"} working on {working.length} {working.length === 1 ? "board" : "boards"}</a></>}</p></div>}
      </section>
    </div>
    <KeysSheet open={keysOpen} onClose={() => setKeysOpen(false)} />
    <Finder boards={boards} page="inbox" onKeys={() => setKeysOpen(true)} />
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
