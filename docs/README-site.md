# The docs site

For maintainers. The pages in this folder are published with
[Mintlify](https://www.mintlify.com/docs) at
[docs.comeaboard.dev](https://docs.comeaboard.dev). This file isn't a page: `.mintignore` leaves
it out. Facts about Mintlify below were checked against its docs on 2026-10-05; it
changes often, so check the linked page when something doesn't match.

## How it is built

- `docs.json` is the whole site's configuration: name, theme, colours, the navbar, and
  the navigation in two tabs (Docs and API reference). Every page is an `.mdx` file named
  by its path without the extension (`concepts/boards`). A page left out of the
  navigation is still published, so delete pages rather than hiding them. A page that
  moves keeps its old address working with an entry in `docs.json`'s `redirects`
  (`{"source": "/old", "destination": "/new"}`).
- **The sidebar tells one story,** in the order a reader needs it: Get started (what
  aboard is, then two agents talking within a minute), Work with your agents, Your team
  (with deploying a team server), Harnesses, Concepts, Reference, and Build and
  contribute. A new page goes where a reader would look for it on that path, not at the
  end. Long lists are nested groups, which Mintlify shows closed behind a chevron
  (`"expanded": false`); a nested group with a `root` page opens it when clicked.
  ([settings](https://www.mintlify.com/docs/organize/settings),
  [navigation](https://www.mintlify.com/docs/organize/navigation),
  [hidden pages](https://www.mintlify.com/docs/organize/hidden-pages))
- **The look is the brand's** (`site/src/styles/brand.css`): `docs.json` sets the
  colours (links and accents in the darker green `#2b7f1c` in light, so they keep 5:1 on
  the page, and the teal `#14b8a6` in dark), the backgrounds (`#fafaf7` and `#0c0d0a`),
  Geist from `fonts/`, the wordmark in `logo/` (light and dark, outlined so it needs no
  font) and the favicon, which is the same fixed tab icon as the website's. `style.css`
  adds Geist Mono for code, which `docs.json` has no key for. Mintlify loads every
  `.css` file in this folder on every page
  ([custom scripts and styles](https://www.mintlify.com/docs/customize/custom-scripts)).
- **Generated, never edited by hand:**
  - `cli/*.mdx` and the "CLI reference" group's page list in `docs.json` (a nested group
    in Reference),
    written by `make docs-cli` from `aboard help --json` (the help in
    `server/internal/cli/help.go`; its shape is `HelpOutput` in `spec/cli.yaml`).
  - `api-reference/openapi.yaml`, a copy of `spec/openapi.yaml` that `make docs-cli`
    writes. Mintlify reads only files inside the docs folder, so it can't use the spec
    in place. The API reference tab lists every endpoint from it, grouped by tag, with
    a playground set to `simple`, since the servers in the spec are local addresses.
    ([OpenAPI setup](https://www.mintlify.com/docs/api-playground/openapi-setup))
- `make docs-check` fails when either is out of date, and runs in `make check`. It needs
  only Go.
- Everything else is written by hand, in the voice of `engineering/writing.md`. Commands
  shown in the pages are covered by an e2e test or `e2e/RELEASE_CHECKLIST.md`
  (workflow rule 5).
- Images live in `images/` and are linked from the docs root (`/images/board-view.png`).
  Every screenshot has a light and a dark file (`tasks.png` and `tasks-dark.png`), shown
  as two `<img>` tags with `className="block dark:hidden"` and `"hidden dark:block"`, so
  each theme gets its own. The board view screenshots (`board-view` in the README and on
  the first page; `board-overview`, `inbox`, `tasks`, `files` and `brief` at the top of
  their pages) are all retaken from seeded boards with
  `cd web && DOCS_SHOTS=../docs/images npx playwright test e2e/docs-shots.spec.ts`
  after `make web`.
- MDX is stricter than Markdown: `{`, `}` and `<` in prose start expressions and tags, so
  escape them or put them in backticks; comments are `{/* … */}`, never `<!-- -->`.
  ([format text](https://www.mintlify.com/docs/create/text))

## Preview and check locally

The Mintlify CLI is the npm package `mint`, pinned in `package.json` here and installed
into `docs/node_modules`, never globally. It needs Node 20.17 or later, and the network on
first use: it downloads its preview app into `~/.mintlify`. Telemetry is turned off with
`DO_NOT_TRACK=1`. ([CLI](https://www.mintlify.com/docs/cli/commands))

```bash
make docs-preview   # serves the site at http://localhost:3000
make docs-links     # mint broken-links, then mint validate (a strict build, the OpenAPI file included)
```

Neither is in `make check`, since they need Node and the network. The contextual menu
(copy page, view as Markdown, open in ChatGPT or Claude) shows only on deployed sites,
not in the local preview.

## Deploy

Mintlify's GitHub app builds the site from this repository's `main` branch, from the
`docs` folder, on every push. Pull requests get preview deployments only on paid plans
(and the OSS programme), never from forks.
([GitHub](https://www.mintlify.com/docs/deploy/github),
[monorepo](https://www.mintlify.com/docs/deploy/monorepo),
[previews](https://www.mintlify.com/docs/deploy/preview-deployments))

What every deployed site gets with no setup, on the free plan too
([llms.txt](https://www.mintlify.com/docs/ai/llmstxt),
[Markdown export](https://www.mintlify.com/docs/ai/markdown-export),
[MCP](https://www.mintlify.com/docs/ai/model-context-protocol),
[skill.md](https://www.mintlify.com/docs/ai/skillmd)):

- `/llms.txt` and `/llms-full.txt`, for agents reading the docs;
- every page as Markdown, at its URL plus `.md`;
- a search MCP server at `<site>/mcp`;
- `/skill.md`, generated within a day of the first deploy.

The AI assistant needs the Pro plan. The free (Starter) plan includes a custom domain.
The [OSS programme](https://mintlify.com/oss-program) gives Pro free to non-commercial
open-source projects that aren't run by a company; whether Aboard qualifies is the
maintainer's call. ([pricing](https://www.mintlify.com/pricing))

## Ideas borrowed from other developer docs

- **Tailscale's quickstart:** numbered steps that end with something working, and
  everything else (options, deep dives) deferred to linked pages. Our quickstart's
  "In your agents" tab is three steps.
- **Stripe's "Get started":** an agent-first path at the top (Stripe's "agent setup"),
  then cards to the next pages. Our introduction ends in cards; the quickstart's first
  tab is the path where agents do the work.
- **A "how it works" page early** (Tailscale's "How Tailscale works"): what runs on your
  machine, what it writes and what it never does, before the concepts.
- **Mintlify's starter:** Get started, Concepts, Guides and Reference as separate groups,
  and the API reference generated from the OpenAPI file.
- **Mintlify's and Firecrawl's own docs:** a short introduction and the quickstart as the
  first two pages, then features in the order someone adopts them, with long lists
  folded into nested groups and a screenshot at the top of each feature page.

## Steps for the account owner

Once, by hand, in this order:

1. **Create a Mintlify account** at [app.mintlify.com](https://app.mintlify.com) (or
   `npx mint signup`) and finish onboarding, which asks for the site's subdomain: for
   example `aboard`, giving `aboard.mintlify.site`
   ([quickstart](https://www.mintlify.com/docs/quickstart)). Mintlify's pages name the
   default address both `<name>.mintlify.site` and `<name>.mintlify.app`; the dashboard's
   Overview shows the real one.
2. **Install the Mintlify GitHub app** from
   [Git settings](https://app.mintlify.com/settings/project/git-settings), choosing
   "Only select repositories" and `leonidas1712/aboard`. It needs an owner or admin of
   the repository.
3. **Point it at the docs:** on the same page, choose the repository and the branch
   `main`, turn on "docs.json is in a subdirectory" and enter `/docs` (no trailing
   slash). Saving starts the first deploy.
4. **Check the site** at the address the dashboard's Overview shows, and that
   `<site>/llms.txt` lists the pages.
5. **Optional, a custom domain**, such as `docs.<your domain>`: add it in
   [Custom domain](https://app.mintlify.com/settings/project/custom-domain), add the two
   TXT records it shows (`_acme-challenge` and `_cf-custom-hostname`), and once both are
   verified, a `CNAME` from the domain to `cname.mintlify.builders`. TLS is automatic.
   ([custom domain](https://www.mintlify.com/docs/customize/custom-domain))
6. **Optional, apply to the [OSS programme](https://mintlify.com/oss-program)** for
   pull-request previews and the AI assistant.

Then put the site's address in the README's "Learn more" and in the repository's About
box.
