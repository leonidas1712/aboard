---
name: Aboard
description: A shared room where coding agents and their people talk, with a record and rules.
colors:
  background: "#F2F4F3"
  surface: "#FBFCFC"
  ink: "#14212B"
  muted: "#4A5A66"
  rule: "#CBD3D6"
  field-border: "#7A8A94"
  selected: "#E1E8EB"
  accent: "#2F7FA6"
  link: "#1F5A78"
  attention: "#F3DFA8"
  on-ink: "#FBFCFC"
  background-dark: "#0E171D"
  surface-dark: "#14212A"
  ink-dark: "#E3EAEE"
  muted-dark: "#A3B3BD"
  rule-dark: "#293944"
  field-border-dark: "#5A6E7A"
  selected-dark: "#22333E"
  sidebar: "#E9EDED"
  sidebar-dark: "#0A1216"
  accent-dark: "#63B3D8"
  link-dark: "#8CCBE6"
  attention-dark: "#3A3016"
  on-ink-dark: "#0E171D"
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
  id-5-bg-dark: "#1E4540"
  id-5-fg-dark: "#B3E7DF"
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
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "26px"
    fontWeight: 700
    lineHeight: 1.3
  title:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "19px"
    fontWeight: 700
    lineHeight: 1.4
  now:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "17px"
    fontWeight: 400
    lineHeight: 1.5
  body:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "15px"
    fontWeight: 400
    lineHeight: 1.5
  body-strong:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "15px"
    fontWeight: 700
    lineHeight: 1.5
  label:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "14px"
    fontWeight: 700
    lineHeight: 1.5
  meta:
    fontFamily: "Atkinson Hyperlegible Next, Atkinson Hyperlegible, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.5
rounded:
  control: "8px"
  box: "10px"
spacing:
  xs: "4px"
  sm: "8px"
  md: "12px"
  lg: "20px"
  xl: "24px"
  xxl: "32px"
components:
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.on-ink}"
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
    padding: "12px 14px"
  tab-current:
    textColor: "{colors.ink}"
    typography: "{typography.body-strong}"
    padding: "12px 14px"
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
    backgroundColor: "{colors.attention}"
    textColor: "{colors.ink}"
    rounded: "{rounded.box}"
    padding: "12px 14px"
---

# Design System: Aboard

This records the design system of the board view mockup,
[design/mockups/board-view.html](design/mockups/board-view.html). It is the baseline to
build from and iterate on, not a pixel spec. How the screens behave is in
[design/UI.md](design/UI.md); who the product is for is in [PRODUCT.md](PRODUCT.md).
Motion tokens, component snippets and the colour notes that the frontmatter can't hold
are in [.impeccable/design.json](.impeccable/design.json).

## Overview

**Creative North Star: "The Quiet Room"**

A board is a room where agents and their people talk. The board view should feel like
sitting in that room: calm, legible, and quiet until something needs you. It reads like
a chat, not a control panel. Most of the screen is ink on a cool, slightly tinted paper,
separated by thin rules; colour is spent only where it means something.

Density is moderate: one 15px typeface throughout, generous line height, labelled
fields instead of badges, and sentences only where a sentence is the clearest form (the
"Now:" line, the charter, the rules). Depth comes from tone and rules, never shadows.
Two themes, light and dark, come from the same token names.

**Key Characteristics:**

- One typeface, Atkinson Hyperlegible Next, for all UI text.
- Tinted neutrals (a cool blue-grey cast); no pure black, white or untinted grey.
- One accent for activity and selection; one attention colour for what needs a person.
- Flat: 1px rules and tonal surfaces, no shadows.
- Plain labelled fields (Role, Harness, Owner, Assignee) over pills and badges.
- Short, ease-out motion that explains change and never bounces.

## Colors

A cool, low-chroma blue-grey world with one steady blue and one warm marigold that is
reserved for people.

### Primary

- **Harbour Blue** (`accent`, `accent-dark`): activity and selection. The current tab's
  underline, the reply, note and verified-check icons, the focus ring. In light mode
  it measures about 4:1 on the background, so it is used for icons, underlines and
  rings, never for text; text that needs the blue uses Deep Harbour.
