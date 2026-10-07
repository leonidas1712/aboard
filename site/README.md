# aboard's website

The public landing page at [comeaboard.dev](https://comeaboard.dev): one static page
built with [Astro](https://astro.build). It is separate from the board view in
[`/web`](../web) and the docs in [`/docs`](../docs). It loads no analytics, trackers or
third-party scripts; the fonts (Geist and Geist Mono, SIL Open Font License) are
self-hosted from `public/fonts`.

## Run it locally

You need Node.js 22.12 or later.

```bash
cd site
npm install
npm run dev        # http://localhost:4321, reloads on save
```

To check the production build:

```bash
npm run check      # type checks
npm run build      # writes the static site to dist/
npm run preview    # serves dist/ at http://localhost:4321
```

The `/install` redirect lives in `vercel.json`, so it only works on Vercel, not in
`npm run dev` or `npm run preview`.

## Where things are

- `src/pages/index.astro`: the page and its copy.
- `src/components/BoardDemo.astro`: the scripted board in the first screen. Its markup
  is the finished conversation, so it reads without JavaScript and under reduced motion;
  the script replays it. It follows the board view's layout and components in `web/app`.
- `src/site.ts`: every URL the page links to (site, install, docs, GitHub). Change them
  there.
- `src/styles/brand.css`: the brand in one file: the palette (light and dark), the
  signature accent as one token (`--accent`), the fonts and the logo. The board view is
  meant to adopt the same tokens later.
- `src/styles/global.css`: the page's layout, on top of `brand.css`.
- `public/`: the favicon (`favicon.svg`, which follows light and dark, plus PNG sizes),
  the apple-touch icon and the web manifest.
- `vercel.json`: the `/install` redirect and cache headers.

## Deploy on Vercel

Import the repository into Vercel with these settings:

| Setting | Value |
| --- | --- |
| Root directory | `site` |
| Framework preset | Astro |
| Build command | `npm run build` |
| Output directory | `dist` |
| Node.js version | 22.x or later |
| Domain | `comeaboard.dev` |

`https://comeaboard.dev/install` is a temporary (302) redirect to the latest release's
`install.sh` on GitHub, so the install line on the page,
`curl -fsSL https://comeaboard.dev/install | sh`, always runs GitHub's script, and the
script's checksum and signature checks still fetch from GitHub.

The docs at `docs.comeaboard.dev` are not served by this site: they are a Mintlify custom
domain, set up in Mintlify's dashboard with a CNAME record for `docs` at the domain's DNS
provider.
