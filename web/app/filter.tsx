"use client";

// The timeline's filters and view settings, in one place: a Filter control that opens a
// panel, and the active filters as chips above the timeline, each removable.

import { ListFilter, X } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuCheckboxItem,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import type { Member } from "./api";
import type { Filter } from "./use-board";

type Props = {
  filter: Filter;
  setFilter: (f: Filter) => void;
  showEvents: boolean;
  setShowEvents: (v: boolean) => void;
  members: Member[];
  /** me is the person's own name, shown as "you" in the list of senders. */
  me: string | null;
};

const anyone = "\u0000";

export function FilterControl({ filter, setFilter, showEvents, setShowEvents, members, me }: Props) {
  const agents = members.filter((m) => m.kind === "agent");
  const people = members.filter((m) => m.kind === "human");
  const roles = [...new Set(agents.map((a) => a.role).filter((r): r is string => !!r))].sort();
  const set = chips(filter, showEvents).length;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className="filter-control ml-auto inline-flex min-h-11 shrink-0 items-center gap-2 rounded-control px-3 text-ink transition-colors duration-[140ms] ease-out hover:bg-selected data-[state=open]:bg-selected"
        aria-label={set > 0 ? `Filter, ${set} set` : "Filter"}
      >
        <ListFilter className="size-4" strokeWidth={1.5} aria-hidden />
        Filter
        {set > 0 && <span className="text-meta text-muted tabular-nums">· {set}</span>}
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="min-w-[16rem]">
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            From
            <span className="text-meta text-muted">{filter.from ? (filter.from === me ? "you" : filter.from) : "anyone"}</span>
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <DropdownMenuRadioGroup
              value={filter.from ?? anyone}
              onValueChange={(v) => setFilter({ ...filter, from: v === anyone ? undefined : v })}
            >
              <DropdownMenuRadioItem value={anyone}>Anyone</DropdownMenuRadioItem>
              {agents.length > 0 && <DropdownMenuLabel>Agents</DropdownMenuLabel>}
              {agents.map((a) => (
                <DropdownMenuRadioItem key={a.id} value={a.name}>
                  {a.name}
                </DropdownMenuRadioItem>
              ))}
              <DropdownMenuLabel>People</DropdownMenuLabel>
              {people.map((p) => (
                <DropdownMenuRadioItem key={p.id} value={p.name}>
                  {p.name === me ? `${p.name} (you)` : p.name}
                </DropdownMenuRadioItem>
              ))}
            </DropdownMenuRadioGroup>
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        {roles.length > 0 && (
          <DropdownMenuSub>
            <DropdownMenuSubTrigger>
              Role
              <span className="text-meta text-muted">{filter.role ?? "any"}</span>
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              <DropdownMenuRadioGroup
                value={filter.role ?? anyone}
                onValueChange={(v) => setFilter({ ...filter, role: v === anyone ? undefined : v })}
              >
                <DropdownMenuRadioItem value={anyone}>Any role</DropdownMenuRadioItem>
                {roles.map((r) => (
                  <DropdownMenuRadioItem key={r} value={r}>
                    {r}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        )}
        <DropdownMenuCheckboxItem
          checked={!!filter.toMe}
          onCheckedChange={(v) => setFilter({ ...filter, toMe: v || undefined })}
          onSelect={(e) => e.preventDefault()}
        >
          Addressed to me
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        <DropdownMenuCheckboxItem checked={showEvents} onCheckedChange={setShowEvents} onSelect={(e) => e.preventDefault()}>
          Show board events
        </DropdownMenuCheckboxItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

type Chip = { key: string; label: string; remove: (f: Filter) => Filter | null };

function chips(filter: Filter, showEvents: boolean): Chip[] {
  const out: Chip[] = [];
  if (filter.from) out.push({ key: "from", label: `From ${filter.from}`, remove: (f) => ({ ...f, from: undefined }) });
  if (filter.role) out.push({ key: "role", label: `Role ${filter.role}`, remove: (f) => ({ ...f, role: undefined }) });
  if (filter.toMe) out.push({ key: "to-me", label: "Addressed to me", remove: (f) => ({ ...f, toMe: undefined }) });
  if (!showEvents) out.push({ key: "events", label: "Board events hidden", remove: () => null });
  return out;
}

/** FilterChips shows the active filters above the timeline; each one's × removes it. */
export function FilterChips({ filter, setFilter, showEvents, setShowEvents, me }: Omit<Props, "members">) {
  const list = chips(filter, showEvents);
  if (list.length === 0) return null;
  return (
    <ul className="filter-chips flex flex-wrap items-center gap-2 pb-3 animate-appear" aria-label="Active filters">
      {list.map((c) => (
        <li key={c.key}>
          <button
            type="button"
            className="chip inline-flex min-h-9 items-center gap-1.5 rounded-control border border-rule bg-surface py-1 pr-2 pl-3 text-meta text-ink transition-colors duration-[140ms] ease-out hover:border-field-border hover:bg-selected"
            aria-label={`Remove filter: ${c.label}`}
            title="Remove this filter"
            onClick={() => {
              const next = c.remove(filter);
              if (next === null) setShowEvents(true);
              else setFilter(next);
            }}
          >
            {c.key === "from" && filter.from === me ? "From you" : c.label}
            <X className="size-3.5 text-muted" strokeWidth={1.75} aria-hidden />
          </button>
        </li>
      ))}
      {list.length > 1 && (
        <li>
          <button
            type="button"
            className="min-h-9 rounded-control px-2 text-meta text-link hover:underline"
            onClick={() => {
              setFilter({});
              setShowEvents(true);
            }}
          >
            Clear all
          </button>
        </li>
      )}
    </ul>
  );
}
