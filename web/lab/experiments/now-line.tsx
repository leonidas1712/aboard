"use client";

// EXPERIMENTAL, lab only: an agent's "now" line, one sentence on what it is working on
// and when it said so, under its name wherever the board panel lists it. A line older
// than staleAfter turns muted with a clock mark and says "set … ago"; an agent with no
// line says so quietly. Fed by the scenario's steps, not the API.

import { History } from "lucide-react";
import type { Member } from "@/app/api";
import { exactTime } from "@/app/words";
import { cn } from "@/lib/utils";
import { at, useLab } from "../store";
import { ago, minutesSince, onBoard, staleAfter, useNow } from "./common";

export function NowLine({ board, agent }: { board: string; agent: Member }) {
  const { snap } = useLab();
  const now = useNow();
  if (!onBoard(board)) return null;
  const line = snap.now[agent.name];
  if (!line) {
    return (
      <p className="agent-now mt-0.5 text-meta text-muted" data-now="none">
        No now line
      </p>
    );
  }
  const stale = minutesSince(line.t, now) > staleAfter;
  const when = ago(line.t, now);
  return (
    <p
      className={cn("agent-now mt-0.5 animate-fade-in", stale && "text-muted")}
      data-now={stale ? "stale" : "fresh"}
      title={`${stale ? "Not updated in a while. " : ""}Set ${exactTime(new Date(at(line.t)).toISOString())}`}
      key={`${line.t}:${line.text}`}
    >
      {stale && <History className="mr-1 inline size-3.5 -translate-y-px" strokeWidth={1.75} aria-label="Not updated in a while" />}
      {line.text}
      <span className="text-meta whitespace-nowrap text-muted"> · {stale ? `set ${when}` : when}</span>
    </p>
  );
}
