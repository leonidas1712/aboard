"use client";

import { useEffect, useRef, useState } from "react";
import { type Message, post } from "./api";
import { Problem } from "./chrome";
import { keyLabel, pressed, typing } from "./keys";
import { Kbd } from "./keys-sheet";
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
  const replyButton = useRef<HTMLButtonElement>(null);
  const backToReply = useRef(false);
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
  // What the agent will go ahead with: an option with the same words, or else its own
  // words, sent as the reply.
  const proposed = ask?.going_with ?? null;
  const proposedOption = proposed ? ask!.options.findIndex((o) => o.trim().toLowerCase() === proposed.trim().toLowerCase()) + 1 : 0;
  const accept = () => { if (proposed) void answer(proposedOption ? ask!.options[proposedOption - 1] : proposed, proposedOption || undefined); };
  const leave = () => { backToReply.current = true; setWriting(false); };
  useEffect(() => {
    if (writing) box.current?.focus();
    else if (backToReply.current) { backToReply.current = false; replyButton.current?.focus(); }
  }, [writing]);
  useEffect(() => {
    if (!keyboard || !enabled || sent) return;
    const key = (e: KeyboardEvent) => {
      if (typing(e)) return;
      const n = Number(e.key);
      if (pressed("option", e) && n <= (ask?.options.length ?? 0)) void answer(ask!.options[n - 1], n);
      else if (pressed("accept", e) && proposed) accept();
      else if (pressed("reply", e)) setWriting(true);
      else return;
      e.preventDefault();
    };
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  });
  if (!ask) return null;
  if (sent) return <p className="py-2 text-meta text-muted" role="status">Sent to {message.from.name}: &ldquo;{sent.body}&rdquo;</p>;
  const choice = "flex min-h-12 w-full items-center gap-3 rounded-control border border-rule bg-surface px-3.5 py-2 text-left transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-hover disabled:opacity-60";
  return <div className="ask-answers mt-3 flex max-w-[640px] flex-col gap-2">
    {ask.going_with && <p className="text-meta text-muted">{ask.state === "went_with" ? "Went with" : "Going with"} {ask.going_with}{ask.going_at && ` · ${clockTime(ask.going_at)}`}{ask.state !== "went_with" && " unless you say"}</p>}
    {ask.state === "withdrawn" ? <p className="text-meta text-muted">Withdrawn</p> : ask.state === "answered" && <p className="text-meta text-muted">Answered{ask.answer_option ? ` · ${ask.options[ask.answer_option - 1]}` : " in words"}</p>}
    <ol className="flex flex-col gap-2" aria-label="Answers">
      {ask.options.map((option, i) => <li key={`${i}-${option}`}>
        {enabled ? <button type="button" disabled={busy} aria-label={`Answer with option ${i + 1}: ${option}`} onClick={() => void answer(option, i + 1)} className={choice}>
          <Kbd>{i + 1}</Kbd><span className="min-w-0 flex-1">{option}</span>{proposedOption === i + 1 && <span className="flex shrink-0 items-center gap-2 text-meta text-muted">proposed{keyboard && <Kbd>{keyLabel("accept")}</Kbd>}</span>}
        </button> : <p className="flex items-center gap-3 py-1 text-meta text-muted"><span>{i + 1}.</span>{option}</p>}
      </li>)}
    </ol>
    {enabled && proposed && !proposedOption && <button type="button" disabled={busy} onClick={accept} className={choice}>{keyboard && <Kbd>{keyLabel("accept")}</Kbd>}<span className="min-w-0 flex-1">Go ahead with {proposed}</span></button>}
    {enabled && (writing ? <form className="flex flex-col gap-2 rounded-control border border-field-border bg-surface p-2.5" onSubmit={(e) => { e.preventDefault(); void answer(text.trim()); }}>
      <label htmlFor={`answer-${message.id}`} className="text-meta text-muted">Your answer, sent to {message.from.name}</label>
      <textarea id={`answer-${message.id}`} ref={box} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={(e) => {
        if (pressed("send", e)) { e.preventDefault(); void answer(text.trim()); }
        else if (pressed("leave", e)) { e.preventDefault(); leave(); }
      }} rows={3} className="w-full resize-y rounded-control border border-field-border bg-surface px-2.5 py-1.5 text-ink" disabled={busy} />
      <div className="flex flex-wrap items-center gap-3">
        <button type="submit" disabled={busy || !text.trim()} className="inline-flex min-h-11 items-center gap-2 rounded-control border border-accent-strong bg-accent px-3.5 font-bold text-on-accent disabled:border-rule disabled:bg-selected disabled:text-muted">{busy ? "Sending…" : `Send to ${message.from.name}`}{keyboard && !busy && <span aria-hidden className="font-normal opacity-80 pointer-coarse:hidden">{keyLabel("send")}</span>}</button>
        <button type="button" disabled={busy} className="min-h-11 px-1 text-meta text-link hover:underline" onClick={leave}>Cancel</button>
        {keyboard && <span className="text-meta text-muted pointer-coarse:hidden">or {keyLabel("leave")}</span>}
      </div>
    </form> : <button ref={replyButton} type="button" disabled={busy} onClick={() => setWriting(true)} className={choice}>{keyboard && <Kbd>{keyLabel("reply")}</Kbd>}Reply with something else</button>)}
    {error !== null && <Problem error={error} />}
  </div>;
}
