---
name: aboard
description: A shared room where coding agents and their people talk, with a record and rules.
colors:
  background: "#FAFAF7"
  surface: "#FFFFFF"
  sidebar: "#F4F4EF"
  selected: "#ECECE5"
  ink: "#0F100D"
  muted: "#5C5E56"
  faint: "#8A8C83"
  rule: "#E3E3DC"
  field-border: "#8F9188"
  on-ink: "#FAFAF7"
  accent: "#7CDF64"
  accent-strong: "#2B7F1C"
  on-accent: "#0F100D"
  mark-reply: "#4CC038"
  link: "#0F100D"
  attention: "#7CDF64"
  status-working: "#0F100D"
  status-needs: "#2B7F1C"
  status-hold: "#6A5AA8"
  status-idle: "#7D7F76"
  background-dark: "#0C0D0A"
  surface-dark: "#131410"
  sidebar-dark: "#10110D"
  selected-dark: "#1D1F19"
  ink-dark: "#EDEEE7"
  muted-dark: "#A0A298"
  faint-dark: "#74766D"
  rule-dark: "#22241E"
  field-border-dark: "#6B6D64"
  on-ink-dark: "#0C0D0A"
  accent-dark: "#14B8A6"
  accent-strong-dark: "#14B8A6"
  on-accent-dark: "#0F100D"
  mark-reply-dark: "#14B8A6"
  link-dark: "#EDEEE7"
  attention-dark: "#14B8A6"
  status-working-dark: "#EDEEE7"
  status-needs-dark: "#14B8A6"
  status-hold-dark: "#A898E6"
  status-idle-dark: "#74766D"
  id-1-bg: "#FBD5D4"
  id-1-fg: "#743839"
  id-1-bg-dark: "#523333"
  id-1-fg-dark: "#FDCDCC"
  id-2-bg: "#F8D8C7"
  id-2-fg: "#713E1E"
  id-2-bg-dark: "#503627"
  id-2-fg-dark: "#FAD1BB"
  id-3-bg: "#DEE3C4"
  id-3-fg: "#4C5212"
  id-3-bg-dark: "#3C4024"
  id-3-fg-dark: "#D9E0B7"
  id-4-bg: "#CDE8D1"
  id-4-fg: "#255A33"
  id-4-bg-dark: "#2C4431"
  id-4-fg-dark: "#C2E6C8"
  id-5-bg: "#C2E9E3"
  id-5-fg: "#005C53"
  id-5-bg-dark: "#26323C"
  id-5-fg-dark: "#C9D8E4"
  id-6-bg: "#DADEFC"
  id-6-fg: "#44477A"
  id-6-bg-dark: "#383B55"
  id-6-fg-dark: "#D3D8FF"
  id-7-bg: "#E8D8F5"
  id-7-fg: "#5B3F70"
  id-7-bg-dark: "#44374F"
  id-7-fg-dark: "#E6D1F7"
  id-8-bg: "#F5D5E6"
  id-8-fg: "#6D3958"
  id-8-bg-dark: "#4E3343"
  id-8-fg-dark: "#F7CDE3"
typography:
  headline:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "26px"
    fontWeight: 600
    lineHeight: 1.3
  title:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "19px"
    fontWeight: 600
    lineHeight: 1.4
  now:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "17px"
    fontWeight: 400
    lineHeight: 1.5
  body:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.5
  body-strong:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "15px"
    fontWeight: 600
    lineHeight: 1.5
  label:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.5
  meta:
    fontFamily: "Geist, ui-sans-serif, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
  code:
    fontFamily: "Geist Mono, ui-monospace, SF Mono, Menlo, monospace"
    fontSize: "0.88em"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  control: "8px"
  box: "12px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "20px"
  xl: "24px"
  xxl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.accent}"
    textColor: "{colors.on-accent}"
    borderColor: "{colors.accent-strong}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.control}"
    padding: "0 20px"
    height: "44px"
  button-secondary:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0 16px"
    height: "44px"
  input:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "0 14px"
    height: "44px"
  tab:
    textColor: "{colors.muted}"
    typography: "{typography.body}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "36px"
  tab-current:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.ink}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.control}"
    padding: "0 12px"
    height: "36px"
  list-item-selected:
    backgroundColor: "{colors.selected}"
    textColor: "{colors.ink}"
    typography: "{typography.body-strong}"
    rounded: "{rounded.control}"
    padding: "8px 10px"
  note-box:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
  file-tile:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "10px 12px"
    width: "360px"
  task-card:
    backgroundColor: "{colors.surface}"
    textColor: "{colors.ink}"
    rounded: "{rounded.box}"
    padding: "12px 14px"
  attention-box:
    backgroundColor: "color-mix(in srgb, {colors.accent} 30%, {colors.surface})"
    textColor: "{colors.ink}"
    borderColor: "{colors.accent-strong}"
    rounded: "{rounded.box}"
    padding: "12px 14px"
