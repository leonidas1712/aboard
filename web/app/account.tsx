"use client";

// Who you are, at the right of the top bar: your mark and name, opening a menu with
// who you are on this board, which server this is, and this browser's settings.

import { ChevronDown } from "lucide-react";
import { useEffect, useState } from "react";
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

/** useTheme is the colour scheme this browser chose, applied to the page. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [stored, setTheme] = usePref<string>("aboard.theme", "system");
  const theme: Theme = isTheme(stored) ? stored : "system";
  useEffect(() => {
    const root = document.documentElement;
    if (theme !== "system") root.dataset.theme = theme;
    else delete root.dataset.theme;
  }, [theme]);
  return [theme, setTheme];
}

/** Swatch is a scheme drawn small: its page, split for "Same as this computer", and its accent. */
function Swatch({ colours: [left, right, accent] }: { colours: readonly [string, string, string] }) {
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
};

export function Account({ admin, onSignOut, person }: Props) {
  const [loadedMe, setMe] = useState<Me | null>(null);
  const me = person ?? loadedMe;
  const [showPeople, setShowPeople] = useState(false);
  const [mode, setMode] = useState<"local" | "team" | null>(null);
  const [theme, setTheme] = useTheme();
  const signedIn = session();
  // A sign-out the server didn't confirm leaves the session on; the page says so.
  const [signOutProblem, setSignOutProblem] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    get<Me>("/v1/me").then((m) => { if (live) { setMe(m); if (m.server_role === "admin") setShowPeople(true); } }, () => {});
    get<{ people: unknown[] }>("/v1/people").then((r) => { if (live && r.people.length > 1) setShowPeople(true); }, () => {});
    get<{ mode: "local" | "team" }>("/v1/info").then((i) => live && setMode(i.mode), () => {});
    return () => {
      live = false;
    };
  }, []);
  if (!me) return null;
  const server = mode === "local" ? "This computer (local)" : window.location.host;
  return (
    <>
    <DropdownMenu>
      <DropdownMenuTrigger
        className="account ml-auto inline-flex min-h-11 items-center gap-2 rounded-control py-1 pr-2 pl-1 text-ink transition-colors duration-[140ms] ease-out hover:bg-hover data-[state=open]:bg-selected"
        aria-label={`You are ${me.name}. Account and settings`}
      >
        <SenderMark name={me.name} kind="human" identity={personIdentity(me.name, me)} className="size-7" />
        <span className="max-w-[12rem] truncate max-sm:hidden">{me.name}</span>
        <ChevronDown className="size-3.5 text-muted" strokeWidth={1.5} aria-hidden />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="account-menu min-w-[16rem]">
        <div className="px-3 pt-2 pb-2">
          <p className="font-bold">{me.name}</p>
          {admin && <p className="text-meta text-muted">Admin of this board</p>}
        </div>
        <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-3 px-3 pb-2 text-meta">
          <dt className="text-muted">Server</dt>
          <dd className="break-all">{server}</dd>
          {signedIn && (
            <>
              <dt className="text-muted">Key</dt>
              <dd className="session-key break-all">{signedIn.key.name}</dd>
              <dt className="text-muted">Until</dt>
              <dd>{new Date(signedIn.expires_at).toLocaleDateString(undefined, { day: "numeric", month: "short", year: "numeric" })}</dd>
            </>
          )}
        </dl>
        <DropdownMenuSeparator />
        {showPeople && <><DropdownMenuItem asChild><a href="/?view=people">People</a></DropdownMenuItem><DropdownMenuSeparator /></>}
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
