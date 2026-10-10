"use client";

// PairingLine is one quiet line under the board's "Now:" line while a pairing request on
// this board, from or to the person, is under way, and once it is ready: "Waiting for
// sam's agent to come online", "Verifying delivery with sam's agent…", then "Ready: both
// agents connected, delivery verified". The words come from the request's state, so it
// never says verified before the server does. A link opens the request in the Inbox.
// Pairing doesn't always move the board's head, so it reads again every 10 seconds while
// a request is live, and whenever the board's head moves (the board view's own stream).

import { Check, Clock, LoaderCircle } from "lucide-react";
import { useEffect, useState } from "react";
import { type Me, get } from "./api";
import { type PairingRequest, type Person, listPairing, listPeople } from "./onboarding-api";
import { live, pairingLine } from "./onboarding-words";

export function PairingLine({ boardId, head }: { boardId: string | undefined; head: number | undefined }) {
  const [requests, setRequests] = useState<PairingRequest[]>([]);
  const [me, setMe] = useState<Me | null>(null);
  const [people, setPeople] = useState<Person[]>([]);
  useEffect(() => {
    if (!boardId) return;
    let alive = true;
    let underWay = false;
    const load = () => {
      listPairing().then((r) => {
        if (!alive) return;
        const here = r.requests.filter((p) => p.board_id === boardId);
        underWay = here.some((p) => live(p.state));
        setRequests(here);
      }, () => {});
    };
    load();
    const tick = setInterval(() => underWay && document.visibilityState === "visible" && load(), 10_000);
    return () => {
      alive = false;
      clearInterval(tick);
    };
  }, [boardId, head]);
  useEffect(() => {
    let alive = true;
    get<Me>("/v1/me").then((m) => alive && setMe(m), () => {});
    listPeople().then((r) => alive && setPeople(r.people), () => {});
    return () => {
      alive = false;
    };
  }, []);
  if (!me) return null;
  const p = requests
    .filter((x) => (x.inviter_id === me.id || x.recipient_id === me.id) && (live(x.state) || x.state === "ready"))
    .sort((a, b) => b.created_at.localeCompare(a.created_at))[0];
  if (!p) return null;
  const mine = p.inviter_id === me.id;
  const other = mine ? (people.find((x) => x.id === p.recipient_id)?.handle ?? "the person you asked") : (p.display?.person_handle ?? "the inviter");
  const ready = p.state === "ready";
  const icon = ready ? (
    <Check className="size-4 text-accent-strong" strokeWidth={2} aria-hidden />
  ) : p.state === "verifying" ? (
    <LoaderCircle className="size-4 text-muted motion-safe:animate-spin motion-safe:[animation-duration:2.4s]" strokeWidth={1.75} aria-hidden />
  ) : (
    <Clock className="size-4 text-muted" strokeWidth={1.75} aria-hidden />
  );
  return (
    <p role="status" aria-live="polite" className="pairing-line flex flex-wrap items-center gap-x-2 gap-y-0.5 pb-2 text-meta" data-state={p.state}>
      <span className="inline-flex items-center gap-2">
        {icon}
        <span className={ready ? "text-ink" : "text-muted"}>{pairingLine(p, mine, other, null)}</span>
      </span>
      <span className="text-faint max-sm:hidden" aria-hidden>
        ·
      </span>
      <a href={`/?inbox&item=${encodeURIComponent(p.id)}`} className="text-muted">
        Pairing request
      </a>
    </p>
  );
}