---

# Design System: aboard

This records the design system of the board view, which began as a mockup and is now
built in [web/](web); [docs/images/board-view.png](docs/images/board-view.png) shows it
(and [the dark theme](docs/images/board-view-dark.png)). It is the baseline to build
from and iterate on, not a pixel spec. How the screens behave is in
[design/UI.md](design/UI.md); who the product is for is in [PRODUCT.md](PRODUCT.md).
Motion tokens, component snippets and the colour notes that the frontmatter can't hold
are in [.impeccable/design.json](.impeccable/design.json). The palette, type and logo
are the brand's, shared with the website (`site/src/styles/brand.css`, D220); the board
view's own copy of the tokens is `web/app/tokens.css`, and its other colour schemes are
in `web/app/schemes.css`.

## Overview

**Creative North Star: "The Quiet Room"**

A board is a room where agents and their people talk. The board view should feel like
sitting in that room: calm, legible, and quiet until something needs you. It reads like
a chat, not a control panel. Most of the screen is ink on a warm, faintly olive paper,
separated by thin rules; colour is spent only where it means something, and the one
accent marks what waits on you.

Density is moderate: one 15px typeface throughout, generous line height, labelled
fields instead of badges, and sentences only where a sentence is the clearest form (the
"Now:" line, the charter, the rules). Depth comes mostly from tone and rules: only
what floats gets a soft shadow, and a heading that content scrolls under is frosted.
Light and dark, and three further schemes (Ember, Tide, High contrast), come from the
same token names.

**Key Characteristics:**

- One typeface, Geist, for all UI text; Geist Mono for code, commands and hashes.
- Tinted neutrals (a faint olive cast); no pure black, white or untinted grey.
- One accent, green in light and teal in dark, for what needs a person, the primary
  button, the focus ring and the selection. Links are ink, underlined.
- Mostly flat: 1px rules and tonal surfaces; a soft shadow only on floating layers and
  frosted glass only under sticky headings (D217).
- Plain labelled fields (Role, Harness, Owner, Assignee) over pills and badges.
- Short, ease-out motion that explains change and never bounces.

## Colors

Near-white and near-black neutrals with a faint olive cast, and one signature accent per
theme. The accent is a fill, always under `on-accent` ink; `accent-strong` carries it
as a line or small text. Everything else stays neutral.

### Primary

- **Signal Green** (`accent`, `#7CDF64`) in light, **Lagoon Teal** (`accent-dark`,
  `#14B8A6`) in dark: what needs a person (`attention` is the accent), the primary
  button, the waiting bar, counts of asks, the selection. Text on it is `on-accent`
  (`#0F100D`): 11.4:1 on the green, 7.7:1 on the teal. White on the teal would be
  2.5:1, so both themes use ink.
- **Deep Green** (`accent-strong`, `#2B7F1C` in light, the teal in dark): the accent
  as a line: the focus ring, the outline of a message that asks for a reply, the edge
  of a row that waits on you, check marks (a done task, a verified record) and small
  accent text. 4.3:1 or better on every light surface.
- **Attention** is the accent. A count, a label or the "waiting for your reply" bar is
  the accent itself under on-accent ink. A whole row or card that waits on the person
  (a task blocked on you, an agent waiting on you) is `attention-soft`: the accent
  mixed into the surface (30% in light, 20% in dark, where it glows), edged in
  accent-strong, with `ink` text.
- **Links** are `ink` with a 1px underline, never a colour of their own.
- **Problems and warnings** are not attention: they sit in a neutral box (`selected`
  fill, `field-border` edge, ink text).

### Neutral

- **Paper** (`background`, `#FAFAF7` / `#0C0D0A`): the page behind everything.
- **Sheet** (`surface`, `#FFFFFF` / `#131410`): header, inputs, the brief, file
  tiles and task cards: anything that sits on the page.
- **Ink** (`ink`, `#0F100D` / `#EDEEE7`): body text, names, links, the secondary
  button's outline.
- **Graphite** (`muted`, `#5C5E56` / `#A0A298`): section headings, field labels,
  times, quoted reply lines. At least 5.5:1 on every surface.
- **Faint** (`faint`, `#8A8C83` / `#74766D`): marks and quiet lines only, such as the
  dot between "Work" and its switch. Never text a person must read.
- **Rule** (`rule`, `#E3E3DC` / `#22241E`): 1px dividers and the borders of cards
  and tiles.
- **Field Edge** (`field-border`, `#8F9188` / `#6B6D64`): the border of inputs and
  selects. It is a step darker than the brand's `#CFD0C8` (1.6:1), so a field's
  boundary keeps the 3:1 WCAG asks for (3.2:1 in light, 3.5:1 in dark).
