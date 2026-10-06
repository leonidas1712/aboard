import { type Page, expect, test } from "@playwright/test";

// One screenshot per scenario moment, view, width and theme, so the mocked features can
// be looked at side by side, and a click-through of the main paths. Run with
// `make lab-shots`.

type Shot = { name: string; query: string; theme?: "dark" | "light"; click?: string; hover?: string };

const team = "lab=team&board=checkout-v2";
const shots: Shot[] = [
  { name: "inbox", query: "lab=team&step=3&inbox=1" },
  { name: "inbox-workspace", query: "lab=workspace&step=3&inbox=1" },
  { name: "board", query: `${team}&step=3` },
  { name: "board-light", query: `${team}&step=3`, theme: "light" },
  { name: "chip-hover", query: `${team}&step=3`, hover: '.thread-tasks [data-task-chip="CHK-16"]' },
  { name: "timeline-filter", query: `${team}&step=3&filter=CHK-12` },
  { name: "tasks-threads", query: `${team}&step=3&view=tasks`, click: '[data-task="CHK-12"] .task-threads' },
  { name: "task-open", query: `${team}&step=3&task=CHK-12` },
  { name: "task-open-needs", query: `${team}&step=3&task=CHK-16` },
  { name: "files", query: `${team}&step=3&view=files` },
  { name: "files-light", query: `${team}&step=3&view=files`, theme: "light" },
  { name: "file-open", query: `${team}&step=3&view=files&artifact=explainer` },
  { name: "file-open-image", query: `${team}&step=3&view=files&artifact=wireframe` },
  { name: "solo", query: "lab=solo&step=2&board=blog-engine" },
  { name: "empty", query: "lab=empty&step=2&board=new-board" },
  { name: "busy-tasks", query: "lab=busy&board=platform&view=tasks" },
];

const widths = [
  { label: "desktop", viewport: { width: 1440, height: 900 }, panel: "&panel=closed" },
  { label: "mobile", viewport: { width: 390, height: 844 }, panel: "&panel=closed" },
];

async function settle(page: Page) {
  await expect(page.locator("[data-lab]")).toBeVisible();
  // The page has loaded once its loading placeholders are gone.
  await expect(page.locator('[role="status"]:not([aria-live]), [aria-label="Loading"]')).toHaveCount(0, { timeout: 15_000 });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(400);
}

test("click through: Inbox row to board, a task id to its panel, a file to its page, the switch to Tasks", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.goto("/?lab=team&step=3&panel=closed");
  await settle(page);
  // With asks waiting, the lab opens on the Inbox; number keys answer the selected ask.
  await expect(page.getByRole("heading", { name: /Inbox/ })).toBeVisible();
  const first = page.locator(".ask-detail h2");
  const asked = await first.textContent();
  await page.keyboard.press("1");
  await expect(page.locator(".ask-detail h2")).not.toHaveText(asked ?? "");
  await page.getByRole("link", { name: /^Open in Checkout v2/ }).click();
  await expect(page.locator(".task-panel")).toBeVisible();
  // A task id in a message opens its task in the side panel; its file opens there too.
  await page.locator(".message .task-ref", { hasText: "CHK-12" }).first().click();
  await expect(page.locator(".task-panel h3")).toHaveText("Move payment intents to the v2 API");
  await page.locator(".task-panel button", { hasText: "what-changed-in-payments.html" }).click();
  await expect(page.locator(".artifact-panel")).toBeVisible();
  await expect(page.locator(".artifact-panel iframe[sandbox=\"\"]")).toHaveCount(1);
  await page.getByRole("tab", { name: /Tasks/ }).click();
  await expect(page.locator(".task-board")).toBeVisible();
  await page.screenshot({ path: "lab/screenshots/click-through-desktop.png" });
});

test("a task chip opens its task, and the task narrows the conversation to its threads", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/?${team}&step=3&panel=closed`);
  await settle(page);
  // The refund-keys thread is about two tasks, and its row says both.
  const row = page.locator('li.thread[data-thread="m15"] .thread-tasks');
  await expect(row.locator("[data-task-chip]")).toHaveCount(2);
  await row.locator('[data-task-chip="CHK-12"]').click();
  await expect(page.locator(".task-panel h3")).toHaveText("Move payment intents to the v2 API");
  await expect(page.locator(".task-panel .thread-list > li")).toHaveCount(4);
  await page.getByRole("button", { name: "Show only CHK-12 in the conversation" }).click();
  await expect(page.locator(".task-filter")).toContainText("3 threads");
  await expect(page.locator('li.message[data-id="m13"]')).toBeHidden();
  await expect(page.locator('li.message[data-id="m17"]')).toBeVisible();
  await page.getByRole("button", { name: "Show everything" }).click();
  await expect(page.locator('li.message[data-id="m13"]')).toBeVisible();
});

test("Tell the team fills in a real message and sends it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/?${team}&step=3&task=CHK-12&panel=closed`);
  await settle(page);
  await page.locator(".tell").getByRole("button", { name: "Hold" }).click();
  await expect(page.locator(".tell textarea")).toHaveValue(/CHK-12: hold here/);
  await page.locator(".tell").getByRole("button", { name: "Send" }).click();
  await page.getByRole("button", { name: "See it in the conversation" }).click();
  await expect(page.getByText("CHK-12: hold here.").first()).toBeVisible();
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
      if (s.hover) {
        await page.locator(s.hover).first().scrollIntoViewIfNeeded();
        await page.locator(s.hover).first().hover();
        await page.waitForTimeout(500);
      }
      if (s.click) {
        await page.locator(s.click).first().click();
        await page.waitForTimeout(300);
      }
      await page.screenshot({ path: `lab/screenshots/${s.name}-${w.label}.png`, fullPage: w.label === "mobile" });
      expect(errors).toEqual([]);
    });
  }
}
