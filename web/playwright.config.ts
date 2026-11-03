import { defineConfig, devices } from "@playwright/test";

// The smoke test builds aboard with the UI embedded and drives it in Stable Chrome. Run it
// with `make web-e2e` after `make web`.
const chromeExecutablePath = process.env.ABOARD_CHROME_PATH;

export default defineConfig({
  testDir: "e2e",
  timeout: 120_000,
  expect: { timeout: 15_000 },
  workers: 1,
  use: {
    ...devices["Desktop Chrome"],
    // Local runs use system Stable Chrome. CI supplies the path produced by its Chrome
    // setup action, making the browser under test explicit rather than bundled.
    channel: chromeExecutablePath ? undefined : "chrome",
    launchOptions: {
      ...(chromeExecutablePath ? { executablePath: chromeExecutablePath } : {}),
      chromiumSandbox: true,
    },
    colorScheme: "light",
  },
});
