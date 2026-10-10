"use client";

// EXPERIMENTAL, lab only: a notice about an invite one of the person's agents made, under
// their allowance or an approval. It says which agent, when, whether the invite is still
// open, used, revoked or expired, and until when it works, with Revoke while it is open.
// Like the API's notice, it never shows the invite's secret, who it was for or the boards
// it carries: anyone who can see the Inbox can see this, and the secret is the access.

import { Button } from "@/components/ui/button";
import type { InviteNotice } from "../../onboarding";
import { at, scenario } from "../../store";
import { ago, useNow } from "../common";
import { AgentTitle, agentRow, boardRow } from "./who";
import { Command, Done, Fields } from "./parts";
import { revoke } from "./state";
import { commands, until } from "./words";

export const noticeState: Record<InviteNotice["state"], string> = {
  active: "Open",
  redeemed: "Used",
  revoked: "Revoked",
  expired: "Expired",
};

/** noticeLine is a notice's state and time in a few words, for its row. */
export function noticeLine(n: InviteNotice, now: number): string {
  if (n.state === "active") return `Open · works until ${until(at(n.expires))}`;
  if (n.state === "redeemed") return "Used: someone joined with it";
  if (n.state === "revoked") return `Revoked · made ${ago(n.t, now)}`;
  return `Expired ${ago(n.expires, now)}, unused`;
}

export function NoticeDetail({ n }: { n: InviteNotice }) {
  const now = useNow();
  return (
    <article className="ob-notice flex max-w-[640px] flex-col gap-5" data-notice={n.id}>
      <p className="text-meta text-muted">Invite notice · {ago(n.t, now)}</p>
      <AgentTitle agent={n.agent} rest="invited someone" />
      <Fields
        rows={[
          agentRow(n.agent, scenario.me),
          boardRow("not named in invite notices", true),
          ["State", noticeState[n.state]],
          ["Made", `${ago(n.t, now)} by ${n.agent}`],
          [n.state === "expired" ? "Ended" : "Works until", until(at(n.expires))],
          ["Invite", <span key="i" className="font-mono text-[13px]">{n.id}</span>],
        ]}
      />
      {n.state === "active" && (
        <div className="flex flex-col gap-3">
          <p className="text-muted">The invite makes one new account on this server, as a member. Revoke it if you didn&apos;t expect it, or it went to the wrong person.</p>
          <div>
            <Button variant="secondary" onClick={() => revoke(n.id)}>
              Revoke the invite
            </Button>
          </div>
          <Command label="Or from your terminal" command={commands.revoke(n.id)} />
        </div>
      )}
      {n.state === "redeemed" && <Done>Someone joined the server with this invite. It can&apos;t be used again.</Done>}
      {n.state === "revoked" && <Done muted>Revoked. Nobody can join with it.</Done>}
      {n.state === "expired" && <Done muted>Expired before anyone used it. Nobody can join with it.</Done>}
      <p className="text-meta text-muted">The invite&apos;s link and who it went to stay with {n.agent}&apos;s session; aboard never shows them here.</p>
    </article>
  );
}
