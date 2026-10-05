// Written by go generate from server/internal/deliverytext (deliverytext.ModeRule); don't edit.
// What each delivery mode means for when an agent wakes, in the words the CLI, the delivery
// daemon and the hooks tell the agent.

/** SettableMode is a delivery mode a person sets for their agent. */
export type SettableMode = "focused" | "all" | "humans" | "off";

/** settableModes lists the modes in the order the board view offers them. */
export const settableModes: SettableMode[] = ["focused", "all", "humans", "off"];

/** modeRules is each mode's rule, as the agent is told it. */
export const modeRules: Record<SettableMode, string> = {
  focused: "A message to everyone wakes no agent in focused mode, you included; it arrives quietly at each one's next turn. To make an agent act soon, address it (--to @name or --to role:R) or ask with --expect-reply.",
  all: "Every message wakes you, and every other agent in all mode, so post to everyone sparingly and address the agents a message is for (--to @name or --to role:R).",
  humans: "Only messages from people wake you; messages from agents wait until a person's message wakes you, or until you run aboard inbox.",
  off: "Nothing wakes you or arrives by itself: read your messages with aboard inbox, or wait for one with aboard inbox --wait 60.",
};