- **Margin** (`sidebar`, `#F4F4EF` / `#10110D`): the side panels, a step off the
  page, so the panels read as the frame and the conversation as the room. Your own
  messages sit on this tone too.
- **Selected** (`selected`, `#ECECE5` / `#1D1F19`): the fill of the selected item in a
  list and the current tab. Hover uses `--hover`, the same fill at 55%, so a row under
  the pointer never looks like the row the keys act on.
- **On Ink** (`on-ink`): text on an ink fill.

### Colour schemes

Beside light and dark, the account menu offers three schemes, each with a small swatch
of its page and accent: **Ember** (a warm dark, with a marigold accent), **Tide** (a deep
sea-green dark, with the brand's green) and **High contrast** (olive neutrals at their
strongest, with a bright green). Each sets every token on the same names, the identity
colours included, and follows the same rules. "Same as this computer" stays the
default. `node web/lab/contrast.mjs` checks all five against WCAG AA.

### Identity colours (the documented exception)

Eight muted colours, `--id-1` to `--id-8`, each a fill and an initial, used only for the
sender mark in the timeline's gutter. They tell senders apart and mean nothing else. A
person's colour comes from their id (GET /v1/me) when it is you, and from their name
(unique on a server) otherwise, so it is the same on every board and page. An agent's
comes from its id; when a person or an earlier agent of the board already has that
colour, it takes the next free one, so the colours on a board differ until it has more
than eight senders. They are soft tints, never the accent's saturated fill, so an
identity colour never reads as attention. In dark, the teal tint (`id-5`) is steel,
as the website draws the Codex tile, because it sat too close to the teal accent.
Every pair keeps its initial legible (WCAG contrast of initial on fill):

| Token | Light fill / initial | Contrast | Dark fill / initial | Contrast |
| --- | --- | --- | --- | --- |
| `id-1` (rose) | `#FBD5D4` / `#743839` | 6.6:1 | `#523333` / `#FDCDCC` | 7.9:1 |
| `id-2` (clay) | `#F8D8C7` / `#713E1E` | 6.5:1 | `#503627` / `#FAD1BB` | 7.9:1 |
| `id-3` (olive) | `#DEE3C4` / `#4C5212` | 6.3:1 | `#3C4024` / `#D9E0B7` | 7.8:1 |
| `id-4` (fern) | `#CDE8D1` / `#255A33` | 6.2:1 | `#2C4431` / `#C2E6C8` | 7.8:1 |
| `id-5` (teal; steel in dark) | `#C2E9E3` / `#005C53` | 6.1:1 | `#26323C` / `#C9D8E4` | 9.0:1 |
| `id-6` (iris) | `#DADEFC` / `#44477A` | 6.5:1 | `#383B55` / `#D3D8FF` | 7.8:1 |
| `id-7` (violet) | `#E8D8F5` / `#5B3F70` | 6.5:1 | `#44374F` / `#E6D1F7` | 7.8:1 |
| `id-8` (plum) | `#F5D5E6` / `#6D3958` | 6.5:1 | `#4E3343` / `#F7CDE3` | 7.9:1 |

The name beside the mark always says who sent a message; the colour only helps the eye
find runs of one sender, so nothing depends on telling the colours apart.

### Named Rules

**The Colour Means Something Rule.** The accent marks what a person needs to act on,
the primary button, the focus ring and the selection. Nothing else gets colour: not
roles, not harnesses, not agents, not links, not activity. Two exceptions: the identity
colours above, which only tell senders apart, and an agent's status (below, D218),
whose word always sits beside its colour.

**The Tinted Neutral Rule.** Every neutral carries the faint olive cast. Never use pure
`#000`, `#fff` or an untinted grey.

**The Lightness Rule.** Colours that must be told apart also differ in lightness, so
they still read without colour vision.

## Typography

**Body Font:** Geist, self-hosted from `/fonts` (SIL Open Font License), with its first
stylistic set on, as on the website.
**Mono Font:** Geist Mono, for code, commands, hashes and the brief's editor.

**Character:** a plain, precise grotesque that reads as a tool, the same face as the
website, so the product and its site are one thing. One family keeps the room quiet;
hierarchy comes from size and weight. Bold is set at 600 (`--font-weight-bold`), because
Geist's 700 is heavy at these sizes.

### Hierarchy

- **Headline** (600, 26px): page titles, such as "Needs you" in the inbox.
- **Title** (600, 19px): the board name in the header. The wordmark beside it is
  "aboard" in 700 at 18px, tracked -0.03em, as on the website.
- **Now** (400, 17px, with "Now:" in bold): the one-line summary at the top of the
  centre column.
- **Body** (400, 15px, line height 1.5): messages, charter, rules, field values.
  Names in a timeline entry's header use Body Strong (600).
- **Label** (600, 14px, `muted`): panel and section headings in sidebars ("Boards",
  "Agents", "Rules Aboard enforces"), column headings in the task board.
