"use client";

import { useEffect, useRef, useState } from "react";
import { type Message, post } from "./api";
import { Problem } from "./chrome";
import { clockTime } from "./words";

export const askChanged = "aboard:ask-changed";

/** AskAnswers sends the option and its words together as a recorded reply. */
export function AskAnswers({ message, board, keyboard = false, readOnly = false, onAnswered }: { message: Message; board: string; keyboard?: boolean; readOnly?: boolean; onAnswered?: (reply: Message) => void }) {
  const [writing, setWriting] = useState(false);
  const [text, setText] = useState("");
  const [busy, setBusy] = useState(false);
  const [sent, setSent] = useState<Message | null>(null);
  const [error, setError] = useState<unknown>(null);
  const box = useRef<HTMLTextAreaElement>(null);
  const inFlight = useRef(false);
  const attempt = useRef<{ body: string; option?: number; key: string } | null>(null);
  const ask = message.ask;
  const enabled = ask?.can_answer === true && ask.state !== "withdrawn" && !readOnly;

  const answer = async (body: string, option?: number) => {
    if (!enabled || inFlight.current || !body.trim()) return;
    inFlight.current = true;
    setBusy(true);
    setError(null);
    if (!attempt.current || attempt.current.body !== body || attempt.current.option !== option) attempt.current = { body, option, key: crypto.randomUUID() };
    try {
      const reply = await post<Message>(`/v1/boards/${encodeURIComponent(board)}/messages`, { body, reply_to: message.id, ...(option === undefined ? {} : { answer: { option } }) }, attempt.current.key);
      setSent(reply);
      window.dispatchEvent(new CustomEvent(askChanged, { detail: board }));
      onAnswered?.(reply);
    } catch (e) { setError(e); }
    finally { inFlight.current = false; setBusy(false); }
  };
  useEffect(() => { if (writing) box.current?.focus(); }, [writing]);
  useEffect(() => {
    if (!keyboard || !enabled || sent) return;
    const key = (e: KeyboardEvent) => {
      if (e.ctrlKey || e.metaKey || e.altKey || (e.target as HTMLElement)?.closest("input, textarea, select, [role=dialog], [role=menu]")) return;
      const n = Number(e.key);
      if (n >= 1 && n <= (ask?.options.length ?? 0)) { e.preventDefault(); void answer(ask!.options[n - 1], n); }
      else if (n === (ask?.options.length ?? 0) + 1) { e.preventDefault(); setWriting(true); }
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  });
  if (!ask) return null;
  if (sent) return <p className="py-2 text-meta text-muted" role="status">Sent to {message.from.name}: &ldquo;{sent.body}&rdquo;</p>;
  return <div className="ask-answers mt-3 flex max-w-[640px] flex-col gap-2">
    {ask.going_with && <p className="text-meta text-muted">{ask.state === "went_with" ? "Went with" : "Going with"} {ask.going_with}{ask.going_at && ` · ${clockTime(ask.going_at)}`}{ask.state !== "went_with" && " unless you say"}</p>}
    {ask.state === "withdrawn" ? <p className="text-meta text-muted">Withdrawn</p> : ask.state === "answered" && <p className="text-meta text-muted">Answered{ask.answer_option ? ` · ${ask.options[ask.answer_option - 1]}` : " in words"}</p>}
    <ol className="flex flex-col gap-2" aria-label="Answers">
      {ask.options.map((option, i) => <li key={`${i}-${option}`}>
        {enabled ? <button type="button" disabled={busy} aria-label={`Answer with option ${i + 1}: ${option}`} onClick={() => void answer(option, i + 1)} className="flex min-h-12 w-full items-center gap-3 rounded-control border border-rule bg-surface px-3.5 py-2 text-left transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected disabled:opacity-60">
          <kbd className="inline-flex size-6 shrink-0 items-center justify-center rounded-[5px] border border-rule font-sans text-meta text-muted">{i + 1}</kbd>{option}
        </button> : <p className="flex items-center gap-3 py-1 text-meta text-muted"><span>{i + 1}.</span>{option}</p>}
      </li>)}
    </ol>
    {enabled && (writing ? <form className="flex flex-col gap-2 rounded-control border border-field-border bg-surface p-2.5" onSubmit={(e) => { e.preventDefault(); void answer(text.trim()); }}>
      <label htmlFor={`answer-${message.id}`} className="text-meta text-muted">Your answer, sent to {message.from.name}</label>
      <textarea id={`answer-${message.id}`} ref={box} value={text} onChange={(e) => setText(e.target.value)} rows={3} className="w-full resize-y rounded-control border border-field-border bg-surface px-2.5 py-1.5 text-ink" disabled={busy} />
      <div className="flex gap-3"><button type="submit" disabled={busy || !text.trim()} className="min-h-10 rounded-control bg-ink px-3.5 font-bold text-on-ink disabled:opacity-60">{busy ? "Sending…" : `Send to ${message.from.name}`}</button><button type="button" disabled={busy} className="min-h-10 text-meta text-link hover:underline" onClick={() => setWriting(false)}>Cancel</button></div>
    </form> : <button type="button" disabled={busy} onClick={() => setWriting(true)} className="flex min-h-12 w-full items-center gap-3 rounded-control border border-rule bg-surface px-3.5 text-left hover:border-field-border hover:bg-selected disabled:opacity-60"><kbd className="inline-flex size-6 shrink-0 items-center justify-center rounded-[5px] border border-rule font-sans text-meta text-muted">{ask.options.length + 1}</kbd>Reply with something else</button>)}
    {error !== null && <Problem error={error} />}
  </div>;
}
