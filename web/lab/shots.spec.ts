import { type Page, expect, test } from "@playwright/test";

// One screenshot per scenario moment, view, width and theme, so the mocked features can
// be looked at side by side. Run with `make lab-shots`.

type Shot = { name: string; query: string; theme?: "dark" | "light" };

const shots: Shot[] = [
  { name: "team-step1-lab-panel", query: "lab=team&step=1&board=checkout-v2&panel=open" },
  { name: "team-step1", query: "lab=team&step=1&board=checkout-v2" },
  { name: "team-step2", query: "lab=team&step=2&board=checkout-v2" },
  { name: "team-step3", query: "lab=team&step=3&board=checkout-v2" },
  { name: "team-step3-light", query: "lab=team&step=3&board=checkout-v2", theme: "light" },
  { name: "team-step3-tasks", query: "lab=team&step=3&board=checkout-v2&view=tasks" },
  { name: "team-step3-tasks-light", query: "lab=team&step=3&board=checkout-v2&view=tasks", theme: "light" },
  { name: "team-board-list", query: "lab=team&step=3" },
  { name: "solo-step1", query: "lab=solo&step=1&board=blog-engine" },
  { name: "solo-step2", query: "lab=solo&step=2&board=blog-engine" },
  { name: "solo-step2-tasks", query: "lab=solo&step=2&board=blog-engine&view=tasks" },
  { name: "empty-step1", query: "lab=empty&step=1&board=new-board" },
  { name: "empty-step2", query: "lab=empty&step=2&board=new-board" },
  { name: "busy", query: "lab=busy&board=platform" },
  { name: "busy-tasks", query: "lab=busy&board=platform&view=tasks" },
  { name: "busy-light", query: "lab=busy&board=platform", theme: "light" },
];

const widths = [
  { label: "desktop", viewport: { width: 1440, height: 900 }, panel: "&panel=closed" },
  { label: "mobile", viewport: { width: 390, height: 844 }, panel: "&panel=closed" },
];

async function settle(page: Page) {
  await expect(page.locator("[data-lab]")).toBeVisible();
  // The board view has loaded once its loading placeholders are gone.
  await expect(page.locator('[role="status"]')).toHaveCount(0, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
}

for (const w of widths) {
  for (const s of shots) {
    test(`${s.name} at ${w.label}`, async ({ page }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.setViewportSize(w.viewport);
      await page.emulateMedia({ colorScheme: s.theme ?? "dark" });
      await page.goto(`/?${s.query}${w.panel}`);
      await settle(page);
      await page.screenshot({ path: `lab/screenshots/${s.name}-${w.label}.png`, fullPage: w.label === "mobile" });
      expect(errors).toEqual([]);
    });
  }
}