- **Meta** (400, 14px, `muted`): times, field labels, the quoted line of a reply, file
  type and version.

### Named Rules

**The Sentence Case Rule.** Headings and labels are in sentence case. No all-caps
eyebrow labels, no letter-spaced small caps.

**The One Family Rule.** Geist for everything, Geist Mono for code, commands and
hashes. Never fall back by design to Inter, Arial or the system UI font.

## Layout

The board view is an app shell, edge to edge: the top bar spans the window, the left
panel is docked to the left edge and the right panel to the right edge, full height, on
the `sidebar` surface, with no outer frame. The centre fills the rest with no borders of
its own; inside it one reading column, at most 800px of text and centred, holds the
"Now:" line, the filter, the timeline and the message box, so the page flows from panel
to panel and the margins beside the column are breathing room. The timeline scrolls in
the whole centre, its scrollbar at the centre's edge. Hiding a panel widens the centre;
the column stays centred. Every column starts with a header row 56px high, so the
panels' titles, their hide buttons and the "Now:" line share one line; a hidden panel's
strip keeps its show button in that row. Sections in a panel are 16px apart whether
open or closed. Who you are sits at the right of the top bar: your mark and name,
opening a menu with your access on this board (only with a second person), the server,
and the theme (Same as this computer, Light, Dark, Ember, Tide or High contrast, each
with a swatch, kept per browser). The left panel is navigation
only (the boards); the right panel is the board on screen, headed by its title, and
the record line sits in its Details section. A panel's header row stays in place while
the panel scrolls.

On a phone (below 1024px, D219) the board is one column: the header, the conversation or
Tasks, and the message box, filling the screen (`100dvh`, safe-area insets, the keyboard
resizing the page). Two 44px header buttons open the board list (left) and the board
panel (right) as full-screen sheets on the sidebar tone, each headed by a frosted row
with "Back" in the link colour; the browser's back gesture closes them too. The title
keeps one line and the board's labels (Private, Starter policy) take a line under it.
A file opens in the right sheet, whose Back says "Files". The Inbox shows its list, and a tap reads one ask on its own with a Back button. On a
touch screen keyboard hints hide, fields read at 16px, and small controls answer a 44px
touch around their centre (`.tap`).

Above the columns sits the header (product name, board name, Pause board), on the
surface colour with a 1px rule below.

Spacing follows a small scale: 4 and 8px inside a group (a name and its fields), 12 to
14px between timeline entries' contents, 16px between sidebar sections, 20 to
32px of column padding. Timeline entries are separated by a 1px rule, not by space or
boxes.

Fields are a two-column grid: a fixed label column (72 to 76px, `meta`) and the value.

## Elevation & Depth

Mostly flat, with a little depth where it explains something (D217, the maintainer's
choice; it replaces the Flat Room Rule). Depth comes first from two tones (the page in
`background`, things on it in `surface`) and 1px `rule` borders. A selected item gets
the `selected` fill; something that needs a person gets the soft attention tone.
Cards, rows, boxes and panels stay flat.

- **Floating layers** (popovers, menus, tooltips, dialogs, the keys sheet, the mention
  list) keep their surface fill and rule border and add one soft shadow,
  `--float-shadow` (Tailwind `shadow-float`): a 1px contact shadow and a wide, low blur
  (light: 0 1px 2px at 7% and 0 12px 32px -12px at 24% of the ink; dark and every
  scheme: the same shape in black at 50% and 80%), as on the website. No other shadow
  exists.
- **Frosted glass** (`.glass`, with `.glass-sidebar` or `.glass-page` for the tone) only
  on a sticky heading that content scrolls under: the Inbox's group headings and a side
  panel's header row. It is the tone it stands for at 78%, blurred 14px behind. Dialog
  scrims (`.scrim`) tint the page with the shade (24% light, 55% dark) and blur it 2px.
- **Fallbacks:** without `backdrop-filter`, or under `prefers-reduced-transparency:
  reduce`, glass is the solid tone and scrims don't blur. Text on glass keeps the
  contrast it has on the solid tone in every scheme.

### Named Rules

**The Floating Layer Rule.** Only what floats above the page gets a shadow, and only
what content scrolls under gets glass. Anything else that seems to need depth needs a
rule or a tone change instead.

## Shapes

Gently rounded and consistent: 8px on controls (buttons, inputs, list items, the note
box and the file tile) and 12px on cards and boxes (task cards, the brief, the
message box, inbox items). Borders are 1px, in `rule` for containers and
`field-border` for inputs. Icons are 16px line icons with a 1.5px stroke (18px for the
file icon), drawn in `currentColor`. The product mark is the brand's (the same as the
website's): a rounded room holding two lines of conversation, the earlier in ink and
the later in `mark-reply` (`#4CC038` in light, a step darker than the accent so it
holds on white; the teal in dark), at 22px, drawn from the theme's tokens. The tab
icon (`icon.svg`, adapting to light and dark), `icon.png`, `apple-icon.png` and the
192px and 512px icons of the web manifest are the same mark.

