import { defineConfig, devices } from "@playwright/test";

// The smoke test builds aboard with the UI embedded and drives it in Chromium. Run it
// with `make web-e2e` after `make web`.
export default defineConfig({
  testDir: "e2e",
  timeout: 120_000,
  expect: { timeout: 15_000 },
  workers: 1,
  use: { ...devices["Desktop Chrome"], colorScheme: "light" },
});
