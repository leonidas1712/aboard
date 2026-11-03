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
    // Use system Stable Chrome unless an operator explicitly supplies its executable.
    // CI relies on the runner's distro package and never falls back to bundled Chromium.
    channel: chromeExecutablePath ? undefined : "chrome",
    launchOptions: {
      ...(chromeExecutablePath ? { executablePath: chromeExecutablePath } : {}),
      chromiumSandbox: true,
    },
    colorScheme: "light",
  },
});
