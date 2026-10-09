"use client";

// The mid-turn policy (D221): whether urgent messages from a person's own agents reach
// their agents at the next step instead of waiting for the turn to end. One shared copy
// of the person's setting feeds the account menu, the agent's menu and its details.

import { useEffect, useSyncExternalStore } from "react";
import { type MidturnPolicy, type MidturnView, getMidturn, setMidturn } from "./api";

export const midturnLabels: Record<MidturnPolicy, string> = { "owner-only": "Owner only", "my-agents": "My agents" };
export const midturnPolicies: MidturnPolicy[] = ["owner-only", "my-agents"];
export const midturnCopy = "Urgent messages from your own agents arrive at their next step instead of waiting for the turn to end.";

type State = { view: MidturnView | null; unavailable: boolean };

// view is null until read; unavailable is true once a server has no such setting.
let state: State = { view: null, unavailable: false };
let loading: Promise<void> | null = null;
const listeners = new Set<() => void>();
const publish = (next: State) => {
  state = next;
  listeners.forEach((l) => l());
};

function load(): Promise<void> {
  loading ??= getMidturn().then(
    (view) => publish({ view, unavailable: false }),
    () => {
      loading = null;
      publish({ view: null, unavailable: true });
    },
  );
  return loading;
}

/** useMidturn is the person's policy, read once and shared; unavailable on a server without it. */
export function useMidturn(): State {
  useEffect(() => void load(), []);
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => void listeners.delete(l);
    },
    () => state,
    () => state,
  );
}

/** changeMidturn sets the default (no member) or an own agent's override (null clears it) and shares the result. */
export async function changeMidturn(policy: MidturnPolicy | null, memberId?: string): Promise<void> {
  // The write answers with the one policy it set and no overrides list, so read the whole view back.
  await setMidturn(policy, memberId);
  publish({ view: await getMidturn(), unavailable: false });
}

/** effectiveMidturn is the policy an own agent follows now, and whether its own override sets it. */
export function effectiveMidturn(view: MidturnView, memberId: string): { policy: MidturnPolicy; overridden: boolean } {
  const o = view.overrides?.find((x) => x.member_id === memberId);
  return o ? { policy: o.policy, overridden: true } : { policy: view.policy, overridden: false };
}
