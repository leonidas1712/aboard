import { type Page, expect, test } from "@playwright/test";

// One screenshot per scenario moment, view, width and theme, so the mocked features can
// be looked at side by side, and a click-through of the main paths. Run with
// `make lab-shots`.

type Shot = { name: string; query: string; theme?: "dark" | "light"; click?: string | string[]; hover?: string };

const team = "lab=team&board=checkout-v2";
const shots: Shot[] = [
  { name: "inbox", query: "lab=team&step=3&inbox=1" },
  { name: "board", query: `${team}&step=3&by=task` },
  { name: "work-by-agent", query: `${team}&step=3&by=agent` },
  { name: "work-by-agent-light", query: `${team}&step=3&by=agent`, theme: "light" },
  { name: "people", query: `${team}&step=3&by=task`, click: "#lab-people button" },
  { name: "agent-popover", query: `${team}&step=3&by=task`, click: '[data-agent="claude"] > button' },
  { name: "agent-narrowed", query: `${team}&step=3&by=task&agent=tester` },
  { name: "add-agent", query: `${team}&step=3&by=task`, click: "button.add-agent" },
  { name: "chip-hover", query: `${team}&step=3`, hover: '.thread-tasks [data-task-chip="CHK-16"]' },
  { name: "timeline-filter", query: `${team}&step=3&filter=CHK-12` },
  { name: "tasks", query: `${team}&step=3&view=tasks` },
  { name: "task-owner-tooltip", query: `${team}&step=3&view=tasks`, hover: '[data-task="CHK-12"] .owner-label' },
  { name: "task-panel-threads", query: `${team}&step=3&view=tasks`, click: '[data-task="CHK-12"] .task-threads' },
  { name: "task-panel-chk16", query: `${team}&step=3&task=CHK-16` },
  { name: "task-panel-about-tooltip", query: `${team}&step=3&task=CHK-12`, hover: ".task-panel h4:has-text('Where it stands')" },
  { name: "brief-edit", query: `${team}&step=3`, click: [".brief button[aria-expanded]", ".brief button:has-text('Edit')"] },
  { name: "files", query: `${team}&step=3&view=files` },
  { name: "file-open", query: `${team}&step=3&view=files&artifact=explainer` },
  { name: "theme-ember", query: `${team}&step=3&by=agent&theme=ember` },
  { name: "theme-tide", query: `${team}&step=3&view=tasks&theme=tide` },
  { name: "theme-contrast", query: `${team}&step=3&by=agent&theme=contrast` },
  { name: "theme-light-marks", query: `${team}&step=3&by=agent&theme=light`, theme: "light" },
  { name: "inbox-ember", query: "lab=workspace&step=3&inbox=1&theme=ember" },
  { name: "solo", query: "lab=solo&step=2&board=blog-engine" },
  { name: "empty", query: "lab=empty&step=2&board=new-board" },
  // Onboarding (GEN-41): the inviter's side, then the colleague's.
  ...onboarding(),
];

