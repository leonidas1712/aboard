// The seam the UI lab plugs into (web/lab, `make lab`). These are only types: a real
// build resolves "aboard-lab" to lab-off.ts, whose lab is null, so the board view draws
// exactly what it always does. The lab build resolves it to web/lab/entry.tsx, which
// fakes the API underneath the page and fills these slots with experimental views fed
// only by its own data. Nothing here is part of the API or the product yet.

import type { ComponentType, ReactNode } from "react";
import type { Member, MemberRef, Message } from "./api";

export type Lab = {
  /** Overlay is the lab's own floating controls, drawn over every screen. */
  Overlay: ComponentType;
  /**
   * Centre wraps the board view's conversation (the timeline and the message box)
   * under the "Now:" line, so an experiment can add a view beside it.
   */
  Centre?: ComponentType<{
    board: string;
    members: Member[];
    identity: (m: MemberRef) => number;
    /** onShow opens a message's thread in the timeline and scrolls to it. */
    onShow: (id: string) => void;
    children: ReactNode;
  }>;
  /**
   * Agents lays out the agents in the board panel, given how the panel draws one (item)
   * and an agent's fields alone (details: owner, role, harness, delivery).
   */
  Agents?: ComponentType<{
    board: string;
    agents: Member[];
    item: (a: Member) => ReactNode;
    details: (a: Member) => ReactNode;
    /** pick filters the timeline to one member's messages, as their name does. */
    pick: (name: string) => void;
  }>;
  /** BoardListTop sits above the list of boards. */
  BoardListTop?: ComponentType;
  /** MessageFooter adds to a message in the timeline, under its text. */
  MessageFooter?: ComponentType<{ board: string; message: Message }>;
};