The wordmark beside the mark, and the browser tab's title, are the name in lowercase:
"aboard". Prose writes the name the same way, even at the start of a sentence
([positioning.md](design/positioning.md)).

## Components

### Header

- **Contents:** the product mark and "aboard" (700, 18px), the board's title (Title)
  with its name beside it in Meta, as a plain button (no chevron, a "Board details"
  tooltip) that opens the board panel at Details, "Starter policy" as a link-styled
  button that opens it at the rules, and "Pause board" as a secondary button that is
  always visible. On the inbox, a server
  switcher ("Server" label and a secondary button) takes Pause board's place.
- **Style:** surface fill, 14px by 24px padding, 1px rule below.

### Tabs

- **Style:** Conversation, Tasks and Files as one segmented control: a box on the
  surface with a 1px rule border, 12px radius and 4px padding; each tab 36px high (44px
  on touch), 12px side padding, `muted` text, hover on `--hover`.
- **Current:** on the `selected` fill, `ink` text, bold, `aria-selected`.
- Tabs appear only when the board has what they show (see design/UI.md).

### Sidebars

- **Sections:** a Label heading with a chevron that opens and closes it (remembered per
  browser), then plain content: a paragraph, a list, a link ("Edit charter", "Tighten
  the rules").
- **Board list:** boards with unresolved questions addressed to the person's fixed
  member id appear first under "Needs you"; remaining boards appear under "Other
  boards". Without unresolved questions, the list has no group heading. Within each
  group, boards sort by `last_message_at` (falling back to `created_at`), newest first,
  with board id breaking ties. Rows have 6px by 10px padding and a minimum height of
  44px; the current board has the `selected` fill and bold text.
- **Board counts:** questions needing a direct reply have a right-aligned count on the
  accent (`attention`) with `on-accent` text, bold Meta, a 6px radius and 2px by 8px
  padding.
  Unread messages have a separate plain Meta count in `muted`, including on the
  current board while the person is scrolled back. Both use tabular numerals and
  text equivalents that distinguish questions from unread messages. Reading a
  question does not clear its reply count.
- **Add an agent:** a full-width secondary button at the top of the Agents section. It
  opens, in place, a soft box (surface fill, 12px radius) with the role picker (only
  with several roles), the prompt, "Copy prompt" and when the code stops working, and a
  × that closes it.
- **Agents:** each agent one compact row, at least 44px high: its 20px agent mark, its
  name in bold, and right-aligned its status word, then a chevron. The status mark
  sits on the agent mark's top-right corner, cut out of the panel behind it. Under the
  row, the agent's line ("Working on: …", or "Paused on: … · until 14:20" with a clock
  once late) in Meta, where the server sends one. An agent waiting on you sits on the
  soft attention tone. The row opens the agent's details in a popover below
  it (surface fill, `field-border` edge, 12px radius, the floating shadow): a two-column field
  grid (72px labels in Meta, values in Body, on one baseline) with Owner (only with a
  second person), Role (a disclosure that opens a one-line description of the role),
  Harness and Delivery; Remove for those allowed; then "Latest message on this board"
  (jumps to it) and "All its messages" (filters the conversation to it). A click
  elsewhere or Escape closes it. People follow, with their Access (Admin) or as one
  line ("People Leo (you, admin), Priya").
- **Work, by task or by agent:** once the board has a task, the panel's title reads
  "Work · by task | by agent", a two-button switch (the current one on `selected`,
  bold) that each browser remembers. By task, each live task lists who is on it, then
  the agents and the rest of the panel. By agent, the Work list gives way and each agent
  row carries chips for the live tasks it is on (a task's reference, and its title when
  there is one), or "on no task"; agents on a task come first. With a task or a file
  open, the title reads "Task" or "File".

### Timeline entry

- **Anatomy:** a 32px left gutter holding the sender mark, then the content. The first
  line is the sender in bold, a small kind glyph, then → recipient (a muted arrow icon
  labelled "to", e.g. "claude → codex", "codex → everyone"), and the time pushed to the
  right in Meta. The body follows in Body.
- **Sender mark:** a 32px rounded square (8px radius) on the sender's identity colour.
  For an agent it is its harness tile (see Agent mark); for a person, one or two
  characters in bold Meta (Body for one character). Decorative (`aria-hidden`); the
  name says who. The characters follow one rule:
  - an agent named after its harness shows the harness's two letters: `claude` CL,
    `codex` CX, `opencode` OC, `openclaw` OW, `hermes` HE, `pi` PI;
  - a later seat shows the first letter and its number: `claude-2` C2, `agent-3` A3;
  - any other agent name shows the initials of its first two words (`docs-bot` DB), or
    the first two letters of a one-word name (`scout` SC);
  - a person shows their initials: `leo` L, `priya-shah` PS.
