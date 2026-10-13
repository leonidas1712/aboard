"use client";

// Settings: one page with a tab for each kind of setting, listed down the left. Profile
// is who you are (your handle, renamed in place, the server and the sign-in); Agents is
// how urgent messages reach your busy agents, and what your agents may do for you without
// asking ("auto mode"); People is everyone on the server; Appearance is this browser's
// theme. A tab shows only when it has something for this person. Each tab has its own
// address (`?view=settings&tab=people`); on a phone the tabs are a list, and picking one
// shows it alone with a way back.
//
// Auto mode is off by default: agents ask before any admin work. One main switch lets
// them add people to boards, as members, without asking (`aboard allowance on`). Inviting
// new people to the server is a separate switch the main one never turns on: it warns and
// asks to confirm, because an agent tricked by a message could let in an outsider who can
// read every open board, and while it is on the server's warning stays in view. Turning
// auto mode off turns everything off, as `aboard allowance off` does.

import { ArrowLeft, ChevronRight, Menu } from "lucide-react";
import { useEffect, useId, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { TooltipProvider } from "@/components/ui/tooltip";
import { ApiError, type Board, type Me, type MidturnPolicy, ackBoard, get } from "./api";
import { Account, Swatch, signedInUntil, useServerName, useTheme } from "./account";
import { Header, Problem } from "./chrome";
import { changed } from "./onboarding-data";
import { type Allowance, type AllowanceCategory, getAllowance, handlePattern, renameSelf, setAllowance } from "./onboarding-api";
import { Command, Warning } from "./onboarding-ui";
import { changeMidturn, midturnCopy, midturnLabels, midturnPolicies, useMidturn } from "./midturn";
import { PeopleSection } from "./people";
import { Sheet, useWide } from "./sheet";
import { BoardNav } from "./sidebars";
import { Finder } from "./finder";
import { themes } from "./themes";

export type SettingsTab = "profile" | "agents" | "people" | "appearance";

const tabs: { id: SettingsTab; label: string; note: string }[] = [
  { id: "profile", label: "Profile", note: "Your handle, server and sign-in" },
  { id: "agents", label: "Agents", note: "Urgent messages and auto mode" },
  { id: "people", label: "People", note: "Everyone on this server" },
  { id: "appearance", label: "Appearance", note: "Theme for this browser" },
];

/** tabInAddress is the tab the address asks for: its tab, People for the old `?view=people`, or none. */
function tabInAddress(): SettingsTab | null {
  const q = new URLSearchParams(window.location.search);
  const t = q.get("tab");
  if (tabs.some((x) => x.id === t)) return t as SettingsTab;
  return q.get("view") === "people" ? "people" : null;
}

const tabAddress = (t: SettingsTab | null) => (t ? `/?view=settings&tab=${t}` : "/?view=settings");

/**
 * useFacts reads what decides which tabs show: who the person is, whether the server has
 * other people (or the person is its admin), and whether it answers for the allowance and
 * the mid-turn setting. settled is true once every read has answered or failed.
 */
function useFacts() {
  const [me, setMe] = useState<Me | null>(null);
  const [showPeople, setShowPeople] = useState(false);
  const [allowance, setAllowanceShown] = useState(false);
  const [pending, setPending] = useState(3);
  const midturn = useMidturn();
  useEffect(() => {
    let live = true;
    const done = () => live && setPending((n) => n - 1);
    get<Me>("/v1/me").then((m) => {
      if (!live) return;
      setMe(m);
      if (m.server_role === "admin") setShowPeople(true);
    }, () => {}).finally(done);
    get<{ people: unknown[] }>("/v1/people").then((r) => live && r.people.length > 1 && setShowPeople(true), () => {}).finally(done);
    getAllowance().then(() => live && setAllowanceShown(true), () => {}).finally(done);
    return () => {
      live = false;
    };
  }, []);
  const settled = pending === 0 && (midturn.view !== null || midturn.unavailable);
  return { me, setMe, showPeople, allowance, midturn: midturn.view !== null, settled };
}

export default function Settings({ onSignOut }: { onSignOut: () => void }) {
  const wide = useWide();
  const [boards, setBoards] = useState<Board[] | null>(null);
  const [boardsOpen, setBoardsOpen] = useState(false);
  const facts = useFacts();
  const [asked, setAsked] = useState<SettingsTab | null>(() => tabInAddress());
  // pushed is true while a phone's history holds an entry for the tab picked from the list.
  const pushed = useRef(false);
  useEffect(() => {
    get<{ boards: Board[] }>("/v1/boards", { lifecycle: "all" }).then((r) => setBoards(r.boards), () => setBoards([]));
    const follow = () => {
      pushed.current = false;
      setAsked(tabInAddress());
    };
    window.addEventListener("popstate", follow);
    return () => window.removeEventListener("popstate", follow);
  }, []);
  const shown = tabs.filter((t) => t.id === "agents" ? facts.allowance || facts.midturn : t.id === "people" ? facts.showPeople : true);
  // A tab that doesn't apply to this person falls back to Profile, once the reads have answered.
  const want = asked && (shown.some((t) => t.id === asked) || !facts.settled) ? asked : asked && "profile";
  const tab: SettingsTab | null = wide ? want ?? "profile" : want;
  const pick = (t: SettingsTab) => {
    if (!wide && !tab) {
      window.history.pushState(null, "", tabAddress(t));
      pushed.current = true;
    } else window.history.replaceState(null, "", tabAddress(t));
    setAsked(t);
  };
  const back = () => {
    if (pushed.current) window.history.back();
    else {
      window.history.replaceState(null, "", tabAddress(null));
      setAsked(null);
    }
  };
  const nav = <BoardNav current="" boards={boards} onMarkRead={(b) => void ackBoard(b.name, b.head_seq)} />;
  const panel = (t: SettingsTab) =>
    t === "profile" ? <ProfileSection me={facts.me} onRenamed={(name) => facts.setMe((m) => m && { ...m, name })} />
    : t === "agents" ? <AgentsSection allowance={facts.allowance} midturn={facts.midturn} />
    : t === "people" ? <PeopleSection />
    : <AppearanceSection />;
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
          account={<Account person={facts.me} onSignOut={onSignOut} />}
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
            <div className="mx-auto flex max-w-[1000px] flex-col gap-6">
              {wide ? (
                <>
                  <h1 className="text-headline font-bold">Settings</h1>
                  <div className="grid grid-cols-[200px_minmax(0,1fr)] items-start gap-10">
                    <TabList tabs={shown} current={tab!} onPick={pick} />
                    <div role="tabpanel" id={`settings-panel-${tab}`} aria-labelledby={`settings-tab-${tab}`} className={cn("settings-panel min-w-0", tab !== "people" && "max-w-[640px]")}>
                      {panel(tab!)}
                    </div>
                  </div>
                </>
              ) : tab ? (
                <div className="settings-panel flex min-w-0 flex-col gap-4">
                  <button type="button" onClick={back} className="settings-back -ml-2.5 inline-flex min-h-11 items-center gap-1.5 self-start rounded-control px-2.5 text-link transition-colors duration-[140ms] ease-out hover:bg-hover">
                    <ArrowLeft className="size-[18px]" strokeWidth={1.75} aria-hidden />
                    Settings
                  </button>
                  {panel(tab)}
                </div>
              ) : (
                <>
                  <h1 className="text-headline font-bold">Settings</h1>
                  <nav aria-label="Settings">
                    <ul className="settings-list flex flex-col divide-y divide-rule rounded-box border border-rule bg-surface">
                      {shown.map((t) => (
                        <li key={t.id}>
                          <button type="button" onClick={() => pick(t.id)} className="flex min-h-14 w-full items-center gap-3 px-4 py-3 text-left transition-colors duration-[140ms] ease-out hover:bg-hover">
                            <span className="flex min-w-0 flex-1 flex-col">
                              <span className="font-bold">{t.label}</span>
                              <span className="text-meta text-muted">{t.note}</span>
                            </span>
                            <ChevronRight className="size-4 shrink-0 text-muted" strokeWidth={1.75} aria-hidden />
                          </button>
                        </li>
                      ))}
                    </ul>
                  </nav>
                </>
              )}
            </div>
          </main>
        </div>
        <Finder boards={boards} page="settings" />
      </div>
    </TooltipProvider>
  );
}

