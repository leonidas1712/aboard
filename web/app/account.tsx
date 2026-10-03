"use client";

// Who you are, at the right of the top bar: your mark and name, opening a menu with
// who you are on this board, which server this is, and this browser's settings.

import { ChevronDown } from "lucide-react";
import { useEffect, useState } from "react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { type Me, get } from "./api";
import { usePref } from "./prefs";
import { SenderMark } from "./timeline";
import { personIdentity } from "./words";

export type Theme = "system" | "light" | "dark";

/** useTheme is the theme this browser chose, applied to the page. */
export function useTheme(): [Theme, (t: Theme) => void] {
  const [theme, setTheme] = usePref<Theme>("aboard.theme", "system");
  useEffect(() => {
    const root = document.documentElement;
    if (theme === "light" || theme === "dark") root.dataset.theme = theme;
    else delete root.dataset.theme;
  }, [theme]);
  return [theme, setTheme];
}

type Props = {
  /** admin is true when the person is an admin of this board and another person is on it. */
  admin?: boolean;
};

export function Account({ admin }: Props) {
  const [me, setMe] = useState<Me | null>(null);
  const [mode, setMode] = useState<"local" | "team" | null>(null);
  const [theme, setTheme] = useTheme();
  useEffect(() => {
    let live = true;
    get<Me>("/v1/me").then((m) => live && setMe(m), () => {});
    get<{ mode: "local" | "team" }>("/v1/info").then((i) => live && setMode(i.mode), () => {});
    return () => {
      live = false;
    };
  }, []);
  if (!me) return null;
  const server = mode === "local" ? "This computer (local)" : window.location.host;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="account ml-auto inline-flex min-h-11 items-center gap-2 rounded-control py-1 pr-2 pl-1 text-ink transition-colors duration-[140ms] ease-out hover:bg-selected data-[state=open]:bg-selected"
        aria-label={`You are ${me.name}. Account and settings`}
      >
        <SenderMark name={me.name} kind="human" identity={personIdentity(me.name, me)} className="size-7" />
        <span className="max-w-[12rem] truncate">{me.name}</span>
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
        </dl>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>Theme</DropdownMenuLabel>
        <DropdownMenuRadioGroup value={theme} onValueChange={(v) => setTheme(v as Theme)}>
          <DropdownMenuRadioItem value="system">Same as this computer</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">Light</DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">Dark</DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
