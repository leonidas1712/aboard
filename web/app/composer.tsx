"use client";

// The message box: one field that posts as the person. Who a message goes to is the
// recipients picked outside the text (a reply's default ones, or the "To" menu's) plus
// everyone the text mentions with "@"; with neither, it goes to everyone. Typing "@"
// offers the board's agents, people and roles. The paperclip, a drop or a paste attaches
// files: board files at a version, or local ones put on the board first.

import { ChevronDown, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { ApiError, type BoardFile, type Member, type MemberRef, type Message, post } from "./api";
import { AttachButton, DraftChips, useDrafts } from "./attachments";
import { DropHint, useDrop } from "./files";
import { type Candidate, type Query, candidates, knownTargets, mentionsIn, queryAt, segments, toSummary, toWords } from "./mentions";
import { SenderMark } from "./agent-mark";
import { harnessName, recipient } from "./words";

type Props = {
  board: string;
  /** members are everyone on the board, agents and people. */
  members: Member[];
  /** roles are the board's roles, which a message can address as role:R. */
  roles: string[];
  /** me is the person writing, left out of suggestions and of a reply's recipients. */
  me: string | null;
  replyTo: Message | null;
  /** replyDefault is who a reply goes to unless the person changes it. */
  replyDefault: string[];
  identity: (from: MemberRef) => number;
  onCancelReply: () => void;
  /** onPosted runs with the message as stored. */
  onPosted: (m: Message) => void;
  onError: (e: unknown) => void;
  /** files are the board's files; null on a server without files, which offers no attachments. */
  files?: BoardFile[] | null;
  /** onFilesChanged runs after the message box puts a file on the board. */
  onFilesChanged?: () => void;
};

const noFiles: BoardFile[] = [];
const nothing = () => {};

export function Composer({ board, members, roles, me, replyTo, replyDefault, identity, onCancelReply, onPosted, onError, files = null, onFilesChanged = nothing }: Props) {
  // picked are the recipients chosen outside the text: a reply's default ones and the
  // "To" menu's. Each shows as a chip that removes it.
  const [picked, setPicked] = useState<string[]>([]);
  const [body, setBody] = useState("");
  const [caret, setCaret] = useState(0);
  const [busy, setBusy] = useState(false);
  const [focused, setFocused] = useState(false);
  const [problem, setProblem] = useState<ApiError | null>(null);
  const [active, setActive] = useState(0);
  // dismissed is where the "@" of a suggestion list closed with Escape starts, so the
  // list stays closed until another mention is begun.
  const [dismissed, setDismissed] = useState<number | null>(null);
  const [announce, setAnnounce] = useState("");
  const field = useRef<HTMLTextAreaElement>(null);
  const backdrop = useRef<HTMLDivElement>(null);
  const ids = useId();
  // One key per message being written, so a retried post after a dropped response
  // never posts twice.
  const key = useRef<string>("");
  const drafts = useDrafts(board, files ?? noFiles, me, onFilesChanged);
  const drop = useDrop(files !== null, (_f, _n, all) => drafts.upload(all));
  // Another file set makes another message.
  const draftKey = drafts.selectors.map((x) => `${x.file}@${x.version}`).join();
  useEffect(() => {
    key.current = "";
  }, [draftKey]);

  const known = useMemo(() => knownTargets(members, roles), [members, roles]);
  const mentioned = useMemo(() => mentionsIn(body, known), [body, known]);
  // Who the message goes to: picked first, then mentions in the order written.
  const to = useMemo(() => [...picked, ...mentioned.filter((t) => !picked.includes(t))], [picked, mentioned]);
  const summary = toSummary(to);
  const label = `Message ${toWords(to)}`;

  // A new reply starts from its default recipients; leaving a reply starts from none.
  const defaults = useRef(replyDefault);
  defaults.current = replyDefault;
  const replyId = replyTo?.id ?? null;
  useEffect(() => {
    setPicked(replyId ? defaults.current : []);
    if (replyId) field.current?.focus();
  }, [replyId]);

  // The field grows with its text, up to about eight lines.
  useEffect(() => {
    const el = field.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 200)}px`;
    if (backdrop.current) backdrop.current.scrollTop = el.scrollTop;
  }, [body]);

  const query: Query | null = focused ? queryAt(body, caret) : null;
  const open = query !== null && query.start !== dismissed;
  const offered = useMemo(() => (open ? candidates(members, roles, me, query.text) : []), [open, members, roles, me, query?.text]);
  const showList = open && offered.length > 0;
  const current = Math.min(active, Math.max(0, offered.length - 1));

  // Screen readers hear how many suggestions there are as the list changes.
  const listNote = showList ? `${offered.length} ${offered.length === 1 ? "suggestion" : "suggestions"}. Up and down to choose, Enter to pick.` : "";
  useEffect(() => {
    if (listNote) setAnnounce(listNote);
  }, [listNote]);

  const choose = (c: Candidate) => {
    if (!query) return;
    const el = field.current;
    const end = query.start + 1 + query.text.length;
    const after = body.slice(end);
    const insert = `@${c.label}${after.startsWith(" ") ? "" : " "}`;
    const next = body.slice(0, query.start) + insert + after;
    const at = query.start + insert.length + (after.startsWith(" ") ? 1 : 0);
    setBody(next);
    setCaret(at);
    setActive(0);
    key.current = "";
    setAnnounce(`${recipient(c.target)} added. To ${toSummary([...picked, ...mentionsIn(next, known).filter((t) => !picked.includes(t))])}.`);
    placeCaret.current = at;
    el?.focus();
  };

  // After a pick the caret goes right after the name, once the new text is in the
  // field and before the next key is handled.
  const placeCaret = useRef<number | null>(null);
  useLayoutEffect(() => {
    const at = placeCaret.current;
    if (at === null) return;
    placeCaret.current = null;
    field.current?.setSelectionRange(at, at);
  }, [body]);

  const unpick = (t: string) => {
    setPicked((p) => p.filter((x) => x !== t));
    key.current = "";
    setAnnounce(`${recipient(t)} removed.`);
    field.current?.focus();
  };

  const send = async (e?: FormEvent) => {
    e?.preventDefault();
    const text = body.trim();
    if (!text || busy || !drafts.ready) return;
    if (!key.current) key.current = crypto.randomUUID();
    setBusy(true);
    setProblem(null);
    try {
      const posted = await post<Message>(
        `/v1/boards/${encodeURIComponent(board)}/messages`,
        {
          body: text,
          to: to.length > 0 ? to : ["all"],
          ...(replyTo ? { reply_to: replyTo.id } : {}),
          ...(drafts.selectors.length > 0 ? { files: drafts.selectors } : {}),
        },
        key.current,
      );
      key.current = "";
      setBody("");
      setPicked([]);
      drafts.clear();
      setDismissed(null);
      onCancelReply();
      onPosted(posted);
    } catch (err) {
      if (err instanceof ApiError && err.status !== 401) setProblem(err);
      else onError(err);
    } finally {
      setBusy(false);
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.nativeEvent.isComposing) return;
    if (showList) {
      if (e.key === "ArrowDown" || e.key === "ArrowUp") {
        e.preventDefault();
        const step = e.key === "ArrowDown" ? 1 : -1;
        setActive((current + step + offered.length) % offered.length);
        return;
      }
      if (e.key === "Enter" || e.key === "Tab") {
        e.preventDefault();
        choose(offered[current]);
        return;
      }
      if (e.key === "Escape") {
        e.preventDefault();
        setDismissed(query!.start);
        setAnnounce("Suggestions closed.");
        return;
      }
    }
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void send();
    } else if (e.key === "Escape" && replyTo) {
      onCancelReply();
    }
  };

  const track = () => {
    const el = field.current;
    if (el) setCaret(el.selectionStart);
  };

  // The "To" menu ticks everyone the message goes to. A name it ticks becomes a chip;
  // a name the text mentions stays ticked until the mention is removed from the text.
  const agents = members.filter((m) => m.kind === "agent");
  const people = members.filter((m) => m.kind === "human" && m.name !== me);
  const toggle = (t: string, on: boolean) => {
    setPicked((p) => (on ? [...p.filter((x) => x !== t), t] : p.filter((x) => x !== t)));
    key.current = "";
  };
  const menuItem = (t: string, text: string) => {
    const inText = mentioned.includes(t) && !picked.includes(t);
    return (
      <DropdownMenuCheckboxItem
        key={t}
        checked={to.includes(t)}
        disabled={inText}
        onCheckedChange={(on) => toggle(t, on === true)}
        onSelect={(e) => e.preventDefault()}
        className="data-[disabled]:text-muted"
      >
        <span className="flex min-w-0 flex-col">
          <span>{text}</span>
          {inText && <span className="text-meta text-muted">Mentioned in the text</span>}
        </span>
      </DropdownMenuCheckboxItem>
    );
  };

  const listId = `${ids}-mentions`;
  const optionId = (i: number) => `${ids}-mention-${i}`;

  return (
    <form
      onSubmit={send}
      className="composer relative border-t border-rule pt-3 pb-[max(1rem,env(safe-area-inset-bottom))]"
      aria-label="Post a message"
      data-drop={drop.over ? "over" : undefined}
      {...drop.props}
    >
      <DropHint over={drop.over} text="Drop to attach to your message" />
      {replyTo && (
        <div className="mb-2 flex items-center gap-2 text-meta text-muted">
          <p className="min-w-0 flex-1 truncate">
            Replying to <strong className="text-ink">{replyTo.sender === "self" ? "your message" : replyTo.from.name}</strong>:{" "}
            {replyTo.body}
          </p>
          <button
            type="button"
            onClick={onCancelReply}
            className="tap inline-flex size-8 items-center justify-center rounded-[6px] text-ink hover:bg-hover"
            aria-label="Cancel the reply"
            title="Cancel the reply"
          >
            <X className="size-4" strokeWidth={1.5} aria-hidden />
          </button>
        </div>
      )}
      {picked.length > 0 && (
        <ul className="recipient-chips mb-2 flex flex-wrap items-center gap-1.5" aria-label="Recipients you picked">
          {picked.map((t) => (
            <li key={t}>
              <button
                type="button"
                data-target={t}
                onClick={() => unpick(t)}
                className="recipient-chip tap inline-flex h-8 items-center gap-1.5 rounded-control border border-rule bg-surface pr-1.5 pl-2.5 text-meta text-ink transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-hover"
                aria-label={`Remove ${recipient(t)} from the recipients`}
              >
                {recipient(t)}
                <X className="size-3.5 text-muted" strokeWidth={1.75} aria-hidden />
              </button>
            </li>
          ))}
          {replyTo && <li className="text-meta text-muted">Type @ to add someone</li>}
        </ul>
      )}
      <DraftChips board={board} d={drafts} bodyEmpty={body.trim() === ""} />
      {showList && (
        <ul
          id={listId}
          role="listbox"
          aria-label="People, agents and roles to mention"
          className="mention-list quiet-scroll absolute bottom-full left-0 z-40 mb-1 max-h-[min(19rem,50dvh)] w-full max-w-[22rem] overflow-y-auto rounded-control border border-rule bg-surface p-1 text-ink shadow-float animate-fade-in"
        >
          {offered.map((c, i) => (
            <li
              key={c.target}
              id={optionId(i)}
              role="option"
              aria-selected={i === current}
              aria-label={`${c.label}, ${detail(c, agents)}`}
              data-target={c.target}
              // Keep the field's focus and caret: the pick happens on mouse down.
              onMouseDown={(e) => {
                e.preventDefault();
                choose(c);
              }}
              onMouseMove={() => i !== current && setActive(i)}
              className={cn(
                "mention-option flex min-h-11 cursor-default items-center gap-2.5 rounded-[6px] px-2 py-1.5 select-none",
                i === current && "bg-selected",
              )}
            >
              {c.member ? (
                <SenderMark name={c.member.name} kind={c.member.kind} harness={c.member.harness} identity={identity(c.member)} className="size-6 rounded-[6px] text-[11px]" />
              ) : (
                <span aria-hidden className="flex size-6 shrink-0 items-center justify-center rounded-[6px] border border-rule text-meta text-muted">
                  @
                </span>
              )}
              <span className="mention-name min-w-0 flex-1 truncate font-bold">{c.label}</span>
              <span className="shrink-0 text-meta text-muted">{detail(c, agents)}</span>
            </li>
          ))}
        </ul>
      )}
      <p className="sr-only" aria-live="polite" aria-atomic="true">
        {announce}
      </p>
      <p className="sr-only" aria-live="polite" aria-atomic="true">
        {drafts.note}
      </p>
      {/* One field holds the recipients, the text and Post. Focus changes the field
          once, softly; only a control reached by keyboard gets its own ring. */}
      <div
        className="composer-field flex min-w-0 flex-wrap items-end gap-1 sm:flex-nowrap rounded-box border border-field-border bg-surface p-1 transition-[border-color,box-shadow] duration-[140ms] ease-out data-[focused]:border-[var(--field-focus)] data-[focused]:shadow-[0_0_0_4px_var(--focus-glow)]"
        data-focused={focused || undefined}
        onFocusCapture={() => setFocused(true)}
        onBlurCapture={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node | null)) setFocused(false);
        }}
      >
        <DropdownMenu>
          <DropdownMenuTrigger
            className="to-label tap inline-flex h-11 max-w-full shrink-0 items-center max-sm:h-9 max-sm:basis-full max-sm:justify-start gap-1 rounded-control px-2.5 text-meta text-ink outline-none hover:bg-hover focus-visible:outline-2 focus-visible:outline-offset-0 focus-visible:outline-accent-strong sm:max-w-[16rem]"
            aria-label={`Recipients: ${toWords(to)}. Change`}
          >
            <span className="truncate">To {summary}</span>
            <ChevronDown className="size-3.5 shrink-0" strokeWidth={1.5} aria-hidden />
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <DropdownMenuCheckboxItem
              checked={to.length === 0}
              disabled={mentioned.length > 0}
              onCheckedChange={() => setPicked([])}
              className="data-[disabled]:text-muted"
            >
              <span className="flex min-w-0 flex-col">
                <span>Everyone</span>
                {mentioned.length > 0 && <span className="text-meta text-muted">Remove the mentions first</span>}
              </span>
            </DropdownMenuCheckboxItem>
            {me && agents.some((a) => a.owner === me) && menuItem(`owner:${me}`, "My agents")}
            {agents.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>Agents</DropdownMenuLabel>
                {agents.map((a) => menuItem(`@${a.name}`, a.name))}
              </>
            )}
            {people.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>People</DropdownMenuLabel>
                {people.map((p) => menuItem(`@${p.name}`, p.name))}
              </>
            )}
            {roles.length > 0 && (
              <>
                <DropdownMenuSeparator />
                <DropdownMenuLabel>Roles</DropdownMenuLabel>
                {roles.map((r) => menuItem(`role:${r}`, `Role ${r}`))}
              </>
            )}
          </DropdownMenuContent>
        </DropdownMenu>
        <label htmlFor="message-body" className="sr-only">
          {label}
        </label>
        <div className="relative min-w-0 flex-1">
          {/* The text again, behind the field, with each mention on a tint. The field's
              own text draws on top; this layer only adds the tint. */}
          <div
            ref={backdrop}
            aria-hidden
            className="mention-backdrop pointer-events-none absolute inset-0 overflow-hidden px-2.5 py-[10px] text-body break-words whitespace-pre-wrap text-transparent pointer-coarse:text-[16px]"
          >
            {segments(body, known).map((s, i) =>
              s.target ? (
                <mark key={i} className="mention-mark rounded-[4px] bg-[var(--mention)] text-transparent">
                  {s.text}
                </mark>
              ) : (
                <span key={i}>{s.text}</span>
              ),
            )}
            {/* A trailing line break needs a character after it to take up its line. */}
            {body.endsWith("\n") && " "}
          </div>
          <textarea
            id="message-body"
            ref={field}
            rows={1}
            value={body}
            role="combobox"
            aria-autocomplete="list"
            aria-expanded={showList}
            aria-controls={showList ? listId : undefined}
            aria-activedescendant={showList ? optionId(current) : undefined}
            aria-describedby={`${ids}-hint`}
            onChange={(e) => {
              setBody(e.target.value);
              setCaret(e.target.selectionStart);
              setActive(0);
              key.current = "";
            }}
            onSelect={track}
            onClick={track}
            onKeyDown={onKeyDown}
            onPaste={(e) => {
              const pasted = Array.from(e.clipboardData.files);
              if (files === null || pasted.length === 0) return;
              e.preventDefault();
              drafts.upload(pasted);
            }}
            onScroll={(e) => {
              if (backdrop.current) backdrop.current.scrollTop = e.currentTarget.scrollTop;
            }}
            placeholder={label}
            className="relative block min-h-11 w-full resize-none bg-transparent px-2.5 py-[10px] text-body text-ink outline-none placeholder:text-muted pointer-coarse:text-[16px]"
          />
          <span id={`${ids}-hint`} className="sr-only">
            Type @ to mention an agent, a person or a role; each one you mention receives the message.
          </span>
        </div>
        {files !== null && <AttachButton files={files} d={drafts} />}
        <Button type="submit" disabled={busy || body.trim() === "" || !drafts.ready} className="focus-visible:outline-offset-0">
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

/** detail is the muted word beside a suggestion: an agent's harness, "person", or a role's size. */
function detail(c: Candidate, agents: Member[]): string {
  if (c.kind === "role") {
    const n = agents.filter((a) => `role:${a.role}` === c.target).length;
    return n === 0 ? "role" : `role · ${n} ${n === 1 ? "agent" : "agents"}`;
  }
  if (c.kind === "human") return "person";
  return harnessName(c.member?.harness) ?? "agent";
}
