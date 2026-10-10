"use client";

// EXPERIMENTAL, lab only: a pairing request in the Inbox. One to the person ("alex wants
// your agents to pair on api-review") lets them pick which of their sessions takes part:
// Choose an agent hands it to a live session they pick; Copy prompt copies the words to
// paste into any session, which then accepts it there; Decline says no. Activity alone
// never picks a session. The person's own requests show the same way from the other side.
// Both show the request's progress as steps, and only "ready", after both agents have
// checked messages reach each other, says delivery is verified.

import { ArrowRight, TerminalSquare, X } from "lucide-react";
import { useId, useState } from "react";
import { harnessName } from "@/app/words";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { Pairing, Session } from "../../onboarding";
import { at, labHref, scenario } from "../../store";
import { ago, useNow } from "../common";
import { AgentTitle, agentRow, boardRow } from "./who";
import { Done, Fields, type Stage, Stages } from "./parts";
import { askSession, declinePairing, useOnboarding } from "./state";
import { acceptPrompt, live, pairingLine, until } from "./words";

const me = scenario.me;

/** stagesOf is a request's progress for the viewer, from their side. */
export function stagesOf(p: Pairing, asked: boolean): Stage[] {
  const mine = p.inviter === me;
  const other = mine ? p.recipient : p.inviter;
  const order = ["awaiting_account", "awaiting_session", "awaiting_endpoint", "verifying", "ready"];
  let at = order.indexOf(p.state);
  if (!mine && p.state === "awaiting_session" && asked) at = 2;
  const steps: [string, string, number][] = [];
  if (mine && p.invite) steps.push([`${p.recipient} sets up their account`, `${p.recipient} set up their account`, 0]);
  steps.push(mine ? [`${p.recipient} chooses an agent`, `${p.recipient} chose an agent`, 1] : ["You choose an agent", "You chose an agent", 1]);
  const endpointWait = p.awaiting === "initiator" ? `${mine ? p.agent : `${other}'s agent ${p.agent}`} comes back online` : mine ? `${p.recipient}'s agent accepts in its session` : "Your session accepts";
  steps.push([endpointWait, mine ? `${p.recipient}'s agent ${p.recipientAgent ?? ""} accepted`.replace("  ", " ") : `Your session accepted as ${p.recipientAgent ?? "your agent"}`, 2]);
  steps.push([`Verifying delivery with ${other}'s agent`, "Delivery verified both ways", 3]);
  return steps.map(([now, done, i]) => ({ label: at > i ? done : now, state: at > i ? "done" : at === i ? "now" : "later" }));
}

