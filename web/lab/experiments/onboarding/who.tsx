"use client";

// EXPERIMENTAL, lab only: how every onboarding card says which agent and which board, so
// an agent never reads as a person: the title names it as someone's agent with its
// harness mark, then an Agent field (name · harness · whose agent) and a Board field
// (where it asked, and when the change reaches the whole server, that it does).

import type { ReactNode } from "react";
import { harnessName } from "@/app/words";
import { scenario } from "../../store";
import { Mark } from "../common";

const harnessOf = (agent: string) => harnessName(scenario.agents.find((a) => a.name === agent)?.harness) ?? "agent";
const ownerOf = (agent: string, fallback: string) => scenario.agents.find((a) => a.name === agent)?.owner ?? fallback;

/** whoseAgent is "your agent" or "alex's agent". */
export const whoseAgent = (owner: string) => (owner === scenario.me ? "your agent" : `${owner}'s agent`);

/** AgentTitle is a card's title: "Your agent [mark] reviewer wants …", the mark being its harness. */
export function AgentTitle({ agent, rest, owner = scenario.me }: { agent: string; rest: string; owner?: string }) {
  const o = ownerOf(agent, owner);
  return (
    <h2 className="text-headline font-bold break-words">
      {o === scenario.me ? "Your agent" : `${o}'s agent`}{" "}
      <span className="inline-flex translate-y-[3px] align-baseline">
        <Mark name={agent} size="md" />
      </span>{" "}
      {agent} {rest}
    </h2>
  );
}

/** agentRow is the Agent field: "reviewer · Codex · your agent". */
export function agentRow(agent: string, owner: string): [string, ReactNode] {
  return ["Agent", `${agent} · ${harnessOf(agent)} · ${whoseAgent(ownerOf(agent, owner))}`];
}

/** boardRow is the Board field: where the agent asked, and a note when the change is server-wide. */
export function boardRow(board: string | null, wide: boolean): [string, ReactNode] {
  return [
    "Board",
    <span key="board" className="flex flex-col">
      <span>{board ?? "none"}</span>
      {wide && <span className="text-meta text-muted">This changes the whole server, not just this board.</span>}
    </span>,
  ];
}
