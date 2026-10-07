"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogTitle } from "@/components/ui/alert-dialog";
import { Account } from "./account";
import { type Board, type Me, type Member, get } from "./api";
import { CopyButton } from "./board-details";
import { Header, Problem } from "./chrome";

export type ServerPerson = { id: string; handle: string; display_name: string | null; server_role: "admin" | "member" | "guest" };
type Shared = { boards: Board[]; agents: number };
type Handoff = { title: string; explanation: string; command: string };

/** shellWord quotes a value as one argument in the commands handed to a terminal. */
export function shellWord(value: string): string {
  return /^[a-zA-Z0-9_@./:-]+$/.test(value) ? value : `'${value.replaceAll("'", "'\\''")}'`;
}

/** People lists server identities and facts from boards the reader shares. */
export default function People({ onSignOut }: { onSignOut: () => void }) {
  const [people, setPeople] = useState<ServerPerson[] | null>(null);
  const [me, setMe] = useState<Me | null>(null);
  const [shared, setShared] = useState<Record<string, Shared> | null>(null);
  const [error, setError] = useState<unknown>(null);
  const [factsError, setFactsError] = useState(false);
  const [handoff, setHandoff] = useState<Handoff | null>(null);
  useEffect(() => {
    let live = true;
    const load = async () => {
      const [who, all, list] = await Promise.all([
        get<Me>("/v1/me"), get<{ people: ServerPerson[] }>("/v1/people"),
        get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }),
      ]);
      if (!live) return;
      setMe(who);
      setPeople(all.people);
      const facts: Record<string, Shared> = Object.fromEntries(all.people.map((p) => [p.id, { boards: [], agents: 0 }]));
      const reads = await Promise.allSettled(list.boards.filter((b) => b.on_board).map(async (b) => {
        const path = `/v1/boards/${encodeURIComponent(b.name)}`;
        const [members, persons] = await Promise.all([
          get<{ members: Member[] }>(`${path}/members`), get<{ people: { id: string }[] }>(`${path}/people`),
        ]);
        return { board: b, members: members.members, persons: persons.people };
      }));
      if (!live) return;
      for (const r of reads) if (r.status === "fulfilled") {
        for (const p of r.value.persons) if (facts[p.id]) {
          facts[p.id].boards.push(r.value.board);
          facts[p.id].agents += r.value.members.filter((m) => m.kind === "agent" && m.owner_id === p.id && (!m.status || m.status === "active")).length;
        }
      }
      setFactsError(reads.some((r) => r.status === "rejected"));
      setShared(facts);
    };
    void load().catch((e) => { if (live) setError(e); });
    return () => { live = false; };
  }, []);
  const server = typeof window === "undefined" ? "" : shellWord(window.location.origin);
  const admin = me?.server_role === "admin";
  return <div className="flex min-h-dvh flex-col">
    <Header account={<Account onSignOut={onSignOut} />} />
    <main className="mx-auto w-full max-w-[1100px] px-4 py-8 sm:px-6">
      <a href="/" className="inline-flex min-h-11 items-center text-link">Your boards</a>
      <div className="mb-6 flex flex-wrap items-start justify-between gap-4">
        <div><h1 className="text-headline font-bold">People</h1><p className="text-muted">People on this server</p></div>
        {admin && <Button onClick={() => setHandoff({ title: "Invite someone", explanation: "Run this in your terminal to make an invite link. The link lets one person join this server as a member.", command: `aboard invite --server ${server}` })}>Invite command</Button>}
      </div>
      {error !== null && <Problem error={error} />}
      {!people && error === null && <p role="status">Loading people…</p>}
      {people?.length === 0 && <p>No people are listed on this server.</p>}
      {people && <table className="people w-full border-collapse text-left max-md:block">
        <thead className="text-meta text-muted max-md:sr-only"><tr className="border-b border-rule"><th scope="col" className="py-3 pr-4">Person</th><th scope="col" className="pr-4">Role</th><th scope="col" className="pr-4">Agents on shared boards</th><th scope="col" className="pr-4">Boards in common</th>{admin && <th scope="col">Admin commands</th>}</tr></thead>
        <tbody className="max-md:block">{people.map((p) => <tr key={p.id} className="border-b border-rule max-md:block max-md:py-4">
          <td className="py-4 pr-4 align-top max-md:block max-md:py-1"><strong>{p.display_name || p.handle}</strong><span className="block text-muted">@{p.handle}{p.id === me?.id ? " (you)" : ""}</span></td>
          <td className="py-4 pr-4 align-top capitalize max-md:block max-md:py-1"><span className="md:sr-only">Role: </span>{p.server_role[0].toUpperCase() + p.server_role.slice(1)}</td>
          <td className="py-4 pr-4 align-top max-md:block max-md:py-1"><span className="md:sr-only">Agents on shared boards: </span>{shared === null ? "Loading…" : factsError ? "Unavailable" : shared[p.id]?.agents ?? 0}</td>
          <td className="py-4 pr-4 align-top max-md:block max-md:py-1">{shared === null ? "Loading…" : factsError ? "Unavailable" : shared[p.id]?.boards.length ? <ul>{shared[p.id].boards.map((b) => <li key={b.id}><a href={`/?board=${encodeURIComponent(b.name)}`} className="inline-flex min-h-11 items-center text-link">{b.title || b.name}</a></li>)}</ul> : "None"}</td>
          {admin && <td className="py-4 align-top max-md:block max-md:py-1"><div className="flex flex-wrap gap-2">
            {p.server_role !== "guest" && <Button variant="secondary" onClick={() => setHandoff({ title: `Change @${p.handle}’s role`, explanation: `Run this in your terminal to make @${p.handle} ${p.server_role === "admin" ? "a member again" : "a server admin who can invite and remove people"}. The server keeps at least one admin.`, command: `aboard people role ${shellWord(`@${p.handle}`)} ${p.server_role === "admin" ? "member" : "admin"} --server ${server}` })}>Role command</Button>}
            <Button variant="secondary" onClick={() => setHandoff({ title: `Remove @${p.handle} from the server`, explanation: "Run this in your terminal. Removal is final: their keys, browser sessions and agents stop working. Their messages stay in the record. Review the terminal’s confirmation before removing them.", command: `aboard people remove ${shellWord(`@${p.handle}`)} --server ${server}` })}>Remove command</Button>
          </div></td>}
        </tr>)}</tbody>
      </table>}
      <p className="mt-4 text-meta text-muted">Agent counts cover seats on boards you share, including archived boards.</p>
      {factsError && <p role="status" className="mt-3">Some shared boards could not be read. Reload this page to try again.</p>}
    </main>
    <AlertDialog open={handoff !== null} onOpenChange={(open) => { if (!open) setHandoff(null); }}><AlertDialogContent>
      <AlertDialogTitle>{handoff?.title}</AlertDialogTitle><AlertDialogDescription>{handoff?.explanation}</AlertDialogDescription>
      <code className="break-all rounded-control border border-rule p-3 text-meta">{handoff?.command}</code>
      <AlertDialogFooter>{handoff && <CopyButton text={handoff.command} label="Copy command" variant="primary" />}<AlertDialogCancel asChild><Button variant="secondary">Close</Button></AlertDialogCancel></AlertDialogFooter>
    </AlertDialogContent></AlertDialog>
  </div>;
}
