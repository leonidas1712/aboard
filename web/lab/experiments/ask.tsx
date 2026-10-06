"use client";

// EXPERIMENTAL, lab only: asking in place. Wherever the person looks (a task, the brief,
// a thread, an item that needs them) they can ask the right agent to act, and the agent
// does the work. An ask is only a message: to one agent, prefilled with what it is
// about, editable, sent through the same API as the message box. Nothing new on the
// server; it is what makes oversight two-way.

import { useState } from "react";
import { post, type Message } from "@/app/api";
import { cn } from "@/lib/utils";
import { openThread, scenario } from "../store";

export type AskProps = {
  /** label names the action ("Ask the owner", "Split this"). */
  label: string;
  /** to is the agent or person asked; everyone when null. */
  to: string | null;
  /** text is the message, prefilled; the person can change it. */
  text: string;
  /** about names the tasks the ask is about. */
  about?: string[];
  className?: string;
};

export function Ask({ label, to, text, about, className }: AskProps) {
  const [open, setOpen] = useState(false);
  const [body, setBody] = useState(text);
  const [sent, setSent] = useState<Message | null>(null);
  const [busy, setBusy] = useState(false);
  const who = to ? `@${to}` : "everyone";
  if (sent) {
    return (
      <span className={cn("ask-sent text-meta", className)}>
        Sent to {to ?? "everyone"}.{" "}
        <button type="button" className="text-link underline decoration-1 underline-offset-[3px] hover:no-underline" onClick={() => openThread(sent.id)}>
          See it in the timeline
        </button>
      </span>
    );
  }
  if (!open) {
    return (
      <button
        type="button"
        className={cn("ask min-h-8 text-meta text-link underline decoration-1 underline-offset-[3px] hover:no-underline", className)}
        onClick={() => {
          setBody(text);
          setOpen(true);
        }}
      >
        {label}
      </button>
    );
  }
  const send = async () => {
    setBusy(true);
    try {
      const m = await post<Message>(`/v1/boards/${encodeURIComponent(scenario.board.name)}/messages`, {
        body,
        to: to ? [`@${to}`] : ["all"],
        // Only the lab reads this; it stands for a message naming its tasks.
        about,
      });
      setSent(m);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="ask-form flex w-full basis-full flex-col gap-2 rounded-control border border-field-border bg-surface p-2.5 text-ink"
      onSubmit={(e) => {
        e.preventDefault();
        void send();
      }}
    >
      <label className="text-meta text-muted" htmlFor={`ask-${label}-${to}`}>
        {label}: a message to {who}
      </label>
      <textarea
        id={`ask-${label}-${to}`}
        value={body}
        onChange={(e) => setBody(e.target.value)}
        rows={3}
        className="w-full resize-y rounded-[6px] border border-field-border bg-surface px-2.5 py-1.5 text-body text-ink"
        autoFocus
      />
      <div className="flex items-center gap-3">
        <button type="submit" disabled={busy || !body.trim()} className="min-h-9 rounded-control bg-ink px-3.5 font-bold text-on-ink disabled:opacity-60">
          Send to {to ?? "everyone"}
        </button>
        <button type="button" className="min-h-9 text-meta text-link hover:underline" onClick={() => setOpen(false)}>
          Cancel
        </button>
      </div>
    </form>
  );
}
