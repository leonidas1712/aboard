// Login codes enter through stdin and are never written to test output or argv.
const fs = require("node:fs");
const path = require("node:path");
const { createRequire } = require("node:module");
const { chromium } = createRequire(path.join(process.cwd(), "package.json"))("@playwright/test");

(async () => {
  const input = JSON.parse(fs.readFileSync(0, "utf8"));
  const browser = await chromium.launch();
  try {
    const page = await browser.newPage();
    await page.goto(input.url);
    await page.getByRole("button", { name: "Continue", exact: true }).click();
    const response = page.waitForResponse((r) => r.request().method() === "POST" && /\/messages$/.test(new URL(r.url()).pathname));
    await page.getByRole("button", { name: input.option }).click();
    if ((await response).status() !== 201) throw new Error("answer refused");
  } finally {
    await browser.close();
  }
})().catch(() => {
  console.error("The isolated ask browser flow failed.");
  process.exitCode = 1;
});
