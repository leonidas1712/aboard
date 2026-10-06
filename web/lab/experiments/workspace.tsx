"use client";

// EXPERIMENTAL, lab only: the overview across many boards, above the list of boards,
// for a person with a team on ten boards and a hundred agents. It adds no new noun:
// the same facts, needs and briefs each board has, gathered up. It leads with what
// needs the person, across every board (blocking asks first, then agents going ahead
// unless told), then what is stuck, then what moved since they last looked, one line
// per board: its brief's summary, labelled with who wrote it and when, beside counted
// facts. Every line links into its board. Fed by the scenario, not the API.

import { ArrowRight } from "lucide-react";
import type { ReactNode } from "react";
import { count } from "@/app/words";
import { cn } from "@/lib/utils";
import type { Workspace } from "../scenario";
import { labHref, scenario, useLab } from "../store";

type Line = Workspace["boards"][number];

const minutes = (n: number) => (n < 60 ? `${n} min ago` : `${Math.floor(n / 60)} h ago`);

export function WorkspaceOverview() {
  const { snap } = useLab();
  const ws = scenario.workspace;
  if (!ws) return null;
  const boards = scenario.otherBoards ?? [];
  const agents = scenario.agents.length + boards.reduce((n, b) => n + (b.agents ?? 0), 0);
  const working = Object.values(snap.presence).filter((p) => p === "working").length + boards.reduce((n, b) => n + (b.working ?? 0), 0);
  const idle = Object.values(snap.presence).filter((p) => p === "idle").length + boards.reduce((n, b) => n + (b.idle ?? 0), 0);
  const needs = ws.boards.flatMap((b) => (b.needs ?? []).map((n) => ({ ...n, board: b })));
  const blocking = needs.filter((n) => n.kind === "asks").sort((a, b) => a.ago - b.ago);
  const ahead = needs.filter((n) => n.kind === "ahead");
  const stuck = ws.boards.flatMap((b) => (b.stuck ?? []).map((s) => ({ text: s, board: b })));
  const moving = ws.boards.filter((b) => b.moving).sort((a, b) => b.moving!.messages - a.moving!.messages);
  return (
    <section aria-labelledby="across" className="workspace mb-10 flex flex-col gap-6">
      <header className="flex flex-col gap-1.5">
        <h1 id="across" className="text-headline font-bold">
          Across your boards
        </h1>
        <Fact label="Facts" title="Counted from each board's record. Nobody wrote these words.">
          {count(ws.boards.length, "board", "boards")} · {agents} agents: {working} working, {idle} idle, {agents - working - idle} disconnected ·{" "}
          {blocking.length} {blocking.length === 1 ? "needs" : "need"} you · {stuck.length} stuck
        </Fact>
        <Fact label="Since" title="Since you last looked, across every board.">
          you last looked: {count(ws.sinceLooked.messages, "message", "messages")} · {count(ws.sinceLooked.tasksDone, "task done", "tasks done")} ·{" "}
          {count(ws.sinceLooked.decisions, "decision", "decisions")} · {count(ws.sinceLooked.artifacts, "file updated", "files updated")}
        </Fact>
      </header>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,3fr)_minmax(0,2fr)]">
        <Part title="Needs you" note={`${blocking.length} blocking · ${ahead.length} going ahead unless you say`}>
          <ul className="flex flex-col gap-2">
            {blocking.map((n, i) => (
              <li key={i} className="flex flex-col gap-0.5 rounded-box bg-attention px-3.5 py-2.5 text-ink">
                <p className="text-meta">
                  <BoardLink line={n.board} className="font-bold text-ink" /> · {n.from} asks you · {minutes(n.ago)}
                </p>
                <p className="line-clamp-2">{n.text}</p>
              </li>
            ))}
            {ahead.map((n, i) => (
              <li key={`a${i}`} className="flex flex-col gap-0.5 rounded-box border-2 border-[var(--attention)] bg-surface px-3.5 py-2.5">
                <p className="text-meta text-muted">
                  <BoardLink line={n.board} className="font-bold text-ink" /> · {n.from} goes ahead unless you say · {minutes(n.ago)}
                </p>
                <p className="line-clamp-2">{n.text}</p>
              </li>
            ))}
          </ul>
        </Part>
        <Part title="Stuck or stale" note={String(stuck.length)}>
          <ul className="divide-y divide-rule rounded-box border border-rule bg-surface">
            {stuck.map((s, i) => (
              <li key={i} className="flex flex-col px-3.5 py-2">
                <BoardLink line={s.board} className="text-meta font-bold text-ink" />
                <span>{s.text}</span>
              </li>
            ))}
          </ul>
        </Part>
      </div>

      <Part title="What moved" note="one line per board: its brief where it has one, and counted facts">
        <ul className="divide-y divide-rule rounded-box border border-rule bg-surface">
          {moving.map((b) => (
            <li key={b.name} className="grid gap-x-4 gap-y-0.5 px-3.5 py-2.5 md:grid-cols-[minmax(0,200px)_minmax(0,1fr)_auto]">
              <BoardLink line={b} className="font-bold text-ink" />
              <span className="min-w-0">
                {b.brief ? (
                  <>
                    <span className="line-clamp-2 md:line-clamp-1">{b.brief.summary}</span>
                    <span className="block text-meta text-muted">
                      Brief, by {b.brief.by}, {minutes(b.brief.ago)}
                    </span>
                  </>
                ) : (
                  <span className="text-meta text-muted">No brief yet</span>
                )}
              </span>
              <span className="text-meta whitespace-nowrap text-muted tabular-nums md:text-right">
                {count(b.moving!.messages, "message", "messages")}
                {b.moving!.tasksDone > 0 && <>, {count(b.moving!.tasksDone, "task done", "tasks done")}</>}
              </span>
            </li>
          ))}
        </ul>
      </Part>
    </section>
  );
}

function Fact({ label, title, children }: { label: string; title: string; children: ReactNode }) {
  return (
    <p className="flex gap-2 text-meta" title={title}>
      <span className="w-[44px] shrink-0 font-bold text-muted">{label}</span>
      <span className="min-w-0 text-ink">{children}</span>
    </p>
  );
}

function Part({ title, note, children }: { title: string; note?: string; children: ReactNode }) {
  return (
    <section aria-label={title} className="flex min-w-0 flex-col gap-2">
      <h2 className="flex flex-wrap items-baseline gap-x-2 text-meta font-bold text-muted">
        {title}
        {note && <span className="font-normal">· {note}</span>}
      </h2>
      {children}
    </section>
  );
}

function BoardLink({ line, className }: { line: Line; className?: string }) {
  return (
    <a href={labHref({ board: line.name, view: null })} className={cn("inline-flex items-center gap-1 no-underline hover:underline", className)}>
      {line.title}
      <ArrowRight className="size-3.5 text-muted" strokeWidth={1.75} aria-hidden />
    </a>
  );
}
