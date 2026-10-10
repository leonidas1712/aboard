"use client";

// Settings: what the person's agents may do for them without asking ("auto mode"). Off
// by default, agents ask before any admin work. One main switch lets them add people to
// boards, as members, without asking (`aboard allowance on`). Inviting new people to the
// server is a separate switch the main one never turns on: it warns and asks to confirm,
// because an agent tricked by a message could let in an outsider who can read every open
// board, and while it is on the server's warning stays in view. Turning auto mode off
// turns everything off, as `aboard allowance off` does.

import { Menu } from "lucide-react";
import { useEffect, useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { TooltipProvider } from "@/components/ui/tooltip";
import { type Board, ackBoard, get } from "./api";
import { Account } from "./account";
import { Header, Problem } from "./chrome";
import { changed } from "./onboarding-data";
import { type Allowance, type AllowanceCategory, getAllowance, setAllowance } from "./onboarding-api";
import { Command, Warning } from "./onboarding-ui";
import { Sheet, useWide } from "./sheet";
import { BoardNav } from "./sidebars";

export default function Settings({ onSignOut }: { onSignOut: () => void }) {
  const wide = useWide();
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [boardsOpen, setBoardsOpen] = useState(false);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => setBoards(r.boards), () => setBoards([]));
  }, []);
  const nav = <BoardNav current="" boards={boards} onMarkRead={(b) => void ackBoard(b.name, b.head_seq)} />;
  return (
    <TooltipProvider delayDuration={250}>
      <div className="settings-page flex min-h-dvh flex-col lg:h-dvh lg:overflow-clip">
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
              <AllowanceSection />
            </div>
          </main>
        </div>
      </div>
    </TooltipProvider>
  );
}

function AllowanceSection() {
  const id = useId();
  const [allowance, setCurrent] = useState<Allowance | null>(null);
  const [loadError, setLoadError] = useState<unknown>(null);
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  useEffect(() => {
    getAllowance().then(setCurrent, setLoadError);
  }, []);
  const categories = allowance?.categories ?? [];
  const add = categories.includes("add-people");
  const invite = categories.includes("invite-people");
  const save = async (next: AllowanceCategory[]) => {
    setBusy(true);
    setError(null);
    try {
      setCurrent(await setAllowance(next));
      changed();
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <section aria-labelledby={`${id}-t`} className="allowance flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <h2 id={`${id}-t`} className="text-title font-bold">
          What your agents may do for you
        </h2>
        <p className="text-muted">Your agents can do admin work for you, such as bringing a teammate onto a board. Unless you allow it here, they ask you first, in your Inbox. Every action is recorded with your name and the agent&apos;s.</p>
      </div>
      {loadError !== null ? (
        <Problem error={loadError} />
      ) : allowance === null ? (
        <p role="status" className="text-muted">
          Loading your settings…
        </p>
      ) : (
        <>
          <div className="flex flex-col rounded-box border border-rule bg-surface">
            <Toggle
              id="auto-mode"
              title="Auto mode"
              on={add}
              disabled={busy}
              onChange={(v) => {
                setConfirming(false);
                void save(v ? [...categories.filter((c) => c !== "add-people"), "add-people"] : []);
              }}
            >
              <p>Add people to boards, as members, without asking. Only on boards where you can add people yourself.</p>
              <p className="text-meta text-muted">{add ? (invite ? "On. Turning it off also stops inviting." : "On. Your agents tell the board when they add someone.") : "Off. Your agents ask you each time."}</p>
            </Toggle>
          </div>
          <div className={confirming ? "flex flex-col rounded-box border border-field-border bg-surface" : "flex flex-col rounded-box border border-rule bg-surface"}>
            <Toggle
              id="invite-people"
              title="Invite people to the server"
              on={invite || confirming}
              disabled={busy}
              onChange={(v) => {
                if (v) setConfirming(true);
                else {
                  setConfirming(false);
                  void save(categories.filter((c) => c !== "invite-people"));
                }
              }}
            >
              <p>Create invites for new people, as members, without asking. Each invite works once, for 24 hours, and shows in your Inbox, where you can revoke it.</p>
              <p className="text-meta text-muted">{invite ? "On. Each invite they make shows in your Inbox." : "Off. Never part of auto mode: it is only on when you turn it on here."}</p>
            </Toggle>
            {confirming && (
              <div className="px-4 pb-4">
                <Warning className="animate-fade-in">
                  <p className="font-bold">Let your agents invite people without asking?</p>
                  <p>An agent can be talked into things by a message it reads. If one is tricked, it could invite someone you don&apos;t know, and that person could read every open board on this server.</p>
                  <div className="flex flex-wrap gap-2 pt-1">
                    <Button
                      variant="secondary"
                      disabled={busy}
                      onClick={() => {
                        setConfirming(false);
                        void save([...categories.filter((c) => c !== "invite-people"), "invite-people"]);
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
            {invite && !confirming && allowance.warning && (
              <div className="px-4 pb-4">
                <Warning>{allowance.warning}</Warning>
              </div>
            )}
          </div>
          {error !== null && <Problem error={error} />}
        </>
      )}
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

/** Toggle is a setting with its switch: the label turns it, and the target is 44px or more. */
function Toggle({ id, title, on, disabled, onChange, children }: { id: string; title: string; on: boolean; disabled?: boolean; onChange: (v: boolean) => void; children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1 px-4 py-4">
      <label htmlFor={id} className="cursor-pointer text-body font-bold">
        {title}
      </label>
      <span className="row-span-2 flex min-h-11 items-start pt-0.5">
        <Switch id={id} checked={on} disabled={disabled} onCheckedChange={onChange} className="tap" />
      </span>
      <div className="flex flex-col gap-1">{children}</div>
    </div>
  );
}
