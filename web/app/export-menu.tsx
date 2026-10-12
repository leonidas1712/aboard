"use client";

// The board's Export menu: copy the board as Markdown, or save it as a .md or .zip file.
// The server builds the document from what this person can read, so the page only
// fetches it and hands it on.

import { Copy, Download, FileArchive, LoaderCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { cn } from "@/lib/utils";
import { ApiError, type ExportFormat, exportBoard } from "./api";

type Action = "copy" | "md" | "zip";

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

/** ExportMenu sits in the board's action row. Its status line is a live region, so a copy, a save or a failure is announced as well as shown. */
export function ExportMenu({ board }: { board: string }) {
  const [busy, setBusy] = useState<Action | null>(null);
  const [message, setMessage] = useState<{ text: string; error: boolean } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined);
  useEffect(() => () => clearTimeout(timer.current), []);

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

  const icon = "pointer-events-none absolute left-3 size-4 text-muted";
  return (
    <div className="export-menu relative flex shrink-0">
      <DropdownMenu>
        <DropdownMenuTrigger
          disabled={busy !== null}
          aria-busy={busy !== null}
          aria-label={busy ? "Exporting…" : "Export"}
          title={busy ? "Exporting…" : "Export"}
          className="export-trigger inline-flex size-11 shrink-0 items-center justify-center rounded-control text-ink transition-colors duration-[140ms] ease-out hover:bg-hover data-[state=open]:bg-selected disabled:text-muted"
        >
          {busy ? <LoaderCircle className="size-4 motion-safe:animate-spin" strokeWidth={1.5} aria-hidden /> : <Download className="size-4" strokeWidth={1.5} aria-hidden />}
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="min-w-[14rem]">
          <DropdownMenuItem onSelect={() => void run("copy")}>
            <Copy className={icon} strokeWidth={1.5} aria-hidden />
            Copy as Markdown
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void run("md")}>
            <Download className={icon} strokeWidth={1.5} aria-hidden />
            Download .md
          </DropdownMenuItem>
          <DropdownMenuItem onSelect={() => void run("zip")}>
            <FileArchive className={icon} strokeWidth={1.5} aria-hidden />
            Download .zip
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
      {/* The status floats under the button so a message never changes the row's height. */}
      <span
        role="status"
        aria-live="polite"
        className={
          message
            ? cn(
                "absolute top-full right-0 z-20 w-max max-w-[min(20rem,calc(100vw-1.5rem))] rounded-control border border-rule bg-surface px-3 py-2 text-meta shadow-float animate-fade-in",
                message.error ? "export-error" : "export-toast text-muted",
              )
            : "sr-only"
        }
      >
        {message?.text}
      </span>
    </div>
  );
}
