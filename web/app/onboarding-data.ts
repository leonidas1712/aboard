"use client";

// useOnboarding reads what onboarding shows the person: their approvals (pending and
// decided), notices about invites their agents made, pairing requests, their allowance,
// their own agents and the people on the server, with the labels that name them. Invites
// and pairing don't move a board's head, so it reads again when the Inbox opens, on any
// stream event, and every 20 seconds while the page is visible.

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { type Board, type Me, type Member, follow, get } from "./api";
import {
  type Allowance,
  type Approval,
  type InviteNotice,
  type OnboardingInbox,
  type PairingRequest,
  type Person,
  getAllowance,
  getOnboardingInbox,
  listApprovals,
  listInviteNotices,
  listOwnAgents,
  listPairing,
  listPeople,
} from "./onboarding-api";
import type { Names } from "./onboarding-words";

export type Onboarding = {
  approvals: Approval[];
  notices: InviteNotice[];
  /** inbox is the board adds and arrivals; empty on a server that does not answer it. */
  inbox: OnboardingInbox;
  pairing: PairingRequest[];
  allowance: Allowance | null;
  agents: Member[];
  names: Names;
  /** loaded is false until the first read answers; error is the last read's problem, if any. */
  loaded: boolean;
  error: unknown;
  refresh: () => void;
};

const noInbox: OnboardingInbox = { board_adds: [], arrivals: [] };

const settle = <T,>(p: Promise<T>): Promise<T | null> => p.then((v) => v, () => null);

export function useOnboarding({ follows = true }: { follows?: boolean } = {}): Onboarding {
  const [approvals, setApprovals] = useState<Approval[]>([]);
  const [notices, setNotices] = useState<InviteNotice[]>([]);
  const [inbox, setInbox] = useState<OnboardingInbox>(noInbox);
  const [pairing, setPairing] = useState<PairingRequest[]>([]);
  const [allowance, setAllowance] = useState<Allowance | null>(null);
  const [agents, setAgents] = useState<Member[]>([]);
  const [people, setPeople] = useState<Person[]>([]);
  const [boards, setBoards] = useState<Board[]>([]);
  const [me, setMe] = useState<Me | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const generation = useRef(0);
  const load = useCallback(async () => {
    const gen = ++generation.current;
    // Each part answers on its own: a server or account without one of them still shows the rest.
    const [a, n, ib, p, al, ag, pe, b, m] = await Promise.all([
      listApprovals("all").then((r) => r.approvals, (e: unknown) => (setError(e), null)),
      settle(listInviteNotices().then((r) => r.notices)),
      settle(getOnboardingInbox()),
      settle(listPairing().then((r) => r.requests)),
      settle(getAllowance()),
      settle(listOwnAgents().then((r) => r.agents)),
      settle(listPeople().then((r) => r.people)),
      settle(get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => r.boards)),
      settle(get<Me>("/v1/me")),
    ]);
    if (gen !== generation.current) return;
    if (a) {
      setApprovals(a);
      setError(null);
    }
    if (n) setNotices(n);
    setInbox(ib ? { board_adds: ib.board_adds ?? [], arrivals: ib.arrivals ?? [] } : noInbox);
    if (p) setPairing(p);
    setAllowance(al);
    if (ag) setAgents(ag);
    if (pe) setPeople(pe);
    if (b) setBoards(b);
    if (m) setMe(m);
    setLoaded(true);
  }, []);
  useEffect(() => {
    void load();
    const tick = setInterval(() => {
      if (document.visibilityState === "visible") void load();
    }, 20_000);
    const stop = follows ? follow({ head: () => void load(), presence: () => void load(), unavailable: () => void load(), error: () => {} }) : () => {};
    const again = () => void load();
    window.addEventListener(onboardingChanged, again);
    return () => {
      clearInterval(tick);
      stop();
      window.removeEventListener(onboardingChanged, again);
    };
  }, [load, follows]);
  const names = useMemo<Names>(
    () => ({
      me: me ? { id: me.id, name: me.name } : null,
      person: (id) => (id ? (people.find((x) => x.id === id)?.handle ?? (me?.id === id ? me.name : null)) : null),
      board: (id) => (id ? (boards.find((x) => x.id === id)?.name ?? null) : null),
    }),
    [people, boards, me],
  );
  return { approvals, notices, inbox, pairing, allowance, agents, names, loaded, error, refresh: () => void load() };
}

/**
 * useOnboardingCount is how many approvals and pairing requests wait on the person, for
 * the Inbox's count beside the boards. It reads again every 30 seconds and after a write.
 */
export function useOnboardingCount(): number {
  const [n, setN] = useState(0);
  useEffect(() => {
    let alive = true;
    const load = async () => {
      const [a, p, m] = await Promise.all([settle(listApprovals("pending")), settle(listPairing()), settle(get<Me>("/v1/me"))]);
      if (!alive) return;
      const waiting = (p?.requests ?? []).filter((r) => m && r.recipient_id === m.id && r.state === "awaiting_session" && !r.chosen_recipient_agent_id).length;
      setN((a?.approvals.filter((x) => x.state === "pending").length ?? 0) + waiting);
    };
    void load();
    const tick = setInterval(() => document.visibilityState === "visible" && void load(), 30_000);
    const again = () => void load();
    window.addEventListener(onboardingChanged, again);
    return () => {
      alive = false;
      clearInterval(tick);
      window.removeEventListener(onboardingChanged, again);
    };
  }, []);
  return n;
}

/** onboardingChanged is the event a write sends so every view of onboarding reads again. */
export const onboardingChanged = "aboard:onboarding-changed";

/** changed tells every view of onboarding on the page to read again. */
export const changed = () => window.dispatchEvent(new Event(onboardingChanged));
