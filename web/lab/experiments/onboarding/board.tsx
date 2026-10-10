"use client";

// EXPERIMENTAL, lab only: onboarding on the board itself.
// - PairingLine: one quiet line under the "Now:" line while a pairing request on this
//   board is under way, and once it is ready: "Waiting for sam's agent to come online",
//   "Verifying delivery with sam's agent…", then "Ready: both agents connected, delivery
//   verified". It never says verified before the server does; a link opens the request.
// - eventLine: how the record reads a join an agent did for its person, from the event's
//   data.authorization: "sam joined as member · invited by writer, approved by alex", or
//   "… added by writer, on alex's allowance".
// - AccountItem: Settings in the account menu, saying whether auto mode is on.

import { Check, Clock, LoaderCircle, Settings2 } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import type { BoardEvent } from "@/app/api";
import { DropdownMenuItem, DropdownMenuSeparator } from "@/components/ui/dropdown-menu";
import { labHref, scenario } from "../../store";
import { Centre } from "../centre";
import { useOnboarding } from "./state";
import { byline, pairingLine } from "./words";

export function PairingLine({ board }: { board: string }) {
  const o = useOnboarding();
  const p = o.pairing
    .filter((x) => x.board === board && (x.inviter === scenario.me || x.recipient === scenario.me) && x.state !== "declined" && x.state !== "cancelled" && x.state !== "expired")
    .sort((a, b) => b.t - a.t)[0];
  if (!p) return null;
  const ready = p.state === "ready";
  const turning = p.state === "verifying";
  const icon = ready ? (
    <Check className="size-4 text-accent-strong" strokeWidth={2} aria-hidden />
  ) : turning ? (
    <LoaderCircle className="size-4 text-muted motion-safe:animate-spin motion-safe:[animation-duration:2.4s]" strokeWidth={1.75} aria-hidden />
  ) : (
    <Clock className="size-4 text-muted" strokeWidth={1.75} aria-hidden />
  );
  return (
    <div className="mx-auto w-full max-w-[848px] px-4 sm:px-6">
      <p role="status" aria-live="polite" className="ob-pairing-line flex flex-wrap items-center gap-x-2 gap-y-0.5 py-1.5 text-meta" data-state={p.state}>
        <span className="inline-flex items-center gap-2">
          {icon}
          <span className={ready ? "text-ink" : "text-muted"}>{pairingLine(p, o.asked[p.id])}</span>
        </span>
        <span className="text-faint max-sm:hidden" aria-hidden>
          ·
        </span>
        <a href={labHref({ inbox: "1", board: null, item: p.id, view: null, task: null, artifact: null })} className="text-muted">
          Pairing request
        </a>
      </p>
    </div>
  );
}

/** OnboardingCentre is the lab's centre with the pairing line on top, right under "Now:". */
export function OnboardingCentre(props: ComponentProps<typeof Centre>) {
  return (
    <>
      <PairingLine board={props.board} />
      <Centre {...props} />
    </>
  );
}

const nameOf = (id: unknown): string => {
  const s = String(id ?? "");
  if (s.startsWith("per_")) return s.slice(4);
  const m = s.match(/^mem_[^_]+(?:-[^_]+)*_(.+)$/);
  return m ? m[1] : s;
};

/** eventLine reads a member.joined that carries data.authorization; null leaves the line to the real UI. */
export function eventLine(e: BoardEvent): string | null {
  const d = (e.data ?? {}) as Record<string, unknown>;
  const a = d.authorization as { person_id?: string; agent_id?: string; via?: "allowance" | "approval" } | undefined;
  if (e.type !== "member.joined" || !a?.via) return null;
  // The authorization doesn't say whether the person came with an invite or was added;
  // the lab knows from its scenario. The product would need the event to say.
  const verb = scenario.people.find((p) => p.name === d.name)?.authorization?.as ?? "added";
  return `${String(d.name)} joined as ${d.access === "admin" ? "admin" : "member"} · ${byline(verb, nameOf(a.agent_id), nameOf(a.person_id), a.via)}`;
}

export function AccountItem(): ReactNode {
  const o = useOnboarding();
  const on = o.allowance.length > 0;
  return (
    <>
      <DropdownMenuItem asChild>
        <a href={labHref({ settings: "1", inbox: null, board: null, item: null, view: null, task: null, artifact: null })} className="ob-settings-item flex items-center gap-2">
          <Settings2 className="size-4 text-muted" strokeWidth={1.5} aria-hidden />
          <span className="flex-1">Settings</span>
          <span className="text-meta text-muted">{on ? "Auto mode on" : "Agents ask you"}</span>
        </a>
      </DropdownMenuItem>
      <DropdownMenuSeparator />
    </>
  );
}
