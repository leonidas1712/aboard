// The colour schemes the lab panel offers: the real UI's own (app/themes.ts).

import { themes } from "@/app/themes";

/** allThemes is every scheme the lab panel offers, the system's first. */
export const allThemes = themes.map((t) => ({ id: t.id, label: t.label }));

/** setTheme applies a scheme and remembers it, as the account menu does. */
export function setTheme(id: string) {
  try {
    localStorage.setItem("aboard.theme", JSON.stringify(id));
  } catch {
    // Not kept; it still applies.
  }
  if (id === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = id;
}
