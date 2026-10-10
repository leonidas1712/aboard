"use client";

// The Inbox's two "someone did this for you" items: you were added to a board, and
// someone you invited arrived. Joining is a copy-and-paste handoff: the board view
// attaches no agent. The server's join prompt goes into a new session, or into the
// resumed session of an agent you already have.

import { type ReactNode, useState } from "react";
import { Button } from "@/components/ui/button";
import { CopyButton } from "./board-details";
import { reopenCommand } from "./location";
import type { Member } from "./api";
import type { BoardAddNotice, InviteArrivalNotice } from "./onboarding-api";
import { AgentMark, Command, Fields } from "./onboarding-ui";
import { harnessName, relativeTime } from "./words";

export const addedKey = (a: BoardAddNotice) => `add:${a.board.id}:${a.added.seq}`;
export const arrivalKey = (a: InviteArrivalNotice) => `arrival:${a.invite_id}`;

/** addedTitle names the person who added you: an agent's owner, else the actor itself. */
export function addedTitle(a: BoardAddNotice): string {
  const who = a.added.by.owner ?? a.added.by.name;
  return `${who ? `@${who}` : "Someone"} added you to ${a.board.name}`;
}

export function arrivalTitle(a: InviteArrivalNotice): string {
  const first = a.boards[0];
  if (!first) return `@${a.handle} joined`;
  const agents = first.agents.map((x) => `@${x.name}`).join(", ");
  return `@${a.handle} joined ${first.board.name}${agents ? ` with ${agents}` : ""}${a.boards.length > 1 ? ` and ${a.boards.length - 1} more` : ""}`;
}

function Dismiss({ onDismiss }: { onDismiss: () => void }) {
  return (
    <div>
      <Button variant="quiet" onClick={onDismiss}>
        Dismiss
      </Button>
    </div>
  );
}

export function BoardAddDetail({ n, agents, now, onDismiss }: { n: BoardAddNotice; agents: Member[]; now: number; onDismiss: () => void }) {
  const [picking, setPicking] = useState(false);
  const mine = agents.filter((m) => !m.status || m.status === "active");
  return (
    <article className="ob-board-add flex max-w-[640px] flex-col gap-5" data-board-add={n.board.id}>
      <p className="text-meta text-muted">Added to a board · {relativeTime(n.added.at, now)}</p>
      <h2 className="text-headline font-bold break-words">{addedTitle(n)}</h2>
      <div className="flex flex-col gap-2">
        <p className="text-meta font-bold text-muted">Paste this into any agent session</p>
        <pre aria-label="Join prompt" className="rounded-control border border-rule bg-background px-3 py-2.5 font-sans text-body break-words whitespace-pre-wrap select-all">
          {n.join_prompt}
        </pre>
        <CopyButton text={n.join_prompt} label="Copy the join prompt" variant="primary" />
      </div>
      <div className="flex flex-col gap-3">
        <div>
          <Button variant="secondary" aria-expanded={picking} onClick={() => setPicking(!picking)}>
            Join with an agent you already have
          </Button>
        </div>
        {picking &&
          (mine.length === 0 ? (
            <p className="text-meta text-muted">You have no agents yet. Paste the prompt into a new session instead.</p>
          ) : (
            <ul aria-label="Your agents" className="ob-pick-agent flex flex-col gap-2">
              {mine.map((m) => {
                const reopen = m.location ? reopenCommand(m.location) : null;
                return (
                  <li key={`${m.board}:${m.id}`} data-agent={m.id} className="flex flex-col gap-2 rounded-box border border-rule px-3.5 py-3">
                    <p className="flex items-center gap-2">
                      <AgentMark name={m.name} harness={m.harness} />
                      <span className="min-w-0 truncate">
                        <span className="font-bold">{m.name}</span> · {harnessName(m.harness) ?? "agent"}
                        {m.board && <span className="text-muted"> · on {m.board}</span>}
                      </span>
                    </p>
                    {reopen ? <Command label="Resume its session" command={reopen} copyLabel={`Copy the resume command for ${m.name}`} /> : <p className="text-meta text-muted">No resume command is known for this agent. Open its session yourself.</p>}
                    <p className="text-meta text-muted">Resume it, then paste the join prompt into that session.</p>
                    <CopyButton text={n.join_prompt} label={`Copy the join prompt for ${m.name}`} variant="secondary" />
                  </li>
                );
              })}
            </ul>
          ))}
      </div>
      <Dismiss onDismiss={onDismiss} />
    </article>
  );
}

export function ArrivalDetail({ n, now, onDismiss }: { n: InviteArrivalNotice; now: number; onDismiss: () => void }) {
  return (
    <article className="ob-arrival flex max-w-[640px] flex-col gap-5" data-arrival={n.invite_id}>
      <p className="text-meta text-muted">Arrived · {relativeTime(n.at, now)}</p>
      <h2 className="text-headline font-bold break-words">{arrivalTitle(n)}</h2>
      <Fields
        rows={n.boards.map((b): [string, ReactNode] => [
          "Board",
          <span key={b.board.id} className="flex flex-col">
            <a href={`/?board=${encodeURIComponent(b.board.name)}`}>{b.board.title || b.board.name}</a>
            <span className="text-meta text-muted">{b.agents.length > 0 ? b.agents.map((a) => `${a.name} · ${harnessName(a.harness) ?? "agent"}`).join(", ") : "no agents yet"}</span>
          </span>,
        ])}
      />
      <Dismiss onDismiss={onDismiss} />
    </article>
  );
}
