"use client";

// EXPERIMENTAL, lab only: the Inbox with agent-driven onboarding in it. It keeps the real
// Inbox's shape (the boards, a list in groups, the selected item large; on a phone the
// list, and a tap reads one item with Back) and adds four kinds of item:
// - Needs you: approvals your agents asked for, and pairing requests to you that wait
//   for you to choose a session;
// - Pairing: requests under way or finished, yours and to you;
// - Invites your agents made: a notice per invite, with Revoke while it is open;
// - Decided: approvals already allowed, declined or expired, so what your agents did
//   for you stays one click away.
// ?item=ID opens one item.

import { ArrowLeft, Menu } from "lucide-react";
import { useEffect, useState } from "react";
import { Account } from "@/app/account";
import { type Board, get } from "@/app/api";
import { Header } from "@/app/chrome";
import { Sheet, useBackEntry, useWide } from "@/app/sheet";
import { relativeTime } from "@/app/words";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { at, scenario } from "../../store";
import { Mark, useNow } from "../common";
import { Nav } from "../nav";
import { ApprovalDetail } from "./approval";
import { groups, type Item } from "./model";
import { NoticeDetail, noticeLine } from "./notice";
import { PairingDetail, pairingRowLine } from "./pairing";
import { useOnboarding } from "./state";
import { wants } from "./words";

export function OnboardingInbox({ onSignOut }: { onSignOut: () => void }) {
  const o = useOnboarding();
  const now = useNow();
  const wide = useWide();
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [picked, setPicked] = useState<string | null>(() => new URLSearchParams(window.location.search).get("item"));
  const [reading, setReading] = useState(false);
  const [boardsOpen, setBoardsOpen] = useState(false);
  useBackEntry(!wide && reading, () => setReading(false));
  // An address naming an item opens it to read, after the first render, so the history
  // entry for Back is made once.
  useEffect(() => {
    if (new URLSearchParams(window.location.search).has("item")) setReading(true);
  }, []);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => setBoards(r.boards));
  }, []);
  const g = groups(o);
  const all = [...g.needs, ...g.pairing, ...g.invites, ...g.decided];
  const current = all.find((i) => i.id === picked) ?? g.needs[0] ?? all[0] ?? null;
  const read = (id: string) => {
    setPicked(id);
    if (!wide) {
      setReading(true);
      window.scrollTo({ top: 0 });
    }
  };
  const phoneReading = !wide && reading && current !== null;
  const nav = <Nav current={null} boards={boards} />;

  const row = (i: Item) => {
    const selected = wide && i.id === current?.id;
    const { who, title, meta, t } = rowOf(i, now, o.asked);
    return (
      <li key={i.id}>
        <button
          type="button"
          data-item={i.id}
          onClick={() => read(i.id)}
          aria-current={selected ? "true" : undefined}
          className={cn(
            "grid min-h-14 w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-3 rounded-control px-2.5 py-3 text-left transition-colors duration-[140ms] ease-out hover:bg-hover active:bg-selected",
            selected && "bg-selected hover:bg-selected",
          )}
        >
          <span className="mt-0.5">
            <Mark name={who} size="md" />
          </span>
          <span className="flex min-w-0 flex-col">
            <span className="flex min-w-0 items-baseline gap-3">
              <span className={cn("line-clamp-2 min-w-0 flex-1", selected && "font-bold")}>{title}</span>
              <span className="shrink-0 text-meta text-muted tabular-nums">{age(t, now)}</span>
            </span>
            <span className="truncate text-meta text-muted">{meta}</span>
          </span>
        </button>
      </li>
    );
  };
  const group = (title: string, list: Item[], first: boolean, count?: number) =>
    list.length > 0 && (
      <>
        <h2 className={cn("glass glass-page z-[1] flex items-baseline gap-2 px-2.5 pb-2 text-meta font-bold lg:sticky lg:top-0", first ? "pt-3" : "pt-7", !first && "text-muted")}>
          {title}
          {count !== undefined && count > 0 && (
            <span className="min-w-6 rounded-[6px] bg-attention px-1.5 text-center font-bold text-on-accent tabular-nums">{count}</span>
          )}
        </h2>
        <ul aria-label={title} className="flex flex-col gap-1">
          {list.map(row)}
        </ul>
      </>
    );

  return (
    <TooltipProvider delayDuration={250}>
      <div className="inbox-page ob-inbox flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
        <Header
          lead={
            !wide && (
              <button type="button" className="relative -ml-1.5 inline-flex size-11 shrink-0 items-center justify-center rounded-control text-ink transition-colors duration-[140ms] ease-out hover:bg-hover" aria-label="Boards" aria-haspopup="dialog" onClick={() => setBoardsOpen(true)}>
                <Menu className="size-5" strokeWidth={1.75} aria-hidden />
              </button>
            )
          }
          account={<Account onSignOut={onSignOut} />}
        />
        <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[252px_minmax(300px,380px)_minmax(0,1fr)]">
          {wide ? (
            <aside aria-label="Boards" className="quiet-scroll border-rule bg-sidebar px-5 py-4 lg:overflow-y-auto lg:border-r">
              {nav}
            </aside>
          ) : (
            <Sheet open={boardsOpen} onClose={() => setBoardsOpen(false)} side="left" title="Boards" back="Inbox">
              {nav}
            </Sheet>
          )}
          <section aria-labelledby="inbox-title" className={cn("quiet-scroll flex flex-col gap-1 px-3 pb-[max(1rem,env(safe-area-inset-bottom))] lg:overflow-y-auto lg:border-r lg:border-rule", phoneReading && "hidden")}>
            <h1 id="inbox-title" className="flex flex-wrap items-baseline gap-x-2 px-2.5 pt-6 pb-2 text-headline font-bold">
              Inbox
              <span className="text-meta font-normal text-muted tabular-nums">{g.needs.length === 0 ? "nothing waits on you" : `${g.needs.length} ${g.needs.length === 1 ? "needs" : "need"} you`}</span>
            </h1>
            {group("Needs you", g.needs, true)}
            {g.needs.length === 0 && <p className="px-2.5 pt-3 text-meta text-muted">Nothing waits on you.</p>}
            {group("Pairing", g.pairing, false)}
            {group("Invites your agents made", g.invites, false)}
            {group("Decided", g.decided, false)}
          </section>
          <section aria-label="The selected item" className={cn("quiet-scroll px-4 pt-3 pb-[max(1.5rem,env(safe-area-inset-bottom))] sm:px-8 lg:overflow-y-auto lg:px-10 lg:py-8", !wide && !phoneReading && "hidden")}>
            {!wide && (
              <button type="button" onClick={() => setReading(false)} className="inbox-back -ml-2.5 mb-2 inline-flex min-h-11 items-center gap-1.5 rounded-control px-2.5 text-link transition-colors duration-[140ms] ease-out hover:bg-hover">
                <ArrowLeft className="size-[18px]" strokeWidth={1.75} aria-hidden />
                Inbox
              </button>
            )}
            {current?.kind === "approval" && <ApprovalDetail key={current.id} a={current.a} onShowInvite={(id) => read(id)} />}
            {current?.kind === "pairing" && <PairingDetail key={current.id} p={current.p} />}
            {current?.kind === "notice" && <NoticeDetail key={current.id} n={current.n} />}
            {!current && (
              <div className="max-w-[560px]">
                <h2 className="text-title font-bold">Nothing waits on you.</h2>
                <p className="mt-2 text-muted">Approvals and pairing requests from your agents and your teammates appear here.</p>
              </div>
            )}
          </section>
        </div>
      </div>
    </TooltipProvider>
  );
}

