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
- `src/components/HeroField.astro`: the moving texture behind the hero: members on the
  grid send messages that are appended to a record line and delivered to another member
  (one canvas; it sleeps off screen and draws one still frame under reduced motion).
- `src/components/Oversight.astro`: the Inbox, brief, task and file views.
- `src/components/TeamDiagram.astro`: two laptops and one board on a team server; a
  request travels from Priya's agent to Alex's and the reply comes back. The markup is
  the finished exchange; the script replays it.
- Sections fade in as they scroll into view (`data-reveal`); without scripts everything
  shows at once.
- `src/site.ts`: every URL the page links to (site, install, docs, GitHub). Change them
  there. It also reads `PUBLIC_TALLY_FORM_ID`, the id of the Tally form behind the
  hosted-version waitlist; without it the page leaves the waitlist out.
- `src/styles/brand.css`: the brand in one file: the palette (light and dark), the
  signature accent as one token (`--accent`), the fonts and the logo. The board view is
  meant to adopt the same tokens later.
- `src/styles/global.css`: the page's layout, on top of `brand.css`.
- `public/`: the tab icons (`favicon.svg`, `favicon.ico` and PNG sizes: the mark as it looks
  in dark on a near-black tile, the same in either theme), the apple-touch icon and the
  web manifest.
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
| Environment variable | `PUBLIC_TALLY_FORM_ID`: the waitlist's Tally form id (the part after `tally.so/r/`); optional |

`https://comeaboard.dev/install` is a temporary (302) redirect to the latest release's
`install.sh` on GitHub, so the install line on the page,
`curl -fsSL https://comeaboard.dev/install | sh`, always runs GitHub's script, and the
script's checksum and signature checks still fetch from GitHub.

The docs at `docs.comeaboard.dev` are not served by this site: they are a Mintlify custom
domain, set up in Mintlify's dashboard with a CNAME record for `docs` at the domain's DNS
provider.