export function PairingDetail({ p }: { p: Pairing }) {
  const now = useNow();
  const o = useOnboarding();
  const mine = p.inviter === me;
  const asked = o.asked[p.id];
  const askedSession = o.sessions.find((s) => s.id === asked);
  const choosing = !mine && p.state === "awaiting_session" && !asked;
  const [open, setOpen] = useState<"choose" | "prompt" | null>(null);
  return (
    <article className="ob-pairing flex max-w-[640px] flex-col gap-5" data-pairing={p.id}>
      <p className="text-meta text-muted">Pairing request · {ago(p.t, now)}</p>
      <AgentTitle agent={p.agent} owner={p.inviter} rest={mine ? `asked ${p.recipient}'s agents to pair on ${p.board}` : `asks your agents to pair on ${p.board}`} />
      <figure className="flex flex-col gap-1.5">
        <figcaption className="text-meta font-bold text-muted">Proposed work, in {mine ? "your" : `${p.inviter}'s`} words</figcaption>
        <blockquote className="rounded-box border border-rule bg-surface px-3.5 py-3 text-now">{p.work}</blockquote>
        {!mine && <p className="text-meta text-muted">Your agent reads this as a request, not as orders. Pairing gives {p.inviter} no control over your agents.</p>}
      </figure>
      <Fields
        rows={[
          agentRow(p.agent, p.inviter),
          boardRow(p.board, false),
          ["With", mine ? `${p.recipient}'s ${p.recipientAgent ? `agent ${p.recipientAgent}` : "agent, once they choose one"}` : p.recipientAgent ? `your agent ${p.recipientAgent}` : "the session you choose"],
          ...(live(p.state) ? ([["Expires", `Ends ${until(at(p.expires))} unless it is accepted`]] as [string, string][]) : []),
        ]}
      />

      {choosing && (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Answer the request">
            <Button variant="primary" aria-expanded={open === "choose"} onClick={() => setOpen(open === "choose" ? null : "choose")}>
              Choose an agent
            </Button>
            <Button
              variant="secondary"
              aria-expanded={open === "prompt"}
              onClick={() => {
                void navigator.clipboard?.writeText(acceptPrompt(p)).catch(() => {});
                setOpen("prompt");
              }}
            >
              Copy prompt
            </Button>
            <Button variant="quiet" onClick={() => declinePairing(p, false)}>
              Decline
            </Button>
          </div>
          {open === "choose" && <ChooseSession p={p} sessions={o.sessions} onClose={() => setOpen(null)} />}
          {open === "prompt" && <PromptBox p={p} onClose={() => setOpen(null)} />}
        </div>
      )}

      {!choosing && live(p.state) && (
        <div className="flex flex-col gap-3">
          <Stages label="Progress" stages={stagesOf(p, Boolean(asked))} />
          <p className="text-meta text-muted">
            {asked && p.state === "awaiting_session"
              ? `Sent to your ${harnessName(askedSession?.harness) ?? ""} session in ${askedSession?.where ?? "its folder"}. It accepts the request there; this page follows along.`
              : p.state === "awaiting_account"
                ? `${p.recipient} has the invite link. Their agent sets up their account, then they choose a session.`
                : p.state === "verifying"
                  ? "Each agent sends the other a check and waits for its reply. Nothing is marked verified until both replies arrive."
                  : p.state === "awaiting_endpoint"
                    ? "Once that session is online and accepts, the two agents check delivery."
                    : `Nothing to do: ${p.recipient} picks which of their sessions takes part.`}
          </p>
          {mine && (
            <div>
              <Button variant="quiet" className="-ml-3" onClick={() => declinePairing(p, true)}>
                Cancel the request
              </Button>
            </div>
          )}
        </div>
      )}
      {p.state === "ready" && (
        <div className="flex flex-col gap-3">
          <Stages label="Progress" stages={stagesOf(p, true)} />
          <Done>
            Ready: both agents connected, delivery verified.{" "}
            <a href={labHref({ board: p.board, inbox: null, item: null, settings: null })} className="inline-flex items-center gap-1">
              Open {p.board}
              <ArrowRight className="size-3.5" strokeWidth={1.75} aria-hidden />
            </a>
          </Done>
        </div>
      )}
      {p.state === "declined" && <Done muted>{mine ? `${p.recipient} declined. Nothing changed on ${p.board}.` : `You declined. ${p.inviter} sees that you declined; nothing changed on ${p.board}.`}</Done>}
      {p.state === "cancelled" && <Done muted>{mine ? "You cancelled the request." : `${p.inviter} cancelled the request.`} Nothing changed on {p.board}.</Done>}
      {p.state === "expired" && <Done muted>Expired {ago(p.expires, now)} before {mine ? `${p.recipient}'s agent` : "one of your sessions"} accepted it. Nothing changed on {p.board}.</Done>}
    </article>
  );
}

