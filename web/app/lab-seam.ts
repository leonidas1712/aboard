// The seam the UI lab plugs into (web/lab, `make lab`). These are only types: a real
// build resolves "aboard-lab" to lab-off.ts, whose lab is null, so the board view draws
// exactly what it always does. The lab build resolves it to web/lab/entry.tsx, which
// fakes the API underneath the page and fills these slots with experimental views fed
// only by its own data. Nothing here is part of the API or the product yet.

import type { ComponentType, ReactNode } from "react";
import type { Member, MemberRef } from "./api";

export type Lab = {
  /** Overlay is the lab's own floating controls, drawn over every screen. */
  Overlay: ComponentType;
  /**
   * Centre wraps the board view's conversation (the timeline and the message box)
   * under the "Now:" line, so an experiment can add a view beside it.
   */
  Centre?: ComponentType<{ board: string; members: Member[]; identity: (m: MemberRef) => number; children: ReactNode }>;
  /** Agents lays out the agents in the board panel, given how the panel draws one. */
  Agents?: ComponentType<{ board: string; agents: Member[]; item: (a: Member) => ReactNode }>;
  /** AgentLine adds a line under an agent's name in the board panel. */
  AgentLine?: ComponentType<{ board: string; agent: Member }>;
};