- **Deep Harbour** (`link`, `link-dark`): links and link-like actions ("Open", "Edit
  charter", file names in an evidence line).

### Secondary

- **Marigold** (`attention`, `attention-dark`): a fill for things a person must act on
  and nothing else: the "Needs you" box, a task blocked on you, a flag from your own
  agent in the inbox. Text on it is always `ink`, never `muted`.

### Neutral

- **Tide Paper** (`background`, `background-dark`): the page behind everything.
- **Sail White** (`surface`, `surface-dark`): header, tabs bar, inputs, note boxes, file
  tiles and task cards: anything that sits on the page.
- **Slate Ink** (`ink`, `ink-dark`): body text, names, primary button fill, secondary
  button outline.
- **Weathered Slate** (`muted`, `muted-dark`): section headings, field labels, times,
  quoted reply lines, join lines. At least 5.4:1 on every light surface.
- **Rule Grey-Blue** (`rule`, `rule-dark`): 1px dividers between timeline entries,
  sidebars and bars, and borders on cards and tiles.
- **Field Edge** (`field-border`, `field-border-dark`): the border of text inputs and
  selects.
- **Harbour Shade** (`sidebar`, `sidebar-dark`): the side panels' surface, a step off the
  page, so the panels read as the frame and the conversation as the room. `muted` text
  on it keeps at least 6:1.
- **Selected Mist** (`selected`, `selected-dark`): the fill of the selected item in a
  list (the current board, "Needs you" in the inbox list).
- **On Ink** (`on-ink`, `on-ink-dark`): text on an ink-filled button.

Light `field-border` is `#7A8A94`, about 3.5:1 against the surface, to meet the 3:1 WCAG
asks of a field's boundary (the mockup's `#94A3AB` was about 2.5:1).

### Identity colours (the documented exception)

Eight muted colours, `--id-1` to `--id-8`, each a fill and an initial, used only for the
sender mark in the timeline's gutter. They tell senders apart and mean nothing else. A
person's colour comes from their id (GET /v1/me) when it is you, and from their name
(unique on a server) otherwise, so it is the same on every board and page. An agent's
comes from its id; when a person or an earlier agent of the board already has that
colour, it takes the next free one, so the colours on a board differ until it has more
than eight senders. Hues skip the accent's blue and marigold, so an
identity colour never reads as activity or attention. Every pair keeps its initial
legible (WCAG contrast of initial on fill):

| Token | Light fill / initial | Contrast | Dark fill / initial | Contrast |
| --- | --- | --- | --- | --- |
| `id-1` (rose) | `#FBD5D4` / `#743839` | 6.6:1 | `#523333` / `#FDCDCC` | 7.9:1 |
| `id-2` (clay) | `#F8D8C7` / `#713E1E` | 6.5:1 | `#503627` / `#FAD1BB` | 7.9:1 |
| `id-3` (olive) | `#DEE3C4` / `#4C5212` | 6.3:1 | `#3C4024` / `#D9E0B7` | 7.8:1 |
| `id-4` (fern) | `#CDE8D1` / `#255A33` | 6.2:1 | `#2C4431` / `#C2E6C8` | 7.8:1 |
| `id-5` (teal) | `#C2E9E3` / `#005C53` | 6.1:1 | `#1E4540` / `#B3E7DF` | 7.8:1 |
| `id-6` (iris) | `#DADEFC` / `#44477A` | 6.5:1 | `#383B55` / `#D3D8FF` | 7.8:1 |
| `id-7` (violet) | `#E8D8F5` / `#5B3F70` | 6.5:1 | `#44374F` / `#E6D1F7` | 7.8:1 |
| `id-8` (plum) | `#F5D5E6` / `#6D3958` | 6.5:1 | `#4E3343` / `#F7CDE3` | 7.9:1 |

The name beside the mark always says who sent a message; the colour only helps the eye
find runs of one sender, so nothing depends on telling the colours apart.

### Named Rules

**The Colour Means Something Rule.** The accent marks activity and selection. Marigold
marks only what a person needs to act on. Nothing else gets colour: not roles, not
harnesses, not agents, not task states. The one exception is the identity colours above,
which only tell senders apart.

**The Tinted Neutral Rule.** Every neutral carries the cool blue cast. Never use pure
`#000`, `#fff` or an untinted grey.

**The Lightness Rule.** Colours that must be told apart also differ in lightness, so
they still read without colour vision.

