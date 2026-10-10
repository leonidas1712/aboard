"use client";

// EXPERIMENTAL, lab only: the person's allowance, in Settings ("auto mode"). Off by
// default: agents ask before any admin work. One main switch lets them add people to
// boards, as members, without asking (`aboard allowance on`). Inviting new people to the
// server is a separate switch that the main one never turns on: it warns and asks to
// confirm, because an agent tricked by a message could let in an outsider who can read
// every open board; while it is on, the API's warning stays in view. Turning auto mode
// off turns everything off, as `aboard allowance off` does. What always asks is listed
// underneath, and the same settings are one command in a terminal.

import { Menu } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Account } from "@/app/account";
import { type Board, get } from "@/app/api";
import { Header } from "@/app/chrome";
import { Sheet, useWide } from "@/app/sheet";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { TooltipProvider } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { type AllowanceCategory, inviteWarning } from "../../onboarding";
import { Nav } from "../nav";
import { Command, Warning } from "./parts";
import { setAllowance, useOnboarding } from "./state";

export function Settings({ onSignOut }: { onSignOut: () => void }) {
  const wide = useWide();
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [boardsOpen, setBoardsOpen] = useState(false);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => setBoards(r.boards));
  }, []);
  const nav = <Nav current="" boards={boards} />;
  return (
    <TooltipProvider delayDuration={250}>
      <div className="ob-settings flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
        <Header
          lead={
            !wide && (
              <button type="button" className="relative -ml-1.5 inline-flex size-11 shrink-0 items-center justify-center rounded-control text-ink transition-colors duration-[140ms] ease-out hover:bg-hover" aria-label="Boards" aria-haspopup="dialog" onClick={() => setBoardsOpen(true)}>
                <Menu className="size-5" strokeWidth={1.75} aria-hidden />
              </button>
            )
          }
          account={<Account onSignOut={onSignOut} />}
        />
        <div className="flex w-full flex-1 flex-col lg:grid lg:min-h-0 lg:grid-cols-[252px_minmax(0,1fr)]">
          {wide ? (
            <aside aria-label="Boards" className="quiet-scroll border-rule bg-sidebar px-5 py-4 lg:overflow-y-auto lg:border-r">
              {nav}
            </aside>
          ) : (
            <Sheet open={boardsOpen} onClose={() => setBoardsOpen(false)} side="left" title="Boards" back="Settings">
              {nav}
            </Sheet>
          )}
          <main className="quiet-scroll px-4 pt-6 pb-24 sm:px-8 lg:overflow-y-auto lg:px-12 lg:pt-8">
            <div className="mx-auto flex max-w-[640px] flex-col gap-8">
              <h1 className="text-headline font-bold">Settings</h1>
              <Allowance />
            </div>
          </main>
        </div>
      </div>
    </TooltipProvider>
  );
}

function Allowance() {
  const o = useOnboarding();
  const id = useId();
  const add = o.allowance.includes("add-people");
  const invite = o.allowance.includes("invite-people");
  const [confirming, setConfirming] = useState(false);
  const save = (c: AllowanceCategory[]) => setAllowance(c);
  return (
    <section aria-labelledby={`${id}-t`} className="flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <h2 id={`${id}-t`} className="text-title font-bold">
          What your agents may do for you
        </h2>
        <p className="text-muted">
          Your agents can do admin work for you, such as bringing a teammate onto a board. Unless you allow it here, they ask you first, in your Inbox. Every action is recorded with your name and the agent&apos;s.
        </p>
      </div>

      <div className="flex flex-col rounded-box border border-rule bg-surface">
        <Toggle
          id="ob-auto-switch"
          title="Auto mode"
          on={add}
          onChange={(v) => {
            setConfirming(false);
            save(v ? [...o.allowance.filter((c) => c !== "add-people"), "add-people"] : []);
          }}
        >
          <p>Add people to boards, as members, without asking. Only on boards where you can add people yourself.</p>
          <p className="text-meta text-muted">{add ? (invite ? "On. Turning it off also stops inviting." : "On. Your agents tell the board when they add someone.") : "Off. Your agents ask you each time."}</p>
        </Toggle>
      </div>

      <div className={cn("flex flex-col rounded-box border bg-surface", confirming ? "border-field-border" : "border-rule")}>
        <Toggle
          id="ob-invite-switch"
          title="Invite people to the server"
          on={invite || confirming}
          onChange={(v) => {
            if (v) setConfirming(true);
            else {
              setConfirming(false);
              save(o.allowance.filter((c) => c !== "invite-people"));
            }
          }}
        >
          <p>Create invites for new people, as members, without asking. Each invite works once, for 24 hours, and shows in your Inbox, where you can revoke it.</p>
          <p className="text-meta text-muted">{invite ? "On. Each invite they make shows in your Inbox." :"Off. Never part of auto mode: it is only on when you turn it on here."}</p>
        </Toggle>
        {confirming && (
          <div className="px-4 pb-4">
            <Warning className="animate-fade-in">
              <p className="font-bold">Let your agents invite people without asking?</p>
              <p>An agent can be talked into things by a message it reads. If one is tricked, it could invite someone you don&apos;t know, and that person could read every open board on this server.</p>
              <div className="flex flex-wrap gap-2 pt-1">
                <Button
                  variant="secondary"
                  onClick={() => {
                    setConfirming(false);
                    save([...o.allowance.filter((c) => c !== "invite-people"), "invite-people"]);
                  }}
                >
                  Turn on inviting
                </Button>
                <Button variant="quiet" onClick={() => setConfirming(false)}>
                  Cancel
                </Button>
              </div>
            </Warning>
          </div>
        )}
        {invite && !confirming && (
          <div className="px-4 pb-4">
            <Warning>{inviteWarning}</Warning>
          </div>
        )}
      </div>

      <div className="flex flex-col gap-2">
        <h3 className="text-meta font-bold text-muted">Always asks you</h3>
        <ul className="flex list-disc flex-col gap-1 pl-5">
          <li>Making someone an admin or a board owner, or changing anyone&apos;s role</li>
          <li>Removing people from a board or the server</li>
          <li>Revoking access keys</li>
          <li>Changing a board&apos;s rules</li>
        </ul>
        <p className="text-meta text-muted">Only you can change these settings, here or in your own terminal. Your agents can&apos;t, even in auto mode.</p>
      </div>

      <Command
        label="From your terminal"
        command="aboard allowance on"
        note={
          <>
            Inviting: <code className="font-mono text-[13px]">aboard allowance set invite-people on</code>. Everything off: <code className="font-mono text-[13px]">aboard allowance off</code>. What is on: <code className="font-mono text-[13px]">aboard allowance</code>.
          </>
        }
      />
    </section>
  );
}

/** Toggle is a setting with its switch: the whole row's label turns it, and it is 44px or more. */
function Toggle({ id, title, on, onChange, children }: { id: string; title: string; on: boolean; onChange: (v: boolean) => void; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1 px-4 py-4">
      <label htmlFor={id} className="cursor-pointer text-body font-bold">
        {title}
      </label>
      <span className="row-span-2 flex min-h-11 items-start pt-0.5">
        <Switch id={id} checked={on} onCheckedChange={onChange} className="tap" />
      </span>
      <div className="flex flex-col gap-1">{children}</div>
    </div>
  );
}
