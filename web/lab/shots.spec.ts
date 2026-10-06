import { type Page, expect, test } from "@playwright/test";

// One screenshot per scenario moment, view, width and theme, so the mocked features can
// be looked at side by side. Run with `make lab-shots`.

type Shot = { name: string; query: string; theme?: "dark" | "light"; click?: string };

const team = "lab=team&board=checkout-v2";
const shots: Shot[] = [
  { name: "team-step1-lab-panel", query: `${team}&step=1&panel=open` },
  { name: "team-step1-now", query: `${team}&step=1` },
  { name: "team-step3-now", query: `${team}&step=3` },
  { name: "team-step3-now-light", query: `${team}&step=3`, theme: "light" },
  { name: "team-step3-brief-open", query: `${team}&step=3&view=timeline`, click: ".brief > button" },
  { name: "team-step3-timeline", query: `${team}&step=3&view=timeline` },
  { name: "team-step3-agent-details", query: `${team}&step=3&view=timeline`, click: '[data-agent="claude"] button[aria-expanded]' },
  { name: "team-step3-tasks", query: `${team}&step=3&view=tasks`, click: "#task-t1 .task-threads" },
  { name: "team-step3-files", query: `${team}&step=3&view=artifacts` },
  { name: "team-step3-files-light", query: `${team}&step=3&view=artifacts`, theme: "light" },
  { name: "team-step3-preview-status", query: `${team}&step=3&artifact=status` },
  { name: "team-step3-preview-brief", query: `${team}&step=3&artifact=brief` },
  { name: "solo-step2", query: "lab=solo&step=2&board=blog-engine" },
  { name: "solo-step2-tasks", query: "lab=solo&step=2&board=blog-engine&view=tasks" },
  { name: "empty-step2", query: "lab=empty&step=2&board=new-board" },
  { name: "busy-now", query: "lab=busy&board=platform" },
  { name: "busy-timeline", query: "lab=busy&board=platform&view=timeline" },
  { name: "busy-tasks", query: "lab=busy&board=platform&view=tasks" },
  { name: "workspace", query: "lab=workspace" },
  { name: "workspace-light", query: "lab=workspace", theme: "light" },
];

const widths = [
  { label: "desktop", viewport: { width: 1440, height: 900 }, panel: "&panel=closed" },
  { label: "mobile", viewport: { width: 390, height: 844 }, panel: "&panel=closed" },
];

async function settle(page: Page) {
  await expect(page.locator("[data-lab]")).toBeVisible();
  // The page has loaded once its loading placeholders are gone.
  await expect(page.locator('[role="status"], [aria-label="Loading"]')).toHaveCount(0, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
}

// Asking in place: an ask on a task is a message to its owner, who answers in a moment.
test("an ask from a task card lands in the timeline and gets an answer", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto(`/?${team}&step=3&view=tasks&panel=closed`);
  await settle(page);
  await page.locator("#task-t1").hover();
  await page.locator("#task-t1").getByRole("button", { name: "Ask the owner" }).click();
  await page.locator("#task-t1").getByRole("button", { name: "Send to claude" }).click();
  await page.locator("#task-t1").getByRole("button", { name: "See it in the timeline" }).click();
  await expect(page.getByText("On it. I'll answer in this thread when it's done.")).toBeVisible({ timeout: 10_000 });
  await page.waitForTimeout(500);
  await page.screenshot({ path: "lab/screenshots/team-step3-ask-answered-desktop.png" });
});

for (const w of widths) {
  for (const s of shots) {
    test(`${s.name} at ${w.label}`, async ({ page }) => {
      const errors: string[] = [];
      page.on("pageerror", (e) => errors.push(e.message));
      await page.setViewportSize(w.viewport);
      await page.emulateMedia({ colorScheme: s.theme ?? "dark" });
      await page.goto(`/?${s.query}${w.panel}`);
      await settle(page);
      if (s.click) {
        await page.locator(s.click).first().click();
        await page.waitForTimeout(300);
      }
      await page.screenshot({ path: `lab/screenshots/${s.name}-${w.label}.png`, fullPage: w.label === "mobile" });
      expect(errors).toEqual([]);
    });
  }
}
