import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { type Page, devices, expect, test } from "@playwright/test";

// Settings, against a real server in an isolated home: the account menu is short and
// opens Settings at Profile; each tab has its own address; the tabs go down the left and
// answer the arrow keys; on a phone they are a list, each opening alone with a way back.
// ABOARD_SETTINGS_SHOTS names a folder to keep screenshots of each in light and dark.

const repo = resolve(__dirname, "../..");
const home = mkdtempSync(join(tmpdir(), "aboard-settings-web-"));
const bin = join(home, "aboard");
const shots = process.env.ABOARD_SETTINGS_SHOTS;
let env: NodeJS.ProcessEnv;

function aboard(...args: string[]): string {
  return execFileSync(bin, args, { cwd: home, env, encoding: "utf8" });
}

function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const server = createServer();
    server.on("error", fail);
    server.listen(0, "127.0.0.1", () => {
      const port = (server.address() as { port: number }).port;
      server.close(() => done(port));
    });
  });
}

test.beforeAll(async () => {
  execFileSync("go", ["build", "-tags", "ui,webtest", "-o", bin, "./server/cmd/aboard"], { cwd: repo, stdio: "inherit" });
  env = {
    NODE_ENV: "test",
    PATH: process.env.PATH,
    HOME: home,
    USER: "alex",
    XDG_CONFIG_HOME: join(home, ".config"),
    XDG_DATA_HOME: join(home, ".local/share"),
    XDG_STATE_HOME: join(home, ".local/state"),
    ABOARD_LOCAL_ADDR: `127.0.0.1:${await freePort()}`,
    BROWSER: "true",
  };
});

test.afterAll(() => {
  try {
    if (env) aboard("down");
  } finally {
    rmSync(home, { recursive: true, force: true });
  }
});

const base = () => `http://${env.ABOARD_LOCAL_ADDR}`;

async function signIn(page: Page, board: string) {
  aboard("pair", "general", "--board", board, "--name", "writer", "--new");
  const link = JSON.parse(aboard("open", "--board", board, "--json"));
  await page.goto(link.url);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByRole("button", { name: /^You are alex/ })).toBeVisible();
}

async function shot(page: Page, name: string) {
  if (!shots) return;
  mkdirSync(shots, { recursive: true });
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.screenshot({ path: join(shots, `${name}.png`), animations: "disabled", fullPage: name.startsWith("phone") });
}

async function noSideScroll(page: Page) {
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
}

