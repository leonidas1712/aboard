"use client";

// The finder is one box that goes anywhere: ⌘K (Ctrl+K off a Mac), or "Find or do
// anything" in the board list, opens it. Typing narrows a list of places (the Inbox,
// Settings and its tabs, and the tabs and panel of the board on screen), the person's boards
// in the sidebar's order, things to do (export the board, mark it read, show the keys)
// and the tasks of the board on screen; arrows pick one and Enter goes there or does it. Each
// action is one the page already offers elsewhere. It reads only what the page has
// already loaded, so it asks the server for nothing.

import { Search } from "lucide-react";
import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";
import { type Board, type Task, isArchived } from "./api";
import { type ExportAction, exportActions } from "./export-menu";
import { Kbd } from "./keys-sheet";
import { keyLabel, pressed } from "./keys";
import { sidebarOrder } from "./sidebars";
import { taskState } from "./task-ui";
import { boardLabel } from "./words";

const findEvent = "aboard:find";

/** openFinder opens the finder of the page on screen, as its key does. */
export function openFinder() {
  window.dispatchEvent(new Event(findEvent));
}

/** FindRow is the board list's way into the finder, with its key. */
export function FindRow() {
  return (
    <button
      type="button"
      onClick={openFinder}
      aria-haspopup="dialog"
      className="find-row -mx-2.5 flex min-h-11 items-center gap-2 rounded-control px-2.5 text-left text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
    >
      <Search className="size-4 shrink-0" strokeWidth={1.75} aria-hidden />
      <span className="flex-1">Find or do anything</span>
      <Kbd aria-label={`Shortcut: ${keyLabel("find")}`}>{keyLabel("find")}</Kbd>
    </button>
  );
}

export type View = "conversation" | "tasks" | "files";

/**
 * Here is the board on screen, as the finder needs it: its tabs and tasks and how to show
 * each, the board panel's members section, and the board's actions. An action left out
 * isn't offered: exportAs while the board loads, markAllRead while nothing is unread.
 */
export type Here = {
  name: string;
  views: View[];
  tasks: Task[];
  setView: (v: View) => void;
  openTask: (ref: string) => void;
  members: { title: string; show: () => void };
  exportAs?: (a: ExportAction) => void;
  markAllRead?: () => void;
};

/** Page is the page under the finder when it isn't a board; it is left out of the places. */
export type Page = "inbox" | "settings";

/** An item is one place the finder can go: ref is a task's reference, sub a second line. */
type Item = { id: string; label: string; ref?: string; sub?: string; note?: string; words: string; go: () => void };
type Group = { key: string; title: string; items: Item[] };

const viewNames: Record<View, string> = { conversation: "Conversation", tasks: "Tasks", files: "Files" };

function goTo(href: string) {
  window.location.href = href;
}

/**
 * Finder holds the finder for one page: its key works anywhere on the page, a text field
 * included, unless another dialog is open. onKeys shows the page's keys sheet, if it has one.
 */
export function Finder({ boards, here, page, onKeys }: { boards: Board[] | null; here?: Here; page?: Page; onKeys?: () => void }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    const show = () => setOpen(true);
    const key = (e: KeyboardEvent) => {
      if (!pressed("find", e) || e.isComposing) return;
      // Another dialog (the keys sheet, a confirmation) keeps the key while it is open.
      if (document.querySelector("dialog[open]:not(.finder), [role=alertdialog]")) return;
      e.preventDefault();
      setOpen((o) => !o);
    };
    window.addEventListener(findEvent, show);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener(findEvent, show);
      window.removeEventListener("keydown", key);
    };
  }, []);
  if (!open) return null;
  return <Palette groups={groupsOf(boards ?? [], here, page, onKeys)} onClose={() => setOpen(false)} />;
}

