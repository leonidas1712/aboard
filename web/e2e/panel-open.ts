import type { Page } from "@playwright/test";

// The board panel's sections start closed. Specs that look inside them open them first,
// once per browser: a section the spec closes stays closed after a reload.
export async function openPanelSections(page: Page) {
  await page.addInitScript(() => {
    for (const id of ["board-agents", "charter", "rules", "board-details"]) {
      try {
        const key = `aboard.open.${id}`;
        if (localStorage.getItem(key) === null) localStorage.setItem(key, "true");
      } catch {
        // Not kept; the page works the same without it.
      }
    }
  });
}