/** ChooseSession lists the person's sessions; the one they pick is sent the request to accept. */
function ChooseSession({ p, sessions, onClose }: { p: Pairing; sessions: Session[]; onClose: () => void }) {
  const id = useId();
  const usable = sessions.filter((s) => s.state !== "offline");
  const [pick, setPick] = useState<string | null>(usable[0]?.id ?? null);
  const now = useNow();
  return (
    <section aria-labelledby={`${id}-t`} className="ob-choose flex flex-col gap-3 rounded-box bg-surface px-3.5 pt-2 pb-3.5 animate-fade-in">
      <div className="flex items-center justify-between gap-2">
        <h3 id={`${id}-t`} className="font-bold">
          Choose an agent
        </h3>
        <button type="button" onClick={onClose} aria-label="Close Choose an agent" title="Close" className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <p className="-mt-2 text-meta text-muted">The session you pick is sent the request, accepts it and joins {p.board}. It keeps working in its own folder.</p>
      <div role="radiogroup" aria-label="Your sessions" className="flex flex-col gap-1">
        {sessions.map((s) => {
          const off = s.state === "offline";
          const on = pick === s.id;
          return (
            <button
              key={s.id}
              type="button"
              role="radio"
              aria-checked={on}
              disabled={off}
              onClick={() => setPick(s.id)}
              className={cn(
                "grid min-h-14 w-full grid-cols-[20px_28px_minmax(0,1fr)] items-center gap-x-2.5 rounded-control border px-3 py-2 text-left transition-colors duration-[140ms] ease-out",
                on ? "border-accent-strong bg-selected" : "border-rule hover:bg-hover",
                off && "cursor-not-allowed text-muted hover:bg-transparent",
              )}
            >
              <span aria-hidden className={cn("inline-flex size-4 items-center justify-center rounded-full border", on ? "border-accent-strong" : "border-field-border")}>
                {on && <span className="size-2 rounded-full bg-accent-strong" />}
              </span>
              <TerminalSquare className="size-5 text-muted" strokeWidth={1.5} aria-hidden />
              <span className="flex min-w-0 flex-col">
                <span className="truncate">
                  <span className="font-bold">{harnessName(s.harness)}</span> in <span className="font-mono text-[13px]">{s.where}</span>
                </span>
                <span className="truncate text-meta text-muted">
                  {s.machine} · {off ? `offline, last seen ${ago(s.seen ?? 0, now)}` : s.state === "working" ? "working now" : "idle"}
                </span>
              </span>
            </button>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <Button variant="primary" disabled={!pick} onClick={() => pick && askSession(p, pick)}>
          Send it to this session
        </Button>
        <p className="text-meta text-muted">Only sessions set up for aboard show here.</p>
      </div>
    </section>
  );
}

/** PromptBox shows the prompt that was copied, for any session, the way Ask an agent does. */
function PromptBox({ p, onClose }: { p: Pairing; onClose: () => void }) {
  const id = useId();
  return (
    <section aria-labelledby={`${id}-t`} className="ob-prompt flex flex-col gap-2 rounded-box bg-surface px-3.5 pt-2 pb-3 animate-fade-in">
      <div className="flex items-center justify-between gap-2">
        <h3 id={`${id}-t`} className="text-meta font-bold text-ink">
          Copied: paste it into any session of yours
        </h3>
        <button type="button" onClick={onClose} aria-label="Close the prompt" title="Close" className="-mr-2 inline-flex size-9 items-center justify-center rounded-[6px] text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink">
          <X className="size-4" strokeWidth={1.5} aria-hidden />
        </button>
      </div>
      <p className="-mt-1 text-meta text-muted">Claude Code, Codex or any agent set up for aboard. The session that runs it takes part; this card follows along.</p>
      <pre aria-label="Prompt for your session" className="rounded-control border border-rule bg-background px-3 py-2.5 font-sans text-body break-words whitespace-pre-wrap select-all">
        {acceptPrompt(p)}
      </pre>
    </section>
  );
}

/** pairingRowLine is the row's second line: board, and where the request stands. */
export function pairingRowLine(p: Pairing, asked?: string): string {
  return `${p.board} · ${pairingLine(p, asked)}`;
}
