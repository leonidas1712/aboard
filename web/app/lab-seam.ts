// The seam the UI lab plugs into (web/lab, `make lab`). These are only types: a real
// build resolves "aboard-lab" to lab-off.ts, whose lab is null, so the UI draws exactly
// what it always does. The lab build resolves it to web/lab/entry.tsx, which fakes the
// API underneath the page and fills these slots with experimental views fed only by its
// own data. Nothing here is part of the API or the product yet.

import type { ComponentType, ReactNode } from "react";
import type { Board, Member, MemberRef, Message } from "./api";

export type Lab = {
  /** Overlay is the lab's own floating controls, drawn over every screen. */
  Overlay: ComponentType;
  /** place names a place of the lab's own the address asks for (such as an inbox), or null. */
  place?: () => string | null;
  /** Place draws that place, instead of a board or the list of boards. */
  Place?: ComponentType<{ onSignOut: () => void }>;
  /** Nav replaces the list of boards in the left panel. */
  Nav?: ComponentType<{ current: string | null; boards: Board[] | null }>;
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
  /** RightTitle heads the board panel instead of the board's name. */
  RightTitle?: ComponentType<{ board: string; fallback: string }>;
  /**
   * RightPanel replaces the board panel's content. boardPanel is the real one without
   * its list of agents (the charter, the rules and the details).
   */
  RightPanel?: ComponentType<{
    board: string;
    members: Member[];
    identity: (m: MemberRef) => number;
    /** pick filters the timeline to one member's messages, as their name does. */
    pick: (name: string) => void;
    boardPanel: ReactNode;
  }>;
  /** MessageFooter adds to a message in the timeline, under its text. */
  MessageFooter?: ComponentType<{ board: string; message: Message }>;
  /** Text draws the plain text of a message, between its mentions. */
  Text?: ComponentType<{ text: string }>;
};