// An item's words hold the other names people type for it, such as "members" for People.
function groupsOf(boards: Board[], here: Here | undefined, page: Page | undefined, onKeys: (() => void) | undefined): Group[] {
  const places: Item[] = [
    ...(page === "inbox" ? [] : [{ id: "inbox", label: "Inbox", note: "Asks to you, from every board", words: "inbox asks questions", go: () => goTo("/?inbox") }]),
    ...(here?.views ?? []).map((v) => ({ id: `view-${v}`, label: viewNames[v], note: "This board", words: viewNames[v], go: () => here!.setView(v) })),
    ...(here ? [{ id: "members", label: here.members.title, note: "This board", words: `${here.members.title} agents people members`, go: here.members.show }] : []),
    { id: "people", label: "People", note: "Everyone on this server", words: "people members team users", go: () => goTo("/?view=settings&tab=people") },
    ...(page === "settings" ? [] : [{ id: "settings", label: "Settings", note: "Your profile, agents and theme", words: "settings profile rename handle preferences", go: () => goTo("/?view=settings") }]),
    { id: "settings-agents", label: "Auto mode and urgent messages", note: "Settings", words: "settings auto mode allowance urgent messages mid-turn midturn agents", go: () => goTo("/?view=settings&tab=agents") },
    { id: "settings-appearance", label: "Theme", note: "Settings", words: "settings theme appearance colour color dark light", go: () => goTo("/?view=settings&tab=appearance") },
  ];
  const actions: Item[] = [
    ...(here?.exportAs
      ? exportActions.map((a) => ({ id: `export-${a.action}`, label: a.label, note: "Export this board", words: `${a.label} export download save markdown`, go: () => here.exportAs!(a.action) }))
      : []),
    ...(here?.markAllRead ? [{ id: "read", label: "Mark all as read", note: "This board", words: "mark all as read unread", go: here.markAllRead }] : []),
    ...(onKeys ? [{ id: "keys", label: "Show keys", words: "show keys keyboard shortcuts help", go: onKeys }] : []),
  ];
  // The boards come in the sidebar's order: those that need the person, the rest, then the archived.
  const { needs, others, archived } = sidebarOrder(boards);
  const sorted = [...needs, ...others, ...archived];
  const boardItems: Item[] = sorted.map((b) => ({
    id: `board-${b.id}`,
    label: boardLabel(b),
    sub: b.title ? b.name : undefined,
    note: b.name === here?.name ? "This board" : isArchived(b) ? "Archived" : undefined,
    words: `${boardLabel(b)} ${b.name}`,
    go: () => (b.name === here?.name ? here.setView("conversation") : goTo(`/?board=${encodeURIComponent(b.name)}`)),
  }));
  // Tasks still being worked on come before finished ones.
  const live = (t: Task) => t.state === "open" || t.state === "in_progress";
  const tasks = here ? [...here.tasks.filter(live), ...here.tasks.filter((t) => !live(t))] : [];
  const taskItems: Item[] = tasks.map((t) => ({
    id: `task-${t.id}`,
    label: t.title,
    ref: t.ref,
    note: taskState(t),
    words: `${t.ref} ${t.title}`,
    go: () => here!.openTask(t.ref),
  }));
  return [
    { key: "places", title: "Go to", items: places },
    { key: "boards", title: "Boards", items: boardItems },
    { key: "actions", title: "Do", items: actions },
    { key: "tasks", title: "Tasks on this board", items: taskItems },
  ];
}

/** matches says whether every word of the query is somewhere in the item's words. */
function matches(item: Item, query: string): boolean {
  const words = item.words.toLowerCase();
  return query.toLowerCase().split(/\s+/).filter(Boolean).every((w) => words.includes(w));
}

