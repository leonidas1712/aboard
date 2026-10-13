"use client";

// The board's Export menu: copy the board as Markdown, or save it as a .md or .zip file.
// The server builds the document from what this person can read, so the page only
// fetches it and hands it on.

import { Copy, Download, FileArchive, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { ApiError, type ExportFormat, exportBoard } from "./api";

export type ExportAction = "copy" | "md" | "zip";
type Action = ExportAction;

/** exportActions are the menu's items, which the finder offers too. */
export const exportActions: { action: ExportAction; label: string }[] = [
  { action: "copy", label: "Copy as Markdown" },
  { action: "md", label: "Download .md" },
  { action: "zip", label: "Download .zip" },
];

const exportEvent = "aboard:export";

/** exportAs runs one of the Export menu's items on the board on screen, as picking it does. */
export function exportAs(action: ExportAction) {
  window.dispatchEvent(new CustomEvent<ExportAction>(exportEvent, { detail: action }));
}

const notAvailable = "Export isn't available on this server yet";

/** exportError is the line shown when an export fails: the API's own message, or a plain one for a server that has no export yet. */
function exportError(e: unknown): string {
  if (e instanceof ApiError) return e.status === 501 ? notAvailable : e.message;
  return "Couldn't export the board. Check the connection and try again.";
}

/** save hands the bytes to the browser as a download named filename. */
function save(blob: Blob, filename: string) {
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = filename;
  document.body.append(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10_000);
}

/**
 * copyText puts the text on the clipboard. Where the browser allows it the copy starts
 * inside the click and waits for the text, which Safari needs. A failed fetch is rethrown
 * as itself, since the browser reports a failed clipboard promise as its own error.
 */
async function copyText(get: () => Promise<string>) {
  let failed: unknown = null;
  const text = get().catch((e: unknown) => {
    failed = e;
    throw e;
  });
  try {
    if (typeof ClipboardItem !== "undefined" && navigator.clipboard?.write) {
      await navigator.clipboard.write([new ClipboardItem({ "text/plain": text.then((t) => new Blob([t], { type: "text/plain" })) })]);
    } else {
      await navigator.clipboard.writeText(await text);
    }
  } catch (e) {
    await text.catch(() => {});
    throw failed ?? e;
  }
}

/** ExportMessage is what the last export did: a confirmation, which fades, or an error, which stays. */
export type ExportMessage = { text: string; error: boolean };

/**
 * ExportNotice shows the last export's message in the flow of the header, so it never
 * lies over a control: beside the Export button on a wide screen, and on the line of the
 * board's labels on a phone. The menu's live region announces it, so this copy is hidden
 * from screen readers.
 */
export function ExportNotice({ message, className }: { message: ExportMessage | null; className?: string }) {
  if (!message) return null;
  return (
    <span aria-hidden title={message.text} className={cn("export-notice min-w-0 truncate text-meta animate-fade-in max-lg:max-w-full lg:max-w-[20rem]", message.error ? "export-error text-ink" : "export-toast text-muted", className)}>
      {message.text}
    </span>
  );
}

/**
 * ExportMenu sits in the board's action row. Its status line is a live region, so a
 * copy, a save or a failure is announced, and shown beside the button on a wide screen;
 * onMessage hands the same words to the header, which shows them on a phone.
 */
export function ExportMenu({ board, onMessage }: { board: string; onMessage?: (m: ExportMessage | null) => void }) {
  const [busy, setBusy] = useState<Action | null>(null);
  const [message, setMessage] = useState<ExportMessage | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);
  useEffect(() => onMessage?.(message), [message, onMessage]);

  const say = (text: string, error = false) => {
    clearTimeout(timer.current);
    setMessage({ text, error });
    // An error stays until the next export; a confirmation fades.
    if (!error) timer.current = setTimeout(() => setMessage(null), 3000);
  };

  const run = async (action: Action) => {
    setBusy(action);
    setMessage(null);
    const format: ExportFormat = action === "zip" ? "zip" : "md";
    try {
      if (action === "copy") {
        await copyText(async () => (await exportBoard(board, format)).blob.text());
        say("Copied");
      } else {
        const { blob, filename } = await exportBoard(board, format);
        save(blob, filename);
        say(`Saved ${filename}`);
      }
    } catch (e) {
      say(exportError(e), true);
    } finally {
      setBusy(null);
    }
  };

  // The finder picks an item through exportAs.
  useEffect(() => {
    const onExport = (e: Event) => void run((e as CustomEvent<ExportAction>).detail);
    window.addEventListener(exportEvent, onExport);
    return () => window.removeEventListener(exportEvent, onExport);
  });

  const icon = "pointer-events-none absolute left-3 size-4 text-muted";
  const icons = { copy: Copy, md: Download, zip: FileArchive };
  return (
    <div className="export-menu flex shrink-0 items-center gap-x-4">
      <DropdownMenu>
        <Tooltip>
          <TooltipTrigger asChild>
            <DropdownMenuTrigger
              disabled={busy !== null}
              aria-busy={busy !== null}
              aria-label={busy ? "Exporting…" : "Export"}
              className="export-trigger inline-flex min-h-11 shrink-0 items-center justify-center gap-1.5 rounded-control text-meta text-muted transition-colors duration-[140ms] ease-out hover:bg-hover hover:text-ink data-[state=open]:bg-selected data-[state=open]:text-ink disabled:text-muted max-sm:size-11 max-sm:text-ink sm:-mx-2 sm:px-2"
            >
              {busy ? <LoaderCircle className="size-4 shrink-0 motion-safe:animate-spin" strokeWidth={1.5} aria-hidden /> : <Download className="size-4 shrink-0" strokeWidth={1.5} aria-hidden />}
              <span className="max-sm:hidden" aria-hidden>
                {busy ? "Exporting…" : "Export"}
              </span>
            </DropdownMenuTrigger>
          </TooltipTrigger>
          <TooltipContent side="bottom" align="start">
            Export this board: copy it as Markdown, or download it as .md or .zip
          </TooltipContent>
        </Tooltip>
        <DropdownMenuContent align="start" className="min-w-[14rem]">
          {exportActions.map(({ action, label }) => {
            const Icon = icons[action];
            return (
              <DropdownMenuItem key={action} onSelect={() => void run(action)}>
                <Icon className={icon} strokeWidth={1.5} aria-hidden />
                {label}
              </DropdownMenuItem>
            );
          })}
        </DropdownMenuContent>
      </DropdownMenu>
      <ExportNotice message={message} className="max-lg:hidden" />
      {/* Announced here; shown by ExportNotice. */}
      <span role="status" aria-live="polite" className="sr-only">
        {message?.text}
      </span>
    </div>
  );
}
