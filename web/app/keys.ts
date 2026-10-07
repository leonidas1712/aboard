// The Inbox's keys, in one table. The handlers match keys through `pressed`, the inline
// hints print `label`, and the "?" sheet lists `what`, so a hint never names a key the
// handler doesn't have.

export type KeyId = "next" | "previous" | "accept" | "option" | "reply" | "send" | "leave" | "snooze" | "open" | "help";
type Key = { id: KeyId; keys: string[]; label: string; what: string };

export const inboxKeys: Key[] = [
  { id: "next", keys: ["j", "ArrowDown"], label: "J", what: "Move to the next ask. The down arrow does the same." },
  { id: "previous", keys: ["k", "ArrowUp"], label: "K", what: "Move to the previous ask. The up arrow does the same." },
  { id: "option", keys: ["1", "2", "3", "4"], label: "1–4", what: "Answer with that option." },
  { id: "accept", keys: ["e"], label: "E", what: "Let the agent go ahead with what it proposed." },
  { id: "reply", keys: ["r"], label: "R", what: "Write your own answer." },
  { id: "send", keys: ["mod+Enter"], label: "mod+↵", what: "Send the answer you wrote." },
  { id: "leave", keys: ["Escape"], label: "Esc", what: "Leave the answer box without sending." },
  { id: "snooze", keys: ["l"], label: "L", what: "Snooze the ask for an hour, on this browser only." },
  { id: "open", keys: ["Enter"], label: "↵", what: "Open the ask on its board." },
  { id: "help", keys: ["?"], label: "?", what: "Show these keys." },
];

/** keyLabel is how a key is printed: ⌘ on a Mac, Ctrl elsewhere. */
export function keyLabel(id: KeyId): string {
  const label = inboxKeys.find((k) => k.id === id)!.label;
  const mac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);
  return label.replace("mod+", mac ? "⌘" : "Ctrl+");
}

/** pressed says whether the event is one of the key's presses. */
export function pressed(id: KeyId, e: KeyboardEvent | React.KeyboardEvent): boolean {
  const key = inboxKeys.find((k) => k.id === id)!;
  const mod = e.metaKey || e.ctrlKey;
  return key.keys.some((k) => k === "mod+Enter" ? mod && e.key === "Enter" : !mod && !e.altKey && (e.key === k || (k.length === 1 && e.key === k.toUpperCase() && k !== k.toUpperCase())));
}

/** typing says whether a key press belongs to a field, menu or dialog rather than the Inbox. */
export function typing(e: KeyboardEvent): boolean {
  return Boolean((e.target as HTMLElement | null)?.closest?.("input, textarea, select, [contenteditable], [role=dialog], [role=menu], dialog[open]"));
}