/** TabList is the tabs down the left: arrows, Home and End move between them and show each. */
function TabList({ tabs: list, current, onPick }: { tabs: typeof tabs; current: SettingsTab; onPick: (t: SettingsTab) => void }) {
  const box = useRef<HTMLDivElement>(null);
  const onKeyDown = (e: React.KeyboardEvent) => {
    const i = list.findIndex((t) => t.id === current);
    const n = list.length;
    const next = e.key === "ArrowDown" ? (i + 1) % n : e.key === "ArrowUp" ? (i - 1 + n) % n : e.key === "Home" ? 0 : e.key === "End" ? n - 1 : -1;
    if (next < 0) return;
    e.preventDefault();
    onPick(list[next].id);
    box.current?.querySelector<HTMLElement>(`#settings-tab-${list[next].id}`)?.focus();
  };
  return (
    <div ref={box} role="tablist" aria-label="Settings" aria-orientation="vertical" onKeyDown={onKeyDown} className="settings-tabs flex flex-col gap-1">
      {list.map((t) => {
        const on = t.id === current;
        return (
          <button
            key={t.id}
            type="button"
            role="tab"
            id={`settings-tab-${t.id}`}
            aria-selected={on}
            aria-controls={`settings-panel-${t.id}`}
            tabIndex={on ? 0 : -1}
            onClick={() => onPick(t.id)}
            className={cn("flex min-h-11 items-center rounded-control px-2.5 text-left text-ink transition-colors duration-[140ms] ease-out hover:bg-hover", on && "bg-selected font-bold hover:bg-selected")}
          >
            {t.label}
          </button>
        );
      })}
    </div>
  );
}

