// The Inbox's and the board view's keys, in one table. The handlers match keys through
// `pressed`, the inline hints print `label`, and the "?" sheet lists `what`, so a hint
// never names a key the handler doesn't have.

export type KeyId = "next" | "previous" | "accept" | "option" | "reply" | "send" | "leave" | "snooze" | "open" | "inbox" | "back" | "help";
/** Where a key works: in the Inbox or on a board. */
export type Place = "inbox" | "board";
type Key = { id: KeyId; keys: string[]; label: string; what: string; places: Place[] };

export const allKeys: Key[] = [
  { id: "next", keys: ["j", "ArrowDown"], label: "J", what: "Move to the next ask. The down arrow does the same.", places: ["inbox"] },
  { id: "previous", keys: ["k", "ArrowUp"], label: "K", what: "Move to the previous ask. The up arrow does the same.", places: ["inbox"] },
  { id: "option", keys: ["1", "2", "3", "4"], label: "1–4", what: "Answer with that option.", places: ["inbox"] },
  { id: "accept", keys: ["e"], label: "E", what: "Let the agent go ahead with what it proposed.", places: ["inbox"] },
  { id: "reply", keys: ["r"], label: "R", what: "Write your own answer.", places: ["inbox"] },
  { id: "send", keys: ["mod+Enter"], label: "mod+↵", what: "Send the answer you wrote.", places: ["inbox"] },
  { id: "leave", keys: ["Escape"], label: "Esc", what: "Leave the answer box without sending.", places: ["inbox"] },
  { id: "snooze", keys: ["l"], label: "L", what: "Snooze the ask for an hour, on this browser only.", places: ["inbox"] },
  { id: "open", keys: ["Enter"], label: "↵", what: "Open the ask on its board.", places: ["inbox"] },
  { id: "inbox", keys: ["i"], label: "I", what: "Go to the Inbox.", places: ["board"] },
  { id: "back", keys: ["Escape"], label: "Esc", what: "Go back to the Inbox, when you opened this board from it.", places: ["board"] },
  { id: "help", keys: ["?"], label: "?", what: "Show these keys.", places: ["inbox", "board"] },
];

/** keysAt lists the keys that work in a place. */
export function keysAt(place: Place): Key[] {
  return allKeys.filter((k) => k.places.includes(place));
}

/** keyLabel is how a key is printed: ⌘ on a Mac, Ctrl elsewhere. */
export function keyLabel(id: KeyId): string {
  const label = allKeys.find((k) => k.id === id)!.label;
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  return label.replace("mod+", mac ? "⌘" : "Ctrl+");
}

/** pressed says whether the event is one of the key's presses. */
export function pressed(id: KeyId, e: KeyboardEvent | React.KeyboardEvent): boolean {
  const key = allKeys.find((k) => k.id === id)!;
  const mod = e.metaKey || e.ctrlKey;
  return key.keys.some((k) => k === "mod+Enter" ? mod && e.key === "Enter" : !mod && !e.altKey && (e.key === k || (k.length === 1 && e.key === k.toUpperCase() && k !== k.toUpperCase())));
}

/** typing says whether a key press belongs to a field, menu or dialog rather than the page. */
export function typing(e: KeyboardEvent): boolean {
  return Boolean((e.target as HTMLElement | null)?.closest?.("input, textarea, select, [contenteditable], [role=dialog], [role=menu], dialog[open]"));
}

/** The ask the Inbox had selected when a board was opened from it, so the Inbox can return to it. */
export type InboxReturn = { id: string; slot: number; scroll: number };
const returnKey = "aboard.inbox.return";

/** rememberInbox keeps the Inbox's selection and scroll for this tab, for the way back. */
export function rememberInbox(state: InboxReturn) {
  try { sessionStorage.setItem(returnKey, JSON.stringify(state)); } catch { /* The Inbox opens at its first ask instead. */ }
}

/** takeInbox returns what rememberInbox kept, once. */
export function takeInbox(): InboxReturn | null {
  try {
    const raw = sessionStorage.getItem(returnKey);
    sessionStorage.removeItem(returnKey);
    const v = raw ? (JSON.parse(raw) as InboxReturn) : null;
    return v && typeof v.id === "string" && Number.isFinite(v.slot) && Number.isFinite(v.scroll) ? v : null;
  } catch {
    return null;
  }
}
