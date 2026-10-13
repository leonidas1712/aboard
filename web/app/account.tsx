"use client";

// Who you are, at the right of the top bar: your mark and name, opening a short menu with
// who you are on this board, which server this is, Settings, the theme and sign-out.

import { ChevronDown, CircleQuestionMark, Settings2 } from "lucide-react";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useCallback, useEffect, useState } from "react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { type Me, get, session, signOut } from "./api";
import { usePref } from "./prefs";
import { type Theme, isTheme, themes } from "./themes";
import { SenderMark } from "./agent-mark";
import { personIdentity } from "./words";

const themeEvent = "aboard:theme";

/** useTheme is the colour scheme this browser chose, applied to the page; every chooser on the page follows a change. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [stored, setStored] = usePref<string>("aboard.theme", "system");
  const theme: Theme = isTheme(stored) ? stored : "system";
  useEffect(() => {
    const follow = (e: Event) => setStored((e as CustomEvent<Theme>).detail);
    window.addEventListener(themeEvent, follow);
    return () => window.removeEventListener(themeEvent, follow);
  }, [setStored]);
  const setTheme = useCallback((t: Theme) => window.dispatchEvent(new CustomEvent(themeEvent, { detail: t })), []);
  useEffect(() => {
    const root = document.documentElement;
    if (theme !== "system") root.dataset.theme = theme;
    else delete root.dataset.theme;
  }, [theme]);
  return [theme, setTheme];
}

/** useServerName names the server this browser talks to: "This computer (local)" or its host. */
export function useServerName(): string {
  const [mode, setMode] = useState<"local" | "team" | null>(null);
  useEffect(() => {
    let live = true;
    get<{ mode: "local" | "team" }>("/v1/info").then((i) => live && setMode(i.mode), () => {});
    return () => {
      live = false;
    };
  }, []);
  return mode === "local" ? "This computer (local)" : typeof window === "undefined" ? "" : window.location.host;
}

/** signedInUntil is the day this browser's sign-in ends, or null without one. */
export function signedInUntil(): string | null {
  const s = session();
  return s ? new Date(s.expires_at).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" }) : null;
}

/** Swatch is a scheme drawn small: its page, split for "Same as this computer", and its accent. */
export function Swatch({ colours: [left, right, accent] }: { colours: readonly [string, string, string] }) {
  return (
    <span aria-hidden className="ml-auto flex size-5 shrink-0 overflow-hidden rounded-[6px] border border-field-border" style={{ background: `linear-gradient(90deg, ${left} 50%, ${right} 50%)` }}>
      <span className="m-auto h-1.5 w-2.5 rounded-full" style={{ background: accent }} />
    </span>
  );
}

type Props = {
  person?: Me | null;
  /** admin is true when the person is an admin of this board and another person is on it. */
  admin?: boolean;
  /** onSignOut runs once this browser has signed out. */
  onSignOut: () => void;
  /** onHelp opens the keys sheet, from a Help button beside the account menu. */
  onHelp?: () => void;
};

export function Account({ admin, onSignOut, person, onHelp }: Props) {
  const [loadedMe, setMe] = useState<Me | null>(null);
  const me = person ?? loadedMe;
  const [theme, setTheme] = useTheme();
  const server = useServerName();
  const until = signedInUntil();
  // A sign-out the server didn't confirm leaves the session on; the page says so.
  const [signOutProblem, setSignOutProblem] = useState<string | null>(null);
  useEffect(() => {
    if (person) return;
    let live = true;
    get<Me>("/v1/me").then((m) => live && setMe(m), () => {});
    return () => {
      live = false;
    };
  }, [person]);
  if (!me) return null;
  return (
    <>
    {onHelp && (
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            onClick={onHelp}
            aria-label="Help and keyboard shortcuts"
            className="help-button ml-auto inline-flex size-11 shrink-0 items-center justify-center rounded-control text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink"
          >
            <CircleQuestionMark className="size-5" strokeWidth={1.75} aria-hidden />
          </button>
        </TooltipTrigger>
        <TooltipContent>Help</TooltipContent>
      </Tooltip>
    )}
    <DropdownMenu>
      <DropdownMenuTrigger
        className={`account ${onHelp ? "ml-1" : "ml-auto"} inline-flex min-h-11 items-center gap-2 rounded-control py-1 pr-2 pl-1 text-ink transition-colors duration-[140ms] ease-out hover:bg-hover data-[state=open]:bg-selected`}
        aria-label={`You are ${me.name}. Account and settings`}
      >
        <SenderMark name={me.name} kind="human" identity={personIdentity(me.name)} className="size-7" />
        <span className="max-w-[12rem] truncate max-sm:hidden">{me.name}</span>
        <ChevronDown className="size-3.5 text-muted" strokeWidth={1.5} aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="account-menu w-[22rem] max-w-[calc(100vw-1.5rem)]">
        <div className="px-3 pt-2 pb-2">
          <p className="font-bold">{me.name}</p>
          {admin && <p className="text-meta text-muted">Admin of this board</p>}
        </div>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 px-3 pb-2 text-meta">
          <dt className="text-muted">Server</dt>
          <dd className="break-all">{server}</dd>
          {until && (
            <>
              <dt className="text-muted">Signed in</dt>
              <dd className="signed-in-until">until {until}</dd>
            </>
          )}
        </dl>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <a href="/?view=settings&tab=profile" className="settings-item flex items-center gap-2">
            <Settings2 className="size-4 text-muted" strokeWidth={1.5} aria-hidden />
            Settings
          </a>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          {themes.map((t) => (
            <DropdownMenuRadioItem key={t.id} value={t.id} className="gap-3" data-scheme={t.id}>
              <span className="flex min-w-0 flex-col">
                <span>{t.label}</span>
                {"hint" in t && <span className="text-meta text-muted">{t.hint}</span>}
              </span>
              <Swatch colours={t.swatch} />
            </DropdownMenuRadioItem>
          ))}
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuItem
          onSelect={() =>
            signOut().then(onSignOut, (e: unknown) =>
              setSignOutProblem(
                `Couldn't sign out: ${e instanceof Error ? e.message : "the server didn't answer"} This browser is still signed in.`,
              ),
            )
          }
        >
          Sign out of this browser
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
    {signOutProblem && (
      <p role="alert" className="sign-out-problem basis-full rounded-box border border-field-border bg-selected px-3 py-2 text-ink">
        {signOutProblem}
      </p>
    )}
    </>
  );
}