/** Row is one fact in a settings box: its name, then its value and any control. */
function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-x-4 gap-y-1 px-4 py-3.5 sm:grid-cols-[140px_minmax(0,1fr)]">
      <dt className="text-muted">{label}</dt>
      <dd className="min-w-0">{children}</dd>
    </div>
  );
}

/** ProfileSection is who you are: your handle, renamed in place, the server and how long this browser stays signed in. */
function ProfileSection({ me, onRenamed }: { me: Me | null; onRenamed: (name: string) => void }) {
  const server = useServerName();
  const until = signedInUntil();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState<unknown>(null);
  const [busy, setBusy] = useState(false);
  const field = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (editing) field.current?.focus();
  }, [editing]);
  const save = async () => {
    if (!me) return;
    const value = draft.trim();
    if (!handlePattern.test(value)) {
      setError(new ApiError(400, "invalid_request", "A handle uses lowercase letters, digits and hyphens, up to 40, and starts with a letter or digit.", ""));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const r = await renameSelf(me.name, value);
      onRenamed(r.person.handle);
      setEditing(false);
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  };
  const cancel = () => {
    setEditing(false);
    setError(null);
  };
  return (
    <section aria-labelledby="settings-profile" className="profile flex flex-col gap-4">
      <h2 id="settings-profile" className="text-title font-bold">Profile</h2>
      <dl className="flex flex-col divide-y divide-rule rounded-box border border-rule bg-surface">
        <Row label="Handle">
          {!me ? (
            <span role="status" className="text-muted">Loading…</span>
          ) : editing ? (
            <form className="flex flex-col gap-2" onSubmit={(e) => { e.preventDefault(); void save(); }} onKeyDown={(e) => { if (e.key === "Escape") { e.preventDefault(); cancel(); } }}>
              <label htmlFor="rename-self" className="text-meta text-muted">New handle</label>
              <input id="rename-self" ref={field} className="min-h-11 rounded-control border border-field-border bg-surface px-3 text-ink" value={draft} maxLength={40} autoCapitalize="none" spellCheck={false} onChange={(e) => setDraft(e.target.value)} />
              {error !== null && <Problem error={error} />}
              <div className="flex flex-wrap gap-2">
                <Button type="submit" disabled={busy}>{busy ? "Renaming…" : "Rename"}</Button>
                <Button type="button" variant="secondary" onClick={cancel}>Cancel</Button>
              </div>
            </form>
          ) : (
            <div className="flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
              <span className="handle font-bold break-all">{me.name}</span>
              {me.kind === "human" && (
                <Button type="button" variant="secondary" onClick={() => { setDraft(me.name); setEditing(true); }}>
                  Rename yourself
                </Button>
              )}
            </div>
          )}
          {me?.kind === "human" && <p className="pt-1 text-meta text-muted">Your handle shows on every board you are on. Your agents, keys and history stay yours.</p>}
        </Row>
        <Row label="Server">
          <span className="break-all">{server}</span>
        </Row>
        {until && (
          <Row label="Signed in">
            <span className="signed-in-until">until {until}</span>
          </Row>
        )}
      </dl>
    </section>
  );
}

