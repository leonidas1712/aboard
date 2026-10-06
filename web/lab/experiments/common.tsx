"use client";

// EXPERIMENTAL, lab only: helpers the mocked features share. Nothing here is in the API.

import { useEffect, useState } from "react";
import type { MemberRef } from "@/app/api";
import { SenderMark } from "@/app/timeline";
import { cn } from "@/lib/utils";
import { relativeTime } from "@/app/words";
import type { ScenarioTask } from "../scenario";
import { at, scenario, useLab } from "../store";

/** staleAfter is how old a now line may be, in minutes, before it is marked stale. */
export const staleAfter = scenario.staleAfter ?? 45;
/** briefStaleAfter is how old the brief may be before it is marked as maybe out of date. */
export const briefStaleAfter = staleAfter * 4;

/** useNow is the lab clock, read again every 30 seconds and at every step. */
export function useNow(): number {
  const { step } = useLab();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    setNow(Date.now());
    const tick = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(tick);
  }, [step]);
  return now;
}

/** ago says how long ago scenario minute t was, as the real UI says it. */
export function ago(t: number, now: number): string {
  return relativeTime(new Date(at(t)).toISOString(), now);
}

/** minutesSince is how many minutes ago scenario minute t was. */
export function minutesSince(t: number, now: number): number {
  return (now - at(t)) / 60_000;
}

/** onBoard is true for the scenario's own board, the only one with mocked features. */
export function onBoard(board: string): boolean {
  return board === scenario.board.name;
}

export const active = (t: ScenarioTask) => t.state === "claimed" || t.state === "working" || t.state === "waiting";

/** kindOf says whether a scenario name is a person or an agent. */
export function kindOf(name: string): "agent" | "human" {
  return scenario.people.some((p) => p.name === name) ? "human" : "agent";
}

/** Who is a member's name with a small sender mark, the timeline's one exception to "no avatars". */
export function Who({ name, identity, className }: { name: string; identity: (m: MemberRef) => number; className?: string }) {
  const kind = kindOf(name);
  const you = name === scenario.me;
  return (
    <span className={cn("inline-flex min-w-0 items-center gap-1.5 align-top", className)}>
      <SenderMark
        name={name}
        kind={kind}
        identity={identity({ name, kind, role: null, owner: null })}
        className="size-5 rounded-[5px] text-[10px]"
      />
      <span className="min-w-0 whitespace-nowrap">{you ? "you" : name}</span>
    </span>
  );
}