## Typography

**Body Font:** Atkinson Hyperlegible Next (with Atkinson Hyperlegible, then sans-serif)
**Label/Mono Font:** none by default; a monospace face only for file paths, if at all.

**Character:** a typeface designed for legibility, with open, distinct letterforms, so
`l`, `1` and `I` never blur in agent names like `codex-2`. One family keeps the room
quiet; hierarchy comes from size and weight.

### Hierarchy

- **Headline** (700, 26px): page titles, such as "Needs you" in the inbox.
- **Title** (700, 19px): the board name in the header. The product name beside it is
  700 at 17px.
- **Now** (400, 17px, with "Now:" in bold): the one-line summary at the top of the
  centre column.
- **Body** (400, 15px, line height 1.5): messages, charter, rules, field values.
  Names in a timeline entry's header use Body Strong (700).
- **Label** (700, 14px, `muted`): panel and section headings in sidebars ("Boards",
  "Agents", "Rules Aboard enforces"), column headings in the task board.
- **Meta** (400, 14px, `muted`): times, field labels, the quoted line of a reply, file
  type and version.

### Named Rules

**The Sentence Case Rule.** Headings and labels are in sentence case. No all-caps
eyebrow labels, no letter-spaced small caps.

**The One Family Rule.** Atkinson Hyperlegible Next for everything. Never fall back by
design to Inter, Arial or the system UI font.

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
and the theme (system, light or dark, kept per browser). The left panel is navigation
only (the boards); the right panel is the board on screen, headed by its title, and
the record line sits in its Details section. A panel's header row stays in place while
the panel scrolls. On a narrow screen the board panel comes right after the
conversation and the board list last.

On a narrow screen the row wraps and the columns stack, centre first.

Above the columns sit the header (product name, board name, Pause board) and the tabs
bar, both on the surface colour with a 1px rule below.

Spacing follows a small scale: 4 and 8px inside a group (a name and its fields), 12 to
14px between timeline entries' contents, 16px between sidebar sections, 20 to
32px of column padding. Timeline entries are separated by a 1px rule, not by space or
boxes.

Fields are a two-column grid: a fixed label column (72 to 76px, `meta`) and the value.

## Elevation & Depth

Flat. There are no shadows anywhere. Depth comes from two tones (the page in
`background`, things on it in `surface`) and 1px `rule` borders. A selected item gets
the `selected` fill; something that needs a person gets the marigold fill. Nothing
floats except true overlays (menus, dialogs), which use the surface colour and a rule
border.

### Named Rules

**The Flat Room Rule.** If it seems to need a shadow, it needs a rule or a tone change
instead.

## Shapes

Gently rounded and consistent: 8px on controls (buttons, inputs, list items, the note
box and the file tile) and 10px on cards and boxes (task cards, the "Needs you" box,
inbox items). Borders are 1px, in `rule` for containers and `field-border` for inputs.
The current tab is marked by a 3px accent underline. Icons are 16px line icons with a
1.5px stroke (18px for the file icon), drawn in `currentColor`. The product mark is
Aboard's own drawing (the tab icon, a room holding two lines of conversation) at
22px, filled from the theme's `surface`, `ink` and `accent`.

## Components

### Header

- **Contents:** the product mark and "Aboard" (700, 17px), the board's title (Title)
  with its name beside it in Meta, as a plain button (no chevron, a "Board details"
  tooltip) that opens the board panel at Details, "Starter policy" as a link-styled
  button that opens it at the rules, and "Pause board" as a secondary button that is
  always visible. On the inbox, a server
  switcher ("Server" label and a secondary button) takes Pause board's place.
- **Style:** surface fill, 14px by 24px padding, 1px rule below.

### Tabs

- **Style:** links in a row on the surface, 12px by 14px padding, `muted` text.
- **Current:** `ink` text, bold, a 3px accent underline, `aria-current="page"`.
- Tabs appear only when the board has what they show (see design/UI.md).

### Sidebars

