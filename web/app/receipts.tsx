"use client";

// Receipts on a message the person or one of their agents sent to someone: a quiet mark
// under it saying whether it has reached its recipients, and on hover or focus each
// recipient with their state. An agent has received a message once its read position
// passes it; a person has read it once theirs does. They are read when the message comes
// into view and again as the board moves, never kept by the page.

import { Check, Circle, Clock3 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { type Presence, type Receipt, type Receipts, get } from "./api";

/** wantsReceipts is true for a message the person or their agent sent to someone, not to everyone. */
export function wantsReceipts(m: { sender: string; to: string[] }): boolean {
  return (m.sender === "self" || m.sender === "owner_agent") && !m.to.includes("all");
}

const presenceNow: Record<Presence, string> = {
  working: "busy now",
  idle: "idle now",
  waiting: "waiting for its person now",
  no_session: "no session now",
};

/** stateOf says one recipient's state in words: "received", "read", "pending, busy now". */
function stateOf(r: Receipt): string {
  if (r.state !== "pending") return r.state;
  // A person is not delivered to: their message waits to be read, it is not pending.
  if (r.member.kind === "human") return "unread";
  if (r.queued) return "queued, arrives at the end of its turn";
  return r.presence ? `pending, ${presenceNow[r.presence]}` : "pending";
}

type Summary = { text: string; done: boolean; icon: "clock" | "circle" | "check" };

/**
 * summaryOf is the mark's own words: "Pending", "Received", "1 of 3", "Read by all 2",
 * "Unread". Delivery covers the agent recipients; people are not delivered to, so they
 * never make a message pending. With agents among the recipients the mark speaks for
 * them alone and the hover lists the people. With only people it says "Read" once they
 * all have, and "Unread" until then.
 */
function summaryOf(rs: Receipt[]): Summary {
  const agents = rs.filter((r) => r.member.kind === "agent");
  if (agents.length === 0) {
    if (rs.some((r) => r.state === "pending")) return { text: "Unread", done: false, icon: "circle" };
    return { text: rs.length === 1 ? "Read" : `Read by all ${rs.length}`, done: true, icon: "check" };
  }
  const done = agents.filter((r) => r.state !== "pending").length;
  if (done === 0) return { text: agents.some((r) => r.queued) ? "Queued" : "Pending", done: false, icon: "clock" };
  if (done < agents.length) return { text: `${done} of ${agents.length}`, done: false, icon: "check" };
  return { text: agents.length === 1 ? "Received" : `Received by all ${agents.length}`, done: true, icon: "check" };
}

export function ReceiptMark({ board, seq, activity }: { board: string; seq: number; activity: number }) {
  const ref = useRef<HTMLDivElement>(null);
  const [seen, setSeen] = useState(false);
  const [receipts, setReceipts] = useState<Receipts | null>(null);

  // Read only once the message is on screen, so a long timeline asks for few.
  useEffect(() => {
    const el = ref.current;
    if (!el || seen) return;
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting)) setSeen(true);
    });
    io.observe(el);
    return () => io.disconnect();
  }, [seen]);

  const load = useCallback(() => {
    get<Receipts>(`/v1/boards/${encodeURIComponent(board)}/messages/${seq}/receipts`).then(setReceipts, () => {
      // No receipts to show; the message reads the same without them.
    });
  }, [board, seq]);

  useEffect(() => {
    if (seen) load();
  }, [seen, activity, load]);

  if (receipts && !receipts.available && !receipts.to_everyone) return <div ref={ref} className="receipts text-meta text-muted">Receipts unavailable</div>;
  const rs = receipts?.recipients ?? [];
  if (rs.length === 0) return <div ref={ref} className="receipts" />;
  const summary = summaryOf(rs);
  const details = rs.map((r) => `${r.member.name}: ${stateOf(r)}`);
  return (
    <div ref={ref} className="receipts mt-1 flex">
      <Tooltip onOpenChange={(open) => open && load()}>
        <TooltipTrigger asChild>
          <button
            type="button"
            className="receipt-mark -ml-1 inline-flex h-6 items-center gap-1 rounded-[6px] px-1 text-meta text-muted transition-colors duration-[140ms] ease-out hover:bg-hover"
            data-done={summary.done || undefined}
            aria-label={`${summary.text}. ${details.join("; ")}`}
          >
            {summary.icon === "clock" ? (
              <Clock3 className="size-3.5 shrink-0" strokeWidth={1.5} aria-hidden />
            ) : summary.icon === "circle" ? (
              <Circle className="size-3.5 shrink-0" strokeWidth={1.5} aria-hidden />
            ) : (
              <Check className="size-3.5 shrink-0" strokeWidth={1.75} aria-hidden />
            )}
            <span aria-hidden>{summary.text}</span>
          </button>
        </TooltipTrigger>
        <TooltipContent>
          <ul className="receipt-list flex flex-col gap-0.5">
            {rs.map((r, i) => (
              <li key={`${r.member.kind}:${r.member.name}`}>{details[i]}</li>
            ))}
          </ul>
        </TooltipContent>
      </Tooltip>
    </div>
  );
}