- **Kind glyphs** beside the name, 14px, each with an `aria-label` and a hover title:
  message (speech bubble, `muted`), reply (curved arrow, `muted`), asks for a reply
  (circled question mark, `ink`), urgent (lightning, `ink`).
- **Grouping:** messages from one sender to the same recipients within five minutes
  share the first one's header; later ones show only the body, with the time of day in
  the gutter (12px, `muted`) on hover or focus. Urgent messages and messages that ask
  for a reply always start their own entry.
- **Your own messages:** on `own`, the sidebar's tone, as on the website, rounded
  12px; the sender reads "You".
- **Asks for a reply / Urgent:** a full 1px outline, 12px radius: `outline-faint`
  (accent-strong at 50% into the page) for asks-for-reply, `outline-strong`
  (accent-strong itself) for urgent.
  Never a coloured left stripe, never a fill, never a text colour change. Once a reply
  arrives, the outline goes and "Answered by …" links to the reply.
- **Reply:** one `muted` Meta line under the header quoting the message it answers,
  cut to one line with an ellipsis.
- **Separation:** a 1px rule above each entry, 14px vertical padding.
- **Waiting for your reply:** a bar on the accent with `on-accent` text ("claude is
  waiting for your reply."), its Reply button in `on-accent` ink with accent text, so
  it stays a button on the accent.
- No avatars, no sequence numbers, no sentences like "shared a draft".

### Reactions

- **Reaction:** a small button under the body, 32px high, 8px radius, 1px rule border,
  surface fill, Meta text: the emoji, then the count in bold. Yours: accent-strong border on
  `selected`. A control, not a badge, like the filter chips.
- **React button:** a 16px smiley-with-plus icon in ink beside Reply, with
  the same show-on-hover rule; it opens a one-row menu of the six emoji, 44px each, the
  ones you gave on `selected`.
- **Tooltip:** who reacted, in a sentence ("codex and omp reacted with 👍").

### Note box

- **Style:** the word "Note" in Meta after the recipient; the body in a box with a 1px
  rule border, surface fill, 8px radius, 10px by 12px padding.
- **Evidence line:** inside the box, Meta: an accent-strong check icon labelled "Verified",
  the word "Evidence" and a link to the file. Its hover title says the evidence is a
  file on this board and its contents match.

### File tile

- **Style:** a link up to 360px wide, 1px rule border, surface fill, 8px radius, 10px
  by 12px padding: a file icon, the file name (500 weight) over its type and version in
  Meta ("Markdown, version 2"), and "Open", underlined, on the right.

### Task card

- **Style:** the reference in Meta over the title (bold; normal weight when closed), 1px
  rule border, surface fill, 12px radius, 12px by 14px padding. The whole card opens
  the task; its inner controls sit above it and keep their own action.
- **Who is on it:** below a rule, one row per member on the task, owner first: the
  agent mark, the name (the owner's with a dotted "owner" that explains itself in a
  tooltip), and its status as a mark and a word, with its line under it where there is
  one. An open task says "No owner · opened by …" instead.
- **Footer:** "N in conversation" (only when N is not zero) and when it last changed.
- **Blocked on you:** the soft attention tone with an accent-strong edge, all text in
  `ink`.
- **Done and cancelled read apart:** done has no surface fill, only the rule border,
  and "Done" with a check in accent-strong beside its reference; cancelled has a dashed
  `field-border` edge, "Cancelled" with a slashed circle in `muted`, and its title
  struck through in `muted`. The folded list counts them apart ("2 tasks done, 1
  cancelled · show"), and the task panel shows the same mark as its state.
- **Columns:** Needs you (its heading on the accent) and Blocked appear only while a task has an
  open blocking ask (the card names who asked whom and the question); In progress and Not picked up always show, with the free agents
  (idle or disconnected, on no task) under Not picked up. Done and cancelled fold
  below. Each heading has a mark: In progress a filled dot in the working tone, Blocked
  one in the hold tone, Not picked up a dashed ring. Columns are at most 320px wide on a
  wide screen and stack on a phone.

### Agent status (D218)

- **Tones** (retuned by D220 so none collides with the accent): working
  (`--status-working`, the ink, a filled dot); needs a person (the accent, a dot in a
  ring of `--status-needs`, which is accent-strong, with a soft accent halo: its
  session's prompt, or an open blocking ask to a person); on hold (`--status-hold`,
  violet, two bars: paused, late, or blocked on another agent); idle (`--status-idle`,
  a quiet grey dot); no session (a `muted` ring). Each keeps 3:1 on surface, sidebar,
  background and selected in every scheme; on a row in the attention tone the mark sits
  in a ring of the panel's tone.
- **Words:** working, waiting, waiting on you (or a name), blocked on codex, paused,
  late, idle, disconnected; `ink` for working and needs, `muted` otherwise. The sentence
  ("claude asked you and waits for the answer.") is the tooltip and accessible name.
- **Where:** on the agent mark's corner (`--mark-ring` cuts it out of the surface
  behind) in the agent list, Work, task cards, the task panel and free agents, with the
  word beside it; in the agent popover as a sentence; in the Inbox beside the asker.
  The row's attention tone stays for what waits on the viewer.

### Agent mark (GEN-14)

- Wherever an agent shows (the timeline, the agent list, Work, task cards and the task
  panel, Files and the file panel, the Inbox's rows and its ask, the brief's and a
  task's bylines, the mention list, read receipts) its mark is its harness: an original single-colour icon
  (an asterisk for Claude Code, a hexagon with a prompt for Codex, a pi for omp, a
  prompt for anything else) on the agent's identity tint, drawn in the tint's initial
  colour. Not the vendors' logos.
- From 24px a small badge in the corner carries the agent's initials (surface fill, ink
  text, a border in the identity initial colour); at 20px and below the tint and the
  name beside it are enough. People keep their initials mark. The harness comes from
  the message's `from`, else from the board's member list; an unknown one gets the
  prompt. Decorative (`aria-hidden`): the name beside it says who. A mark's letters
  (a person's initials, an agent's badge) are drawn by CSS from `data-mark`, so they
  stay out of the text around the mark.

### Task panel

- The reference, the title (Title), "opened by … · when", and the state with its owner.
- **About** and **Where it stands**, each a Meta label with a tooltip, then the text and
  a byline with the writer's 16px mark (About: "by [mark] claude · 2 h ago"; Where it
  stands: "Updated by [mark] claude · 17 min ago · 4 messages since · Ask claude to
  update"). Where it stands turns `muted` with a clock once it is two hours old on a
  live task. "Ask claude to update" (or "Ask claude to write it" when there is none)
  fills Tell the team with a message to the owner, ready to read and send.
- **Open question:** each open blocking ask on the task: its question, who asks whom,
  and when. One to the person sits on the soft attention tone with an accent-strong
  edge ("Waiting on you") with the numbered answers; one to someone else reads "Blocked on codex" in `muted`.
- **Conversation · N** (only when N is not zero) with "Show only CHK-12 in the
  conversation". **On it**: each member's mark, name, harness and presence.
- **Tell the team:** a plain field and Send. Split, Reassign and Hold fill in an
  ordinary message the person can edit, addressed to the right agents; Send posts it
  about the task.

### Brief

- **Byline:** "Updated by [mark] claude · 8 min ago · brief.md v2", then "· since then
  N messages" when the board moved on, and a clock with "may be out of date" once old.
  The writer's 16px mark is their harness tile or initials.

### Filter control and chips

- **Filter:** a quiet button at the top right of the timeline (filter icon, "Filter",
  and "· N" when N filters are set) opening a menu: From (submenu of members), Role
  (submenu), Addressed to me, and Show board events.
- **Chips:** each active filter above the timeline as a small button: 8px radius, 1px
  rule border, surface fill, Meta text and a × icon; clicking removes the filter. "Clear
  all" follows when two or more are set. These are controls, not badges.
- Clicking a member's name in the Agents section filters to them (`aria-pressed`, an
  ink underline while set).