function Palette({ groups: all, onClose }: { groups: Group[]; onClose: () => void }) {
  const id = useId();
  const dialog = useRef<HTMLDialogElement>(null);
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const groups = all.map((g) => ({ ...g, items: g.items.filter((i) => matches(i, query)) })).filter((g) => g.items.length > 0);
  const items = groups.flatMap((g) => g.items);
  const current = items[Math.min(active, items.length - 1)];
  const optionId = (item: Item) => `${id}-${item.id}`;

  // The finder is a modal dialog, so the page under it can't be reached, and focus goes
  // back to what had it when it closes.
  useLayoutEffect(() => {
    const d = dialog.current!;
    const opener = document.activeElement as HTMLElement | null;
    d.showModal();
    return () => {
      if (d.open) d.close();
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);
  useEffect(() => {
    if (current) document.getElementById(optionId(current))?.scrollIntoView({ block: "nearest" });
  });

  const choose = (item: Item | undefined) => {
    if (!item) return;
    onClose();
    item.go();
  };
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.nativeEvent.isComposing) return;
    const n = items.length;
    if (e.key === "ArrowDown" && n > 0) setActive((a) => (Math.min(a, n - 1) + 1) % n);
    else if (e.key === "ArrowUp" && n > 0) setActive((a) => (Math.min(a, n - 1) - 1 + n) % n);
    else if (e.key === "Enter") choose(current);
    else if (e.key === "Escape") onClose();
    // The input is the finder's only stop, so Tab stays in it.
    else if (e.key !== "Tab") return;
    e.preventDefault();
  };

  const listId = `${id}-list`;
  return createPortal(
    <dialog
      ref={dialog}
      aria-labelledby={`${id}-title`}
      aria-modal="true"
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
      className="finder mx-auto mt-[12vh] hidden max-h-[min(560px,76dvh)] w-[min(560px,calc(100vw-2rem))] flex-col overflow-hidden rounded-box border border-rule bg-surface p-0 text-ink shadow-float open:flex open:animate-fade-in"
    >
      <h2 id={`${id}-title`} className="sr-only">
        Find or do anything
      </h2>
      <div className="flex shrink-0 items-center gap-2.5 border-b border-rule px-4">
        <Search className="size-4 shrink-0 text-muted" strokeWidth={1.75} aria-hidden />
        <label htmlFor={`${id}-input`} className="sr-only">
          Find a board or task, or do something
        </label>
        <input
          id={`${id}-input`}
          role="combobox"
          aria-expanded="true"
          aria-controls={listId}
          aria-autocomplete="list"
          aria-activedescendant={current ? optionId(current) : undefined}
          autoComplete="off"
          spellCheck={false}
          autoFocus
          value={query}
          onChange={(e) => {
            setQuery(e.target.value);
            setActive(0);
          }}
          onKeyDown={onKeyDown}
          placeholder="Find a board or task, or do something"
          className="min-h-13 min-w-0 flex-1 bg-transparent text-body text-ink outline-none placeholder:text-muted"
        />
      </div>
      <div id={listId} role="listbox" aria-label="Results" className="quiet-scroll min-h-0 flex-1 overflow-y-auto overscroll-contain p-1.5">
        {groups.map((g) => (
          <div key={g.key} role="group" aria-labelledby={`${id}-${g.key}`} className="pb-1">
            <div id={`${id}-${g.key}`} role="presentation" className="px-2.5 pt-2 pb-1 text-meta font-bold text-muted">
              {g.title}
            </div>
            {g.items.map((item) => {
              const selected = item === current;
              return (
                <div
                  key={item.id}
                  id={optionId(item)}
                  role="option"
                  aria-selected={selected}
                  onMouseMove={() => !selected && setActive(items.indexOf(item))}
                  onClick={() => choose(item)}
                  className={cn("flex min-h-10 cursor-pointer items-center gap-3 rounded-control px-2.5 py-1.5", selected && "bg-selected")}
                >
                  {item.ref && <span className="shrink-0 text-meta text-muted tabular-nums">{item.ref}</span>}
                  <span className="flex min-w-0 flex-1 flex-col">
                    <span className={cn("truncate", selected && "font-bold")}>{item.label}</span>
                    {item.sub && <span className="truncate text-meta text-muted">{item.sub}</span>}
                  </span>
                  {item.note && <span className="shrink-0 text-meta text-muted">{item.note}</span>}
                </div>
              );
            })}
          </div>
        ))}
        {items.length === 0 && (
          <p role="status" className="px-2.5 py-6 text-center text-muted">
            Nothing matches &ldquo;{query.trim()}&rdquo;.
          </p>
        )}
      </div>
      <p className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-1 border-t border-rule px-4 py-2 text-meta text-muted pointer-coarse:hidden">
        <span className="inline-flex items-center gap-1.5">
          <Kbd>↑</Kbd>
          <Kbd>↓</Kbd>to move
        </span>
        <span className="inline-flex items-center gap-1.5">
          <Kbd>↵</Kbd>to go or do
        </span>
        <span className="inline-flex items-center gap-1.5">
          <Kbd>Esc</Kbd>to close
        </span>
      </p>
    </dialog>,
    document.body,
  );
}