test("the account menu opens Settings, whose tabs have their own addresses and answer the arrow keys", async ({ page }) => {
  await signIn(page, "settings-desk");

  // The menu is short: who you are, the server, Settings, the theme and sign-out.
  await page.getByRole("button", { name: /^You are alex/ }).click();
  const menu = page.locator(".account-menu");
  await expect(menu).toContainText("This computer (local)");
  await expect(menu.getByRole("menuitem", { name: "Rename yourself" })).toHaveCount(0);
  await expect(menu.getByRole("menuitem", { name: "People", exact: true })).toHaveCount(0);
  await expect(menu).not.toContainText("Urgent messages while an agent works");
  await expect(menu.getByRole("menuitemradio", { name: "Dark" })).toBeVisible();
  await menu.getByRole("menuitem", { name: "Settings" }).click();

  // Settings opens at Profile: the handle with its rename, the server and the sign-in.
  await expect(page).toHaveURL(/[?&]view=settings&tab=profile\b/);
  await expect(page.getByRole("heading", { name: "Settings", level: 1 })).toBeVisible();
  const tabs = page.getByRole("tablist", { name: "Settings" });
  await expect(tabs).toHaveAttribute("aria-orientation", "vertical");
  const names = await tabs.getByRole("tab").allTextContents();
  expect(names[0]).toBe("Profile");
  expect(names.at(-1)).toBe("Appearance");
  expect(names).toContain("Agents");
  const profile = page.getByRole("tabpanel", { name: "Profile" });
  await expect(profile.locator(".handle")).toHaveText("alex");
  await expect(profile.getByRole("button", { name: "Rename yourself" })).toBeVisible();
  await expect(profile).toContainText("This computer (local)");
  await expect(profile.locator(".signed-in-until")).toHaveText(/^until /);
  await shot(page, "desktop-profile-light");

  // The arrows move between the tabs and show each, with its address; Home and End go to the ends.
  await tabs.getByRole("tab", { name: "Profile" }).focus();
  await page.keyboard.press("ArrowDown");
  const agents = tabs.getByRole("tab", { name: "Agents" });
  await expect(agents).toBeFocused();
  await expect(agents).toHaveAttribute("aria-selected", "true");
  await expect(page).toHaveURL(/[?&]tab=agents\b/);
  await expect(page.getByRole("tabpanel", { name: "Agents" })).toContainText("Urgent messages while an agent works");
  await expect(page.getByRole("tabpanel", { name: "Agents" })).toContainText("What your agents may do for you");
  await shot(page, "desktop-agents-light");
  await agents.focus();
  await page.keyboard.press("End");
  await expect(tabs.getByRole("tab", { name: "Appearance" })).toBeFocused();
  await page.keyboard.press("Home");
  await expect(tabs.getByRole("tab", { name: "Profile" })).toHaveAttribute("aria-selected", "true");
  await page.keyboard.press("ArrowUp");
  await expect(tabs.getByRole("tab", { name: "Appearance" })).toHaveAttribute("aria-selected", "true");
  await expect(tabs.getByRole("tab", { name: "Profile" })).toHaveAttribute("tabindex", "-1");

  // A tab's address opens it; the theme chosen there applies to the page and the menu.
  await page.goto(`${base()}/?view=settings&tab=appearance`);
  const appearance = page.getByRole("tabpanel", { name: "Appearance" });
  await expect(appearance.getByRole("radio", { name: "Same as this computer" })).toBeChecked();
  await appearance.getByRole("radio", { name: /^Dark/ }).check();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  await page.getByRole("button", { name: /^You are alex/ }).click();
  await expect(page.getByRole("menuitemradio", { name: "Dark" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Escape");
  await shot(page, "desktop-appearance-dark");
  await page.getByRole("tab", { name: "Agents" }).click();
  await shot(page, "desktop-agents-dark");
  await page.getByRole("tab", { name: "Profile" }).click();
  await shot(page, "desktop-profile-dark");
  await page.getByRole("tab", { name: "Appearance" }).click();
  await appearance.getByRole("radio", { name: "Same as this computer" }).check();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", /./);

  // An address for a tab this build doesn't have opens at Profile.
  await page.goto(`${base()}/?view=settings&tab=nothing`);
  await expect(page.getByRole("tab", { name: "Profile" })).toHaveAttribute("aria-selected", "true");
});

test.describe("on a phone", () => {
  const { defaultBrowserType: _, ...phone } = devices["iPhone 13"];
  test.use({ ...phone, colorScheme: "light" });

  test("Settings is a list of its tabs, and each opens alone with a way back", async ({ page }) => {
    await signIn(page, "settings-phone");
    await page.goto(`${base()}/?view=settings`);
    const list = page.getByRole("navigation", { name: "Settings" });
    await expect(list.getByRole("button", { name: /^Profile/ })).toBeVisible();
    await expect(page.getByRole("tablist")).toHaveCount(0);
    await noSideScroll(page);
    await shot(page, "phone-list-light");

    // A tap opens one; Back and the browser's back both return to the list.
    await list.getByRole("button", { name: /^Agents/ }).click();
    await expect(page).toHaveURL(/[?&]tab=agents\b/);
    await expect(page.getByRole("heading", { name: "Urgent messages while an agent works" })).toBeVisible();
    await noSideScroll(page);
    await shot(page, "phone-agents-light");
    await page.goBack();
    await expect(list.getByRole("button", { name: /^Agents/ })).toBeVisible();
    await list.getByRole("button", { name: /^Profile/ }).click();
    await expect(page.locator(".profile .handle")).toHaveText("alex");
    await shot(page, "phone-profile-light");
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await expect(list).toBeVisible();
    await expect(page).toHaveURL(/[?&]view=settings$/);

    // A tab's address opens that tab alone, and Back goes to the list.
    await page.goto(`${base()}/?view=settings&tab=appearance`);
    await page.getByRole("radio", { name: /^Dark/ }).check();
    await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
    await noSideScroll(page);
    await shot(page, "phone-appearance-dark");
    await page.getByRole("button", { name: "Settings", exact: true }).click();
    await shot(page, "phone-list-dark");
    await list.getByRole("button", { name: /^Agents/ }).click();
    await shot(page, "phone-agents-dark");
    await page.goBack();
    await list.getByRole("button", { name: /^Profile/ }).click();
    await shot(page, "phone-profile-dark");
  });
});