function onboarding(): Shot[] {
  const alex = "lab=onboard-inviter";
  const sam = "lab=onboard-joiner";
  const invite = "item=apr_01K7Q2M8ZC4T9V3XWHN6RB5JDE";
  const both: Shot[] = [
    { name: "ob-approval-invite", query: `${alex}&step=1&inbox=1&${invite}` },
    { name: "ob-approval-invite-always", query: `${alex}&step=1&inbox=1&${invite}`, click: ".ob-approval button:has-text('Allow always')" },
    { name: "ob-approval-allowed-once", query: `${alex}&step=1&inbox=1&${invite}`, click: ".ob-approval button:has-text('Allow once')" },
    { name: "ob-approval-risky", query: `${alex}&step=1&inbox=1&item=apr_01K7Q2P4HS8WQ2NB6YT3MXK9RA` },
    { name: "ob-approval-add-people", query: `${alex}&step=3&inbox=1&item=apr_01K7Q3B9GT2XV6PM4ZQ8NHJ5CW` },
    { name: "ob-approval-declined", query: `${alex}&step=1&inbox=1&item=apr_01K7Q2P4HS8WQ2NB6YT3MXK9RA`, click: ".ob-approval button:has-text('Decline')" },
    { name: "ob-notice-open", query: `${alex}&step=2&inbox=1&item=inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS` },
    { name: "ob-notice-revoked", query: `${alex}&step=2&inbox=1&item=inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS`, click: ".ob-notice button:has-text('Revoke')" },
    { name: "ob-notice-redeemed", query: `${alex}&step=3&inbox=1&item=inv_01K7Q2R7XJ5NE4AD9M2TPW8BKS` },
    { name: "ob-notice-expired", query: `${alex}&step=3&inbox=1&item=inv_01K7NZ3WQ8T5HC2RV9MB6XJ4DP` },
    { name: "ob-pairing-out-account", query: `${alex}&step=2&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD` },
    { name: "ob-pairing-out-session", query: `${alex}&step=3&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD` },
    { name: "ob-pairing-out-declined", query: `${alex}&step=3&inbox=1&item=prq_01K7P4K2TZ9BC6WM8XR3HQ5NJV` },
    { name: "ob-pairing-in-expired", query: `${alex}&step=3&inbox=1&item=prq_01K7P1D5RW8KM3XT6HB9QZ2NC4` },
    { name: "ob-settings-off", query: `${alex}&step=1&settings=1` },
    { name: "ob-settings-auto-on", query: `${alex}&step=1&settings=1&allow=add-people` },
    { name: "ob-settings-invite-confirm", query: `${alex}&step=1&settings=1&allow=add-people`, click: "#ob-invite-switch" },
    { name: "ob-settings-invite-on", query: `${alex}&step=1&settings=1&allow=add-people,invite-people` },
    { name: "ob-account-menu", query: `${alex}&step=4&board=api-review`, click: "button.account" },
    { name: "ob-board-waiting", query: `${alex}&step=4&board=api-review` },
    { name: "ob-board-verifying", query: `${alex}&step=5&board=api-review` },
    { name: "ob-board-ready", query: `${alex}&step=6&board=api-review` },
    { name: "ob-join-page", query: `${sam}&step=1&join=1` },
    { name: "ob-join-terminal", query: `${sam}&step=1&join=1`, click: "button:has-text('Set it up in a terminal')" },
    { name: "ob-pairing-in-new", query: `${sam}&step=2&inbox=1` },
    { name: "ob-pairing-in-choose", query: `${sam}&step=2&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD`, click: ".ob-pairing button:has-text('Choose an agent')" },
    { name: "ob-pairing-in-prompt", query: `${sam}&step=2&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD`, click: ".ob-pairing button:has-text('Copy prompt')" },
    { name: "ob-pairing-in-sent", query: `${sam}&step=2&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD`, click: [".ob-pairing button:has-text('Choose an agent')", ".ob-choose button:has-text('Send it to this session')"] },
    { name: "ob-pairing-in-declined", query: `${sam}&step=2&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD`, click: ".ob-pairing button:has-text('Decline')" },
    { name: "ob-pairing-in-verifying", query: `${sam}&step=4&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD` },
    { name: "ob-pairing-in-ready", query: `${sam}&step=5&inbox=1&item=prq_01K7Q2R9MB4XH7TN2QW6KZ8CJD` },
    { name: "ob-board-sam-waiting", query: `${sam}&step=3&board=api-review` },
    { name: "ob-board-sam-ready", query: `${sam}&step=5&board=api-review` },
  ];
  // Each in dark (the shots' default) and in light.
  return both.flatMap((s) => [s, { ...s, name: `${s.name}-light`, theme: "light" as const, query: `${s.query}&theme=light` }]);
}

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
  await expect(page.locator(".task-panel .thread-list > li")).toHaveCount(5);
  await page.getByRole("button", { name: "Show only CHK-12 in the conversation" }).click();
  await expect(page.locator(".task-filter")).toContainText("5 in conversation");
  await expect(page.locator('li.message[data-id="m13"]')).toBeHidden();
  await expect(page.locator('li.message[data-id="m17"]')).toBeVisible();
  await page.getByRole("button", { name: "Show everything" }).click();
  await expect(page.locator('li.message[data-id="m13"]')).toBeVisible();
});

test("an agent's popover jumps to its latest message, and narrows the conversation to it", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/?${team}&step=3&by=task&panel=closed`);
  await settle(page);
  await page.locator('[data-agent="tester"] > button').first().click();
  await page.getByRole("button", { name: /Latest message on this board/ }).click();
  await expect(page.locator('li.message[data-id="m18"]')).toBeInViewport();
  await page.locator('[data-agent="tester"] > button').first().click();
  await page.getByRole("button", { name: "All its messages" }).click();
  await expect(page.locator(".task-filter")).toContainText("tester");
  await expect(page.locator('li.message[data-id="m17"]')).toBeHidden();
  await page.getByRole("button", { name: "Show everything" }).click();
  await expect(page.locator('li.message[data-id="m17"]')).toBeVisible();
});

test("a whole task card opens its task, and its inner controls keep their own action", async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(`/?${team}&step=3&view=tasks&panel=closed`);
  await settle(page);
  // CHK-19 is blocked on an ask between agents, so it is in Blocked, not Waiting.
  await expect(page.locator("#tasks-blocked").locator("..").locator('[data-task="CHK-19"]')).toBeVisible();
  // A click on the card's body, away from its title, opens the task.
  const card = page.locator('[data-task="CHK-12"]');
  const box = (await card.boundingBox())!;
  await page.mouse.click(box.x + box.width - 12, box.y + box.height / 2);
  await expect(page.locator(".task-panel h3")).toHaveText("Move payment intents to the v2 API");
  // An agent's name on a card opens its popover in the Work panel instead.
  await card.getByRole("button", { name: "reviewer" }).click();
  await expect(page.locator('[data-agent="reviewer"] .agent-popover')).toBeVisible();
  // So does the keyboard: the card's title is a real button.
  await page.locator('[data-task="CHK-18"] .card-open').focus();
  await page.keyboard.press("Enter");
  await expect(page.locator(".task-panel h3")).toHaveText("Load-test checkout at 3x traffic");
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
      for (const c of typeof s.click === "string" ? [s.click] : (s.click ?? [])) {
        await page.locator(c).first().click({ force: true });
        await page.waitForTimeout(300);
      }
      await page.screenshot({ path: `lab/screenshots/${s.name}-${w.label}.png`, fullPage: w.label === "mobile" });
      expect(errors).toEqual([]);
    });
  }
}
