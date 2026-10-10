"use client";

// EXPERIMENTAL, lab only: an approval in the Inbox ("ask me"). An agent asked the server
// to do admin work for its person; the person's allowance didn't cover it, so the server
// holds the exact request here. The card says what the agent wants in a sentence, which
// agent and when, the exact details, and three choices: Allow once, Allow always (which
// also turns that kind of action on in the person's allowance) and Decline. Actions that
// can never be in an allowance (roles, removing people, keys, a board's rules) have no
// Allow always and say why. Allow always on an invite warns first: a tricked agent could
// invite an outsider, who could then read every open board. The same choice is one
// command in a terminal, shown filled in.

import { useState } from "react";
import { Button } from "@/components/ui/button";
import type { Approval } from "../../onboarding";
import { inviteWarning } from "../../onboarding";
import { at, labHref, scenario } from "../../store";
import { ago, useNow } from "../common";
import { Command, Done, Fields, Warning } from "./parts";
import { decide, useOnboarding } from "./state";
import { AgentTitle, agentRow, boardRow } from "./who";
import { alwaysAsks, byline, commands, did, serverWide, until, wantsRest } from "./words";

export function ApprovalDetail({ a, onShowInvite }: { a: Approval; onShowInvite?: (id: string) => void }) {
  const now = useNow();
  const o = useOnboarding();
  const [confirming, setConfirming] = useState(false);
  const why = alwaysAsks(a.action);
  const invite = a.action.kind === "invite_people";
  const act = a.action;
  // What it wants (the title), then Agent and Board, then the action's own fields.
  const rows: [string, React.ReactNode][] = [agentRow(a.agent, scenario.me), boardRow(a.board, serverWide(act))];
  if (act.kind === "invite_people") {
    rows.push(["Invite", `One new person, as a member. The invite works once, for ${act.ttl_hours} hours.`]);
    if (act.boards.length) rows.push(["Joins", `${act.boards.join(", ")}, as a member`]);
    if (act.pairing) rows.push(["Pairing", <span key="p">{a.agent} asks the new person&apos;s agent to pair on: &ldquo;{act.pairing.work}&rdquo;</span>]);
  } else if (act.kind === "add_people") {
    rows.push(["Person", act.person], ["Change", `joins ${act.board} as a member`]);
  } else if (act.kind === "set_server_role") {
    rows.push(["Person", act.person], ["Change", act.role === "admin" ? "member to server admin: can invite and remove people and manage every board's people" : "admin to member"]);
  } else if (act.kind === "set_board_role") {
    rows.push(["Person", act.person], ["Change", `member to ${act.role} of ${act.board}`]);
  } else if (act.kind === "remove_person") {
    rows.push(["Person", act.person], ["Change", act.board ? `leaves ${act.board}` : "leaves the server, and every board on it"]);
  } else if (act.kind === "revoke_key") {
    rows.push(["Person", act.person], ["Change", `key ${act.key_name} stops working`]);
  } else {
    rows.push(["Change", `${act.board}'s rules: ${act.change}`]);
  }
  if (a.state === "pending") rows.push(["Expires", `This request ends ${until(at(a.expires))} if nobody decides`]);

  return (
    <article className="ob-approval flex max-w-[640px] flex-col gap-5" data-approval={a.id}>
      <p className="text-meta text-muted">
        {a.state === "pending" ? "Asks you to approve" : "Asked you to approve"} · {ago(a.t, now)}
      </p>
      <AgentTitle agent={a.agent} rest={wantsRest(a.action)} />
      <Fields rows={rows} />

      {a.state === "pending" && (
        <div className="flex flex-col gap-3">
          {confirming ? (
            <Warning className="animate-fade-in">
              <p className="font-bold">Let your agents invite people without asking?</p>
              <p>
                An agent can be talked into things by a message it reads. If one is tricked, it could invite someone you don&apos;t know, and that person could read every open board on this server.
              </p>
              <p className="text-meta text-muted">Each invite still shows in your Inbox, where you can revoke it before it is used. You can turn this off in Settings.</p>
              <div className="flex flex-wrap gap-2 pt-1">
                <Button variant="secondary" onClick={() => decide(a, "executed", true, o.allowance)}>
                  Allow always
                </Button>
                <Button variant="quiet" onClick={() => setConfirming(false)}>
                  Cancel
                </Button>
              </div>
            </Warning>
          ) : (
            <div className="flex flex-wrap items-center gap-2" role="group" aria-label="Decide">
              <Button variant="primary" onClick={() => decide(a, "executed")}>
                Allow once
              </Button>
              {!why && (
                <Button variant="secondary" onClick={() => (invite ? setConfirming(true) : decide(a, "executed", true, o.allowance))}>
                  Allow always
                </Button>
              )}
              <Button variant="quiet" onClick={() => decide(a, "declined")}>
                Decline
              </Button>
            </div>
          )}
          {why ? (
            <p className="text-meta text-muted">{why}</p>
          ) : (
            !confirming && (
              <p className="text-meta text-muted">
                {invite ? "Allow always lets your agents invite people without asking. " : `Allow always lets your agents add people to boards without asking, as members. `}
                Change it any time in{" "}
                <a href={labHref({ settings: "1", inbox: null, board: null, item: null })} className="text-muted">
                  Settings
                </a>
                .
              </p>
            )
          )}
          <Command label="Or from your terminal" command={commands.allow(a)} note="Only you can run it, in your own terminal: agent sessions can't approve." />
        </div>
      )}

      {a.state === "executed" && (
        <div className="flex flex-col gap-3">
          <Done>
            {a.decided?.always ? "Allowed always" : "Allowed once"} {a.decided && <span className="text-muted">· {ago(a.decided.t, now)}</span>}. {did(a.agent, a.action)}.
            {a.invite && onShowInvite && (
              <>
                {" "}
                <button type="button" className="text-link underline underline-offset-[3px] hover:no-underline" onClick={() => onShowInvite(a.invite!)}>
                  See the invite
                </button>
              </>
            )}
          </Done>
          {a.decided?.always && (
            <p className="text-meta text-muted">
              Auto mode now covers {invite ? "inviting people to the server" : "adding people to boards"}.{" "}
              <a href={labHref({ settings: "1", inbox: null, board: null, item: null })} className="text-muted">
                Change it in Settings
              </a>
            </p>
          )}
          {a.decided?.always && invite && <Warning>{inviteWarning}</Warning>}
          <Record a={a} />
        </div>
      )}
      {a.state === "declined" && (
        <div className="flex flex-col gap-3">
          <Done muted>
            Declined{a.decided && ` · ${ago(a.decided.t, now)}`}. Nothing was done; {a.agent} sees that you declined.
          </Done>
        </div>
      )}
      {a.state === "expired" && <Done muted>Expired without a decision. Nothing was done; {a.agent} can ask again.</Done>}
    </article>
  );
}

/** Record is how the server records the action: who acted, and on whose authority. */
function Record({ a }: { a: Approval }) {
  const act = a.action;
  const line =
    act.kind === "invite_people"
      ? `an invite made by ${a.agent}, approved by you`
      : act.kind === "add_people"
        ? `${act.person} joined ${act.board} as member · ${byline("added", a.agent, "you", "approval")}`
        : `${did(a.agent, a.action)} · approved by you`;
  return (
    <p className="text-meta text-muted">
      In the record: {line}.<br />
      Approval <span className="font-mono text-[12px]">{a.id}</span>
    </p>
  );
}