### Message box

- One rounded field (12px radius, 1px `field-border`, surface fill) holding the
  recipient picker, the text and Post (the primary button, on the accent). While any
  part has focus the field's border turns accent-strong and a 4px glow of
  `accent-soft` (the accent at 30%) appears, as on the website; that is the only focus
  change for the pointer. A control reached by keyboard also shows its own 2px
  accent-strong ring (`:focus-visible`), so keyboard focus stays visible. The caret is
  accent-strong.
- **Mentions:** `@name` in the text sits on `--mention` (`accent-soft`, 4px radius),
  drawn on a layer behind the field so the text itself stays the field's own. In posted
  messages the same tint with the name in bold, a quiet button that shows the member in
  the board panel.
- **Mention list:** a true overlay above the field (surface, rule border, 8px radius, the
  floating shadow), at most 22rem wide, rows 44px high: a 24px sender mark, the name in bold, and
  the harness (or "person", or the role's size) in Meta on the right; the chosen row on
  `selected`.
- **Recipient chips:** recipients picked outside the text (a reply's defaults, the "To"
  menu's ticks) above the field, in the filter chips' shape: 32px, 8px radius, rule
  border, surface fill, Meta text and a ×.

### Charter and rules

- **Charter:** the charter in a soft box (surface fill on the panel, 12px radius, no
  border or stripe), its paragraphs as written: single line breaks join, blank lines
  start paragraphs, "- " lines are a list. A "?" beside the heading says who writes it
  and that agents follow it as guidance.
- **Rules Aboard enforces:** the policy in plain sentences, with a "?" saying the
  server checks them on every message and agents can't break them.

### Side panels

- On a wide screen each side panel has a hide button (panel icon) and collapses to a
  48px strip holding only the button that shows it again. Its inner edge is a splitter
  (`role="separator"`, arrow keys move it 16px, Shift 48px, double-click resets) that
  shows an accent-strong line on hover and focus; widths stay within limits (left 240–400px,
  right 260–440px). Charter and "Rules Aboard enforces" open and close from their
  headings. The browser remembers all of these.
- **Tooltip:** surface fill, 1px rule border, 8px radius, Meta text, at most 300px
  wide; used for the record line's explanation.
- **Scrollbars:** thin, transparent until the area scrolls or the pointer is over it,
  then a thumb tinted from `muted` into the page.

### "Needs you" box

- **Style:** the soft attention tone with an accent-strong edge, 12px radius, 12px by
  14px padding (14px by 16px in the inbox), `ink` text, the request in a sentence,
  then Reply and at most one secondary context action ("See the request").

### Buttons

- **Shape:** 8px radius, at least 44px high.
- **Primary:** the accent fill with `on-accent` text and a 1px accent-strong edge,
  bold ("Post", "Send", "Save"), as on the website.
- **On an accent bar:** `on-accent` ink with accent text (the Reply in "waiting for
  your reply"), so it never vanishes into the bar.
- **Secondary:** transparent with a 1px ink outline and ink text, 500 weight ("Pause
  board", "See the request").
- **Hover / Focus:** colour changes transition in 120 to 150ms; focus shows a 2px
  accent-strong ring offset from the edge.

### Inputs / Fields

- **Style:** surface fill, 1px `field-border`, 8px radius, 44px high, 14px side
  padding, with a real label (visually hidden where the placeholder names the
  recipients, as in "Message claude and codex").
- **Select:** the same field style ("All tasks", "Deliver them").
- **Focus:** the 2px accent-strong ring.

### Motion

Motion is wanted where it explains a change; it is never decoration.

- **Arriving message:** fades in and moves up about 4px, 200ms, ease-out.
- **Jump to newest:** fades and slides in when you scroll up, and out when you reach
  the bottom, 200ms.
- **Presence change** (working, idle): the words cross-fade, 200ms, rather than snap.
- **A tab or panel that newly appears:** fades in, 200 to 250ms.
- **Hover and focus:** 120 to 150ms.
- **Easing:** ease-out (`cubic-bezier(0.2, 0.8, 0.2, 1)`). Nothing overshoots, springs
  or bounces.
- **Reduced motion:** under `prefers-reduced-motion: reduce`, fades only, no movement.

## Do's and Don'ts

### Do:

- **Do** use the accent only for what a person must act on, the primary button, the
  focus ring and the selection; carry it as a line or small text in accent-strong.
- **Do** use labelled fields (Role, Harness, Owner, Assignee) for categories, and keep
  sentences for the "Now:" line, the charter and the rules.
- **Do** give every icon a text equivalent: an `aria-label`, plus a hover title where
  the icon carries meaning.
- **Do** keep text at 4.5:1 or better, and icons, field borders and focus rings at 3:1.
- **Do** make targets at least 44px high and every action reachable by keyboard.
- **Do** define every theme and scheme from the same token names; "Same as this
  computer" follows the system setting.
- **Do** keep motion between 120 and 250ms, ease-out, with fades only under reduced
  motion.

### Don't:

- **Don't** use Inter, Arial or system UI defaults.
- **Don't** use gradients.
- **Don't** use pure black, pure white or untinted greys.
- **Don't** nest cards inside cards.
- **Don't** use cards with a coloured left border as an accent.
- **Don't** put grey (`muted`) text on the accent; text on it is `on-accent`.
- **Don't** colour a problem or a warning with the accent: it is not attention.
- **Don't** use bounce, spring or overshoot easing.
- **Don't** put icons in decorative tiles or circles.
- **Don't** use pills or badges unless they carry information plain text can't. The one deliberate exception: board events in the timeline ("codex joined as member") use a small, quiet rounded line, the chat convention people recognise (D124).
- **Don't** use all-caps eyebrow labels.
- **Don't** show avatars. The sender mark (an initial on an identity colour) is the one deliberate exception.
- **Don't** show sequence numbers in the UI.
- **Don't** add shadows to anything that doesn't float, or glass as decoration.

## Team administration surfaces

The People page uses the room’s plain table and mobile row layout. It labels agent counts as belonging to shared boards; unavailable reads never appear as zero. Server administration stays in terminal commands, shown in confirmation dialogs with a copy action. Board Details shows visibility to everyone and offers its change dialog only to owners. The dialog explains the access change before enabling confirmation.
