"use client";

// EXPERIMENTAL, lab only: the Inbox, the place for deciding. Every ask from every board
// that waits on the person, blocking ones first and then those an agent is "going with
// X unless you say", which block nothing,
// unless held after them; below, what is worth a look (late, idle, stale). The selected
// ask shows large, with its evidence and numbered answer buttons, and number keys
// answer it: 1, 2… for its options, the last for a reply in your own words. j and k, or
// the arrows, move between asks; Enter opens it on its board. An answer is an ordinary
// reply to the agent who asked. Home at scale: the lab opens here when there is an ask.

import { ArrowDown, ArrowUp, Clock, CornerDownLeft, FileText } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { type Board, get } from "@/app/api";
import { Account } from "@/app/account";
import { Header } from "@/app/chrome";
import { TooltipProvider } from "@/components/ui/tooltip";
import { relativeTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { labHref, useLab, useUi } from "../store";
import { type Ask, answer, asksOf, noticesOf } from "./asks";
import { Mark, useNow } from "./common";
import { Nav } from "./nav";

export function Inbox({ onSignOut }: { onSignOut: () => void }) {
  const { snap } = useLab();
  const { answered } = useUi();
  const now = useNow();
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [picked, setPicked] = useState<string | null>(null);
  const [sent, setSent] = useState<{ to: string; text: string } | null>(null);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => setBoards(r.boards));
  }, []);
  const asks = asksOf(snap, answered);
  const notices = noticesOf(snap, now);
  const current = asks.find((a) => a.id === picked) ?? asks[0] ?? null;
  const index = current ? asks.indexOf(current) : -1;

  const reply = useCallback(
    (a: Ask, text: string) => {
      setSent({ to: a.from, text });
      const next = asks[asks.indexOf(a) + 1] ?? asks[asks.indexOf(a) - 1] ?? null;
      setPicked(next?.id ?? null);
      void answer(a, text);
    },
    [asks],
  );

  return (
    <TooltipProvider delayDuration={250}>
      <div className="inbox-page flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
        <Header account={<Account onSignOut={onSignOut} />} />
        <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[272px_minmax(320px,400px)_minmax(0,1fr)]">
          <aside aria-label="Boards" className="quiet-scroll order-3 border-t border-rule bg-sidebar px-4 py-4 sm:px-6 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:border-r lg:px-5">
            <Nav current={null} boards={boards} />
          </aside>

          <section aria-labelledby="inbox-title" className="quiet-scroll order-1 flex flex-col gap-1 px-3 py-4 lg:order-none lg:overflow-y-auto lg:border-r lg:border-rule">
            <h1 id="inbox-title" className="flex items-baseline gap-2 px-2.5 pb-2 text-headline font-bold">
              Inbox
              <span className="text-meta font-normal text-muted">{asks.length === 0 ? "nothing waits on you" : `${asks.length} ${asks.length === 1 ? "needs" : "need"} you`}</span>
            </h1>
            <ul className="flex flex-col gap-0.5" aria-label="Waiting on you">
              {asks.map((a) => (
                <li key={a.id}>
                  <button
                    type="button"
                    onClick={() => setPicked(a.id)}
                    aria-current={a === current || undefined}
                    className={cn(
                      "grid w-full grid-cols-[28px_minmax(0,1fr)] items-start gap-x-2.5 rounded-control px-2.5 py-2.5 text-left transition-colors duration-[140ms] ease-out hover:bg-selected",
                      a === current && "bg-selected",
                    )}
                  >
                    <Mark name={a.from} size="md" />
                    <span className="flex min-w-0 flex-col">
                      <span className="truncate font-bold">{a.question}</span>
                      <span className="truncate text-meta text-muted">
                        {a.boardTitle} · {a.from} · {a.ahead ? `going with ${a.goingWith ?? "it"} unless you say` : relativeTime(new Date(a.at).toISOString(), now)}
                      </span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
            {notices.length > 0 && (
              <>
                <h2 className="px-2.5 pt-5 pb-1 text-meta font-bold text-muted">Worth a look</h2>
                <ul className="flex flex-col gap-0.5" aria-label="Worth a look">
                  {notices.map((n) => (
                    <li key={n.key}>
                      <a
                        href={labHref({ board: n.board, inbox: null, view: null, task: null, artifact: null })}
                        className="grid grid-cols-[28px_minmax(0,1fr)] items-start gap-x-2.5 rounded-control px-2.5 py-2.5 text-ink no-underline hover:bg-selected"
                      >
                        <Mark name={n.who} size="md" />
                        <span className="flex min-w-0 flex-col">
                          <span>
                            {n.late && <Clock className="mr-1.5 inline size-3.5 -translate-y-px text-muted" strokeWidth={2} aria-label="Late or idle" />}
                            {n.text}
                          </span>
                          <span className="text-meta text-muted">
                            {n.boardTitle} · {n.detail}
                          </span>
                        </span>
                      </a>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </section>

          <section aria-label="The selected ask" className="quiet-scroll order-2 border-t border-rule px-4 py-6 sm:px-8 lg:order-none lg:overflow-y-auto lg:border-t-0 lg:px-12 lg:py-10">
            {sent && (
              <p className="mb-6 text-meta text-muted" role="status" aria-live="polite">
                Sent to {sent.to}: &ldquo;{sent.text}&rdquo;
              </p>
            )}
            {current ? (
              <Detail key={current.id} a={current} index={index} count={asks.length} onPick={(i) => setPicked(asks[i]?.id ?? null)} onAnswer={reply} />
            ) : (
              <div className="flex max-w-[560px] flex-col gap-2">
                <h2 className="text-title font-bold">Nothing waits on you.</h2>
                <p className="text-muted">Asks from your agents land here, from every board, with their evidence and buttons to answer.</p>
              </div>
            )}
          </section>
        </div>
      </div>
    </TooltipProvider>
  );
}

function Detail({ a, index, count, onPick, onAnswer }: { a: Ask; index: number; count: number; onPick: (i: number) => void; onAnswer: (a: Ask, text: string) => void }) {
  const [writing, setWriting] = useState(false);
  const [text, setText] = useState("");
  const box = useRef<HTMLTextAreaElement>(null);
  const options = a.options;
  const boardHref = labHref({ board: a.board, inbox: null, view: null, artifact: null, task: a.task ?? null });
  useEffect(() => {
    const key = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement;
      if (e.metaKey || e.ctrlKey || e.altKey || el.closest("input, textarea, select, [role=dialog]")) return;
      const n = Number(e.key);
      if (n >= 1 && n <= options.length) onAnswer(a, options[n - 1]);
      else if (n === options.length + 1) setWriting(true);
      else if (e.key === "j" || e.key === "ArrowDown") onPick(Math.min(count - 1, index + 1));
      else if (e.key === "k" || e.key === "ArrowUp") onPick(Math.max(0, index - 1));
      else if (e.key === "Enter") window.location.href = boardHref;
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [a, options, index, count, onPick, onAnswer, boardHref]);
  useEffect(() => {
    if (writing) box.current?.focus();
  }, [writing]);
  return (
    <article className="ask-detail flex max-w-[640px] animate-fade-in flex-col gap-4">
      <p className="text-meta text-muted">
        <a href={labHref({ board: a.board, inbox: null, view: null, task: null, artifact: null })} className="text-muted">
          {a.boardTitle}
        </a>
        {a.task && (
          <>
            {" · "}
            <a href={boardHref} className="text-muted">
              {a.task}
            </a>
          </>
        )}
      </p>
      <p className="flex items-center gap-2">
        <Mark name={a.from} size="md" />
        <strong>{a.from}</strong>
        <span className="text-meta text-muted">{a.ahead ? `is going with ${a.goingWith ?? "it"} unless you say` : "asks you"}</span>
      </p>
      <h2 className="text-headline font-bold">{a.question}</h2>
      {a.body !== a.question && <p className="text-now">{a.body}</p>}
      {a.artifact && (
        <a
          href={labHref({ board: a.board, inbox: null, view: null, task: a.task ?? null, artifact: a.artifact.id ?? null })}
          className="file-tile flex max-w-[420px] items-center gap-3 rounded-control border border-rule bg-surface px-3.5 py-3 text-ink no-underline hover:border-field-border"
        >
          <FileText className="size-5 shrink-0 text-muted" strokeWidth={1.5} aria-hidden />
          <span className="flex min-w-0 flex-1 flex-col">
            <span className="font-bold">{a.artifact.name}</span>
            <span className="truncate text-meta text-muted">
              artifact by {a.from} · {a.artifact.summary}
            </span>
          </span>
          <span className="text-meta text-link">Open</span>
        </a>
      )}
      <ol className="flex flex-col gap-2" aria-label="Answers">
        {options.map((o, i) => (
          <li key={o}>
            <button
              type="button"
              onClick={() => onAnswer(a, o)}
              className="flex min-h-12 w-full items-center gap-3 rounded-control border border-rule bg-surface px-3.5 text-left transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected"
            >
              <kbd className="inline-flex size-6 shrink-0 items-center justify-center rounded-[5px] border border-rule font-sans text-meta text-muted">{i + 1}</kbd>
              {o}
            </button>
          </li>
        ))}
        <li>
          {writing ? (
            <form
              className="flex flex-col gap-2 rounded-control border border-field-border bg-surface p-2.5"
              onSubmit={(e) => {
                e.preventDefault();
                if (text.trim()) onAnswer(a, text.trim());
              }}
            >
              <label htmlFor="own-answer" className="text-meta text-muted">
                Your answer, sent to {a.from}
              </label>
              <textarea id="own-answer" ref={box} value={text} onChange={(e) => setText(e.target.value)} rows={3} className="w-full resize-y rounded-[6px] border border-field-border bg-surface px-2.5 py-1.5 text-ink" />
              <div className="flex gap-3">
                <button type="submit" disabled={!text.trim()} className="min-h-9 rounded-control bg-ink px-3.5 font-bold text-on-ink disabled:opacity-60">
                  Send to {a.from}
                </button>
                <button type="button" className="text-meta text-link hover:underline" onClick={() => setWriting(false)}>
                  Cancel
                </button>
              </div>
            </form>
          ) : (
            <button
              type="button"
              onClick={() => setWriting(true)}
              className="flex min-h-12 w-full items-center gap-3 rounded-control border border-rule bg-surface px-3.5 text-left transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected"
            >
              <kbd className="inline-flex size-6 shrink-0 items-center justify-center rounded-[5px] border border-rule font-sans text-meta text-muted">{options.length + 1}</kbd>
              Reply with something else
            </button>
          )}
        </li>
      </ol>
      <p className="flex flex-wrap items-center gap-x-4 gap-y-1 text-meta text-muted">
        <a href={boardHref} className="inline-flex items-center gap-1.5">
          Open in {a.boardTitle}
          <kbd className="inline-flex items-center rounded-[4px] border border-rule px-1 font-sans">
            <CornerDownLeft className="size-3" strokeWidth={1.75} aria-label="Enter" />
          </kbd>
        </a>
        <span className="inline-flex items-center gap-1">
          <ArrowDown className="size-3" strokeWidth={1.75} aria-hidden />
          <ArrowUp className="size-3" strokeWidth={1.75} aria-hidden />
          or j and k to move
        </span>
      </p>
    </article>
  );
}