/** AgentsSection is how your agents work for you: urgent messages while they work, and auto mode. */
function AgentsSection({ allowance, midturn }: { allowance: boolean; midturn: boolean }) {
  return (
    <div className="flex flex-col gap-10">
      <h2 className="sr-only">Agents</h2>
      {midturn && <MidturnSection />}
      {allowance && <AllowanceSection />}
    </div>
  );
}

/** MidturnSection is the person's mid-turn setting, the default each of their agents follows unless its details set its own. */
function MidturnSection() {
  const id = useId();
  const { view } = useMidturn();
  const [problem, setProblem] = useState<string | null>(null);
  // The choice shows at once; the server's answer replaces it, or a refusal puts back the old one.
  const [choosing, setChoosing] = useState<MidturnPolicy | null>(null);
  if (!view) return null;
  return (
    <section aria-labelledby={`${id}-t`} className="midturn-setting flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <h3 id={`${id}-t`} className="text-title font-bold">
          Urgent messages while an agent works
        </h3>
        <p className="text-muted">{midturnCopy}</p>
      </div>
      <div role="radiogroup" aria-labelledby={`${id}-t`} className="flex flex-col divide-y divide-rule rounded-box border border-rule bg-surface">
        {midturnPolicies.map((p) => (
          <label key={p} className="flex min-h-12 cursor-pointer items-center gap-3 px-4 py-3">
            <input
              type="radio"
              name={`${id}-midturn`}
              value={p}
              checked={(choosing ?? view.policy) === p}
              onChange={() => {
                setProblem(null);
                setChoosing(p);
                changeMidturn(p)
                  .catch((e: unknown) => setProblem(e instanceof ApiError ? e.message : "Couldn't change the mid-turn setting."))
                  .finally(() => setChoosing(null));
              }}
              className="size-4 shrink-0 accent-accent-strong"
            />
            <span>{midturnLabels[p]}</span>
          </label>
        ))}
      </div>
      <p className="text-meta text-muted">One agent of yours can follow its own setting instead, from its details on the board.</p>
      {problem && (
        <p role="alert" className="midturn-problem rounded-box border border-field-border bg-selected px-3 py-2 text-ink">
          {problem}
        </p>
      )}
    </section>
  );
}

/** AppearanceSection is this browser's theme. */
function AppearanceSection() {
  const id = useId();
  const [theme, setTheme] = useTheme();
  return (
    <section aria-labelledby={`${id}-t`} className="appearance flex flex-col gap-4">
      <div className="flex flex-col gap-1.5">
        <h2 id={`${id}-t`} className="text-title font-bold">Theme</h2>
        <p className="text-muted">Kept in this browser only.</p>
      </div>
      <div role="radiogroup" aria-labelledby={`${id}-t`} className="flex flex-col divide-y divide-rule rounded-box border border-rule bg-surface">
        {themes.map((t) => (
          <label key={t.id} className="flex min-h-12 cursor-pointer items-center gap-3 px-4 py-3" data-scheme={t.id}>
            <input type="radio" name={`${id}-theme`} value={t.id} checked={theme === t.id} onChange={() => setTheme(t.id)} className="size-4 shrink-0 accent-accent-strong" />
            <span className="flex min-w-0 flex-col">
              <span>{t.label}</span>
              {"hint" in t && <span className="text-meta text-muted">{t.hint}</span>}
            </span>
            <Swatch colours={t.swatch} />
          </label>
        ))}
      </div>
    </section>
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
        <h3 id={`${id}-t`} className="text-title font-bold">
          What your agents may do for you
        </h3>
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
        <h4 className="text-meta font-bold text-muted">Always asks you</h4>
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
