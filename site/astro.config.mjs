// @ts-check
import { defineConfig } from "astro/config";

// A static site: `astro build` writes plain HTML with its CSS inlined, and two small
// scripts, to dist/.
export default defineConfig({
  site: "https://comeaboard.dev",
  output: "static",
  build: { inlineStylesheets: "always" },
  devToolbar: { enabled: false },
});
