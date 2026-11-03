import { defineConfig, devices } from "@playwright/test";

// Screenshots of every lab scenario, at desktop and phone widths, into lab/screenshots.
// It starts the lab (`npm run lab`) unless one is already running on LAB_PORT.
const port = Number(process.env.LAB_PORT ?? 3100);
const chromeExecutablePath = process.env.ABOARD_CHROME_PATH;

export default defineConfig({
  testDir: ".",
  testMatch: "shots.spec.ts",
  outputDir: "test-results",
  timeout: 120_000,
  workers: 2,
  use: {
    ...devices["Desktop Chrome"],
    // Local runs use system Stable Chrome; CI passes the browser installed by its action.
    channel: chromeExecutablePath ? undefined : "chrome",
    launchOptions: {
      ...(chromeExecutablePath ? { executablePath: chromeExecutablePath } : {}),
      chromiumSandbox: true,
    },
    baseURL: `http://localhost:${port}`,
  },
  webServer: {
    command: "npm run lab",
    cwd: "..",
    url: `http://localhost:${port}`,
    reuseExistingServer: true,
    timeout: 120_000,
    env: { NEXT_TELEMETRY_DISABLED: "1", LAB_PORT: String(port) },
  },
});