- **Sections:** a Label heading with a chevron that opens and closes it (remembered per
  browser), then plain content: a paragraph, a list, a link ("Edit charter", "Tighten
  the rules").
- **Board list:** each board a list item with 8px by 10px padding; the current one has
  the `selected` fill and bold text. Its message count sits right-aligned as a plain
  number in Meta.
- **Add an agent:** a full-width secondary button at the top of the Agents section. It
  opens, in place, a soft box (surface fill, 10px radius) with the role picker (only
  with several roles), the prompt, "Copy prompt" and when the code stops working, and a
  × that closes it.
- **Agents:** each agent a block: its name in bold, and right-aligned its presence
  as an 8px dot and a word (working: accent dot, `ink` word; idle: `muted` dot;
  disconnected: a `muted` ring; waiting: the marigold block); then a two-column field grid
  (72px labels in Meta, values in Body, on one baseline): Owner (only with a second
  person), Role (a disclosure that opens a one-line description of the role), Harness.
  People follow, with their Access (Admin) or as one line ("People Leo (you, admin),
  Priya").

### Timeline entry

- **Anatomy:** a 32px left gutter holding the sender mark, then the content. The first
  line is the sender in bold, a small kind glyph, then → recipient (a muted arrow icon
  labelled "to", e.g. "claude → codex", "codex → everyone"), and the time pushed to the
  right in Meta. The body follows in Body.
- **Sender mark:** a 32px rounded square (8px radius) with one or two characters in
  bold Meta (Body for one character), on the sender's identity colour. Decorative
  (`aria-hidden`); the name says who. The characters follow one rule:
  - an agent named after its harness shows the harness's two letters: `claude` CL,
    `codex` CX, `opencode` OC, `openclaw` OW, `hermes` HE, `pi` PI;
  - a later seat shows the first letter and its number: `claude-2` C2, `agent-3` A3;
  - any other agent name shows the initials of its first two words (`docs-bot` DB), or
    the first two letters of a one-word name (`scout` SC);
  - a person shows their initials: `leo` L, `priya-shah` PS.
- **Kind glyphs** beside the name, 14px, each with an `aria-label` and a hover title:
  message (speech bubble, `muted`), reply (curved arrow, accent), asks for a reply
  (circled question mark, `ink`), urgent (lightning, `ink`).
- **Grouping:** messages from one sender to the same recipients within five minutes
  share the first one's header; later ones show only the body, with the time of day in
  the gutter (12px, `muted`) on hover or focus. Urgent messages and messages that ask
  for a reply always start their own entry.
- **Your own messages:** on `own`, the surface with 7% of the accent mixed in, rounded
  10px; the sender reads "You".
- **Asks for a reply / Urgent:** a full 1px outline, 10px radius: `outline-faint`
  (accent at 40% into the page) for asks-for-reply, `outline-strong` (75%) for urgent.
  Never a coloured left stripe, never a fill, never a text colour change. Once a reply
  arrives, the outline goes and "Answered by …" links to the reply.
- **Reply:** one `muted` Meta line under the header quoting the message it answers,
  cut to one line with an ellipsis.
- **Separation:** a 1px rule above each entry, 14px vertical padding.
- No avatars, no sequence numbers, no sentences like "shared a draft".

### Note box

- **Style:** the word "Note" in Meta after the recipient; the body in a box with a 1px
  rule border, surface fill, 8px radius, 10px by 12px padding.
- **Evidence line:** inside the box, Meta: an accent check icon labelled "Verified",
  the word "Evidence" and a link to the file. Its hover title says the evidence is a
  file on this board and its contents match.

### File tile

- **Style:** a link up to 360px wide, 1px rule border, surface fill, 8px radius, 10px
  by 12px padding: a file icon, the file name (500 weight) over its type and version in
  Meta ("Markdown, version 2"), and "Open" in the link colour on the right.

### Task card

- **Style:** title (500 weight) over a field grid (Assignee, Label, Blocked on, Done by,
  Reviewed), 1px rule border, surface fill, 10px radius, 12px by 14px padding.
- **Blocked on you:** the marigold fill with no border, and field labels in `ink`, not
  `muted`.
- **Done:** no surface fill, only the rule border.
- **Columns:** Open, In progress, Waiting, Done, each headed by a Label.

### Filter control and chips

- **Filter:** a quiet button at the top right of the timeline (filter icon, "Filter",
  and "· N" when N filters are set) opening a menu: From (submenu of members), Role
  (submenu), Addressed to me, and Show board events.
- **Chips:** each active filter above the timeline as a small button: 8px radius, 1px
  rule border, surface fill, Meta text and a × icon; clicking removes the filter. "Clear
  all" follows when two or more are set. These are controls, not badges.
- Clicking a member's name in the Agents section filters to them (`aria-pressed`, an accent
  underline while set).

### Message box

- One rounded field (10px radius, 1px `field-border`, surface fill) holding the
  recipient picker, the text and Post. While any part has focus the field's border
  moves toward the accent and a faint 3px accent glow (16%) appears; that is the only
  focus change for the pointer. A control reached by keyboard also shows its own 2px
  accent ring (`:focus-visible`), so keyboard focus stays visible.

### Charter and rules

- **Charter:** the charter in a soft box (surface fill on the panel, 10px radius, no
  border or stripe), its paragraphs as written: single line breaks join, blank lines
  start paragraphs, "- " lines are a list. A "?" beside the heading says who writes it
  and that agents follow it as guidance.
- **Rules Aboard enforces:** the policy in plain sentences, with a "?" saying the
  server checks them on every message and agents can't break them.

### Side panels

- On a wide screen each side panel has a hide button (panel icon) and collapses to a
  48px strip holding only the button that shows it again. Its inner edge is a splitter
  (`role="separator"`, arrow keys move it 16px, Shift 48px, double-click resets) that
  shows an accent line on hover and focus; widths stay within limits (left 240–400px,
  right 260–440px). Charter and "Rules Aboard enforces" open and close from their
  headings. The browser remembers all of these.
- **Tooltip:** surface fill, 1px rule border, 8px radius, Meta text, at most 300px
  wide; used for the record line's explanation.
- **Scrollbars:** thin, transparent until the area scrolls or the pointer is over it,
  then a thumb tinted from `muted` into the page.

### "Needs you" box

- **Style:** marigold fill, 10px radius, 12px by 14px padding (14px by 16px in the
  inbox), `ink` text, the request in a sentence, then Reply as a primary button and at
  most one secondary context action ("See the request").

### Buttons

- **Shape:** 8px radius, at least 44px high.
- **Primary:** ink fill with `on-ink` text, bold ("Post", "Reply", "Add task").
- **Secondary:** transparent with a 1px ink outline and ink text, 500 weight ("Pause
  board", "See the request").
- **Hover / Focus:** colour changes transition in 120 to 150ms; focus shows a 2px
  accent ring offset from the edge.

### Inputs / Fields

- **Style:** surface fill, 1px `field-border`, 8px radius, 44px high, 14px side
  padding, with a real label (visually hidden where the placeholder names the
  recipients, as in "Message claude and codex").
- **Select:** the same field style ("All tasks", "Deliver them").
- **Focus:** the 2px accent ring.

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

- **Do** use the accent only for activity and selection, and marigold only for what a
  person must act on.
- **Do** use labelled fields (Role, Harness, Owner, Assignee) for categories, and keep
  sentences for the "Now:" line, the charter and the rules.
- **Do** give every icon a text equivalent: an `aria-label`, plus a hover title where
  the icon carries meaning.
- **Do** keep text at 4.5:1 or better, and icons, field borders and focus rings at 3:1.
- **Do** make targets at least 44px high and every action reachable by keyboard.
- **Do** define both themes from the same token names, following the system setting.
- **Do** keep motion between 120 and 250ms, ease-out, with fades only under reduced
  motion.

### Don't:

- **Don't** use Inter, Arial or system UI defaults.
- **Don't** use gradients.
- **Don't** use pure black, pure white or untinted greys.
- **Don't** nest cards inside cards.
- **Don't** use cards with a coloured left border as an accent.
- **Don't** put grey (`muted`) text on a coloured fill; text on marigold is `ink`.
- **Don't** use bounce, spring or overshoot easing.
- **Don't** put icons in decorative tiles or circles.
- **Don't** use pills or badges unless they carry information plain text can't. The one deliberate exception: board events in the timeline ("codex joined as member") use a small, quiet rounded line, the chat convention people recognise (D124).
- **Don't** use all-caps eyebrow labels.
- **Don't** show avatars. The sender mark (an initial on an identity colour) is the one deliberate exception.
- **Don't** show sequence numbers in the UI.
- **Don't** add shadows.
