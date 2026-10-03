"use client";

// The message box: one field that posts as the person, to everyone unless they pick a
// name or a role, and a reply that links to the message it answers.

import { ChevronDown, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { ApiError, type Member, type Message, post } from "./api";
import { recipient } from "./words";

type Props = {
  board: string;
  agents: Member[];
  replyTo: Message | null;
  onCancelReply: () => void;
  onPosted: () => void;
  onError: (e: unknown) => void;
};

export function Composer({ board, agents, replyTo, onCancelReply, onPosted, onError }: Props) {
  const [to, setTo] = useState("all");
  const [body, setBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const field = useRef<HTMLTextAreaElement>(null);
  // One key per message being written, so a retried post after a dropped response
  // never posts twice.
  const key = useRef<string>("");

  const roles = [...new Set(agents.map((a) => a.role).filter((r): r is string => !!r))].sort();
  const target = replyTo ? (replyTo.sender === "self" ? replyTo.to.join(",") : `@${replyTo.from.name}`) : to;
  const label = `Message ${target.split(",").map(recipient).join(", ")}`;

  useEffect(() => {
    if (replyTo) field.current?.focus();
  }, [replyTo]);

  // The field grows with its text, up to about eight lines.
  useEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
  }, [body]);

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const text = body.trim();
    if (!text || busy) return;
    if (!key.current) key.current = crypto.randomUUID();
    setBusy(true);
    setProblem(null);
    try {
      await post<Message>(
        `/v1/boards/${encodeURIComponent(board)}/messages`,
        { body: text, to: target.split(","), ...(replyTo ? { reply_to: replyTo.id } : {}) },
        key.current,
      );
      key.current = "";
      setBody("");
      onCancelReply();
      onPosted();
    } catch (err) {
      if (err instanceof ApiError && err.status !== 401) setProblem(err);
      else onError(err);
    } finally {
      setBusy(false);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault();
      void send();
    } else if (e.key === "Escape" && replyTo) {
      onCancelReply();
    }
  };

  return (
    <form onSubmit={send} className="composer border-t border-rule pt-3 pb-4" aria-label="Post a message">
      {replyTo && (
        <div className="mb-2 flex items-center gap-2 text-meta text-muted">
          <p className="min-w-0 flex-1 truncate">
            Replying to <strong className="text-ink">{replyTo.sender === "self" ? "your message" : replyTo.from.name}</strong>: {replyTo.body}
          </p>
          <button
            type="button"
            onClick={onCancelReply}
            className="inline-flex size-8 items-center justify-center rounded-[6px] text-ink hover:bg-selected"
            aria-label="Cancel the reply"
            title="Cancel the reply"
          >
            <X className="size-4" strokeWidth={1.5} aria-hidden />
          </button>
        </div>
      )}
      <div className="flex items-end gap-2">
        <div className="flex min-w-0 flex-1 items-end rounded-control border border-field-border bg-surface transition-colors duration-[140ms] ease-out focus-within:outline-2 focus-within:outline-offset-2 focus-within:outline-accent">
          {!replyTo && (
            <DropdownMenu>
              <DropdownMenuTrigger
                className="m-1 inline-flex h-9 shrink-0 items-center gap-1 rounded-[6px] px-2 text-meta text-ink hover:bg-selected focus-visible:outline-2"
                aria-label={`Recipients: ${recipient(to)}. Change`}
              >
                To {recipient(to)}
                <ChevronDown className="size-3.5" strokeWidth={1.5} aria-hidden />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                <DropdownMenuRadioGroup value={to} onValueChange={setTo}>
                  <DropdownMenuRadioItem value="all">Everyone</DropdownMenuRadioItem>
                  {agents.length > 0 && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuLabel>Agents</DropdownMenuLabel>
                      {agents.map((a) => (
                        <DropdownMenuRadioItem key={a.id} value={`@${a.name}`}>
                          {a.name}
                        </DropdownMenuRadioItem>
                      ))}
                    </>
                  )}
                  {roles.length > 0 && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuLabel>Roles</DropdownMenuLabel>
                      {roles.map((r) => (
                        <DropdownMenuRadioItem key={r} value={`role:${r}`}>
                          Role {r}
                        </DropdownMenuRadioItem>
                      ))}
                    </>
                  )}
                </DropdownMenuRadioGroup>
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <label htmlFor="message-body" className="sr-only">
            {label}
          </label>
          <textarea
            id="message-body"
            ref={field}
            rows={1}
            value={body}
            onChange={(e) => {
              setBody(e.target.value);
              key.current = "";
            }}
            onKeyDown={onKeyDown}
            placeholder={label}
            className="min-h-[42px] min-w-0 flex-1 resize-none bg-transparent px-3 py-[9px] text-body text-ink outline-none placeholder:text-muted"
          />
        </div>
        <Button type="submit" disabled={busy || body.trim() === ""}>
          {busy ? "Posting…" : "Post"}
        </Button>
      </div>
      {problem && (
        <p role="alert" className="mt-2 text-ink">
          <strong>{problem.message}</strong> {problem.hint}
        </p>
      )}
    </form>
  );
}