/** rowOf is what an item's row says: whose mark, its first line, its second, and its time. */
function rowOf(i: Item, now: number, asked: Record<string, string>): { who: string; title: string; meta: string; t: number } {
  if (i.kind === "approval") {
    const a = i.a;
    const state =
      a.state === "pending"
        ? a.action.kind === "invite_people" || a.action.kind === "add_people"
          ? "asks you to approve"
          : "always asks you"
        : a.state === "executed"
          ? a.decided?.always
            ? "allowed always"
            : "allowed once"
          : a.state;
    return { who: a.agent, title: wants(a.agent, a.action), meta: `${a.board} · ${a.agent} · ${state}`, t: a.decided?.t ?? a.t };
  }
  if (i.kind === "notice") return { who: i.n.agent, title: `Your agent ${i.n.agent} invited someone`, meta: noticeLine(i.n, now), t: i.n.t };
  const p = i.p;
  const mine = p.inviter === scenario.me;
  const waiting = !mine && p.state === "awaiting_session" && !asked[p.id];
  return {
    who: mine ? p.agent : p.inviter,
    title: mine ? `You asked ${p.recipient} to pair on ${p.board}` : `${p.inviter} wants your agents to pair on ${p.board}`,
    meta: waiting ? `${p.board} · ${p.work}` : pairingRowLine(p, asked[p.id]),
    t: p.t,
  };
}

/** age is how long ago, short enough for a row: "4 min", "now". */
function age(t: number, now: number): string {
  const words = relativeTime(new Date(at(t)).toISOString(), now);
  return words === "just now" ? "now" : words.replace(/ ago$/, "");
}
