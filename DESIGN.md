---
name: Querycraft
description: A learn-SQL-by-writing-it app — ink-on-paper workspace with one pine accent.
colors:
  page-canvas: "#f4f5f7"
  surface-raised: "#ffffff"
  surface-sunken: "#f7f8fa"
  surface-fill: "#eef0f3"
  hairline: "#e3e6eb"
  hairline-strong: "#cbd1da"
  ink: "#14161a"
  ink-secondary: "#494f5a"
  ink-tertiary: "#636a75"
  pine: "#0b6b59"
  pine-hover: "#0a5c4d"
  pine-ink: "#0c6f5c"
  pine-wash: "rgba(12, 111, 92, 0.10)"
  pine-hairline: "rgba(12, 111, 92, 0.26)"
  pass-green: "#1a7f4f"
  pass-wash: "rgba(26, 127, 79, 0.11)"
  caution-amber: "#8a5a10"
  caution-wash: "rgba(138, 90, 16, 0.12)"
  fail-red: "#b3352f"
  fail-wash: "rgba(179, 53, 47, 0.10)"
  code-canvas: "#fbfcfd"
  code-ink: "#20303f"
  code-hairline: "#e6e9ee"
typography:
  display:
    fontFamily: "Space Grotesk, Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "30px"
    fontWeight: 600
    lineHeight: 1.15
    letterSpacing: "-0.02em"
  headline:
    fontFamily: "Space Grotesk, Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "17px"
    fontWeight: 600
    lineHeight: 1.35
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 600
    lineHeight: 1.6
    letterSpacing: "0"
  body:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14px"
    fontWeight: 400
    lineHeight: 1.6
    letterSpacing: "0"
  prose:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "14.5px"
    fontWeight: 400
    lineHeight: 1.75
    letterSpacing: "0"
  label:
    fontFamily: "Geist Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "11px"
    fontWeight: 500
    lineHeight: 1.6
    letterSpacing: "0.1em"
rounded:
  xs: "6px"
  sm: "8px"
  md: "10px"
  lg: "12px"
  xl: "14px"
  pill: "999px"
spacing:
  "2": "2px"
  "4": "4px"
  "6": "6px"
  "8": "8px"
  "10": "10px"
  "12": "12px"
  "14": "14px"
  "16": "16px"
  "18": "18px"
  "20": "20px"
  "22": "22px"
  "24": "24px"
  "28": "28px"
  "32": "32px"
  "40": "40px"
  "56": "56px"
  "72": "72px"
components:
  button-primary:
    backgroundColor: "{colors.pine}"
    textColor: "#ffffff"
    rounded: "{rounded.sm}"
    padding: "8px 13px"
    typography: "body"
  button-primary-hover:
    backgroundColor: "{colors.pine-hover}"
  button-secondary:
    backgroundColor: "{colors.surface-fill}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    padding: "8px 13px"
    typography: "body"
  button-secondary-hover:
    backgroundColor: "{colors.hairline}"
  button-ghost:
    backgroundColor: "transparent"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.sm}"
    padding: "8px 13px"
    typography: "body"
  input:
    backgroundColor: "{colors.surface-fill}"
    textColor: "{colors.ink}"
    rounded: "{rounded.sm}"
    padding: "9px 11px"
    typography: "body"
  nav-item:
    backgroundColor: "transparent"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.sm}"
    padding: "8px 10px"
  nav-item-active:
    backgroundColor: "{colors.surface-fill}"
    textColor: "{colors.ink}"
  pill:
    backgroundColor: "{colors.surface-fill}"
    textColor: "{colors.ink-secondary}"
    rounded: "{rounded.pill}"
    padding: "4px 10px"
  card:
    backgroundColor: "{colors.surface-raised}"
    textColor: "{colors.ink}"
    rounded: "{rounded.lg}"
    padding: "20px 22px"
  code-cell:
    backgroundColor: "{colors.code-canvas}"
    textColor: "{colors.code-ink}"
    rounded: "{rounded.lg}"
---

# Design System: Querycraft

## Overview

**Creative North Star: "The Working Terminal"**

Querycraft is a workspace that happens to teach. The learner is not watching a
lesson; they are at a bench with a real database on it, and the interface is built
so that the thing they are making — the query and what it returned — is always the
loudest object on screen. Everything else is furniture: paper-white surfaces, a
single hairline where a border would do, and a rail of navigation that stays quiet
until it is the current place.

The palette is directional rather than decorative. Ink neutrals carry every surface
and every line; exactly one saturated hue, a deep pine green, is spent on state and
action — the active nav item, the primary button, the pass colour's quieter sibling,
the focus ring. Nothing else is allowed to be a colour. The feedback panels are the
only place the system raises its voice, and they do it with a wash of green, amber
or red rather than a border or a badge, so a verdict reads as a change in the room
rather than a sticker applied to it.

Type does the structural work that colour is not allowed to. Space Grotesk is
reserved for display: page titles, the numbers in a stat row, the ring's percentage,
the named brand. Geist carries all prose and all chrome. Geist Mono is not a costume
for "technical" — it is spent only on data and metadata, which is the honest
distinction: column names, row values, ids, durations, statuses, and the small
uppercase labels that annotate a section. The result of that split is that a learner
can tell what is *material* (data, from the engine) and what is *furniture* (the app
talking about itself) before reading a word.

Density is app-density, not marketing-density. The shell is a fixed 244px rail plus a
scrolling workspace, the content column caps at 1180px, and reading prose caps at
66ch on purpose. Chapters are long, so the reading column is a genuinely comfortable
measure and everything around a SQL cell is deliberately tight, with the cell itself
supplying the visual break.

**Key Characteristics:**

- Paper-white surfaces on a slightly cool canvas; hairline separators instead of
  borders and shadows.
- One accent hue (pine), spent only on action, current state, focus, and success.
- Three type roles with distinct jobs: Space Grotesk display, Geist prose, Geist Mono
  for data and metadata only.
- Verdict surfaces are washes, not bordered callouts; execution errors are visually
  distinct from wrong answers.
- Flat by default. Depth appears only as an overlay response (drawer, modal, toast).
- Two palettes, one token set: "Ink & Pine" (light) and "Pine on Slate" (dark).

## Colors

A near-neutral ink system with exactly one accent hue, plus three semantic verdict
colours that are wash-only.

### Primary

- **Pine** (`#0b6b59`): the accent as a solid fill — the primary button, the active
  switch track, the completed pip in a lesson list. In the light palette the darker
  step is used for fills so white text clears AA on it.
- **Deep Pine Ink** (`#0c6f5c`): the accent used *as text or iconography* — the current
  nav icon, an eyebrow, a link, a `.tag`, the filled arc of a progress ring. Kept a
  step darker than the fill so accent-on-accent-wash text still clears AA.
- **Pine Wash** (`rgba(12, 111, 92, 0.10)`): the accent as a background — selected
  chips, the current course switch, an "accepted" panel, the editor's focus tint.
- **Pine Hairline** (`rgba(12, 111, 92, 0.26)`): the accent as a 1px edge, used only on
  the exercise cell to mark it as gradable.

### Neutral

- **Page Canvas** (`#f4f5f7`): the app background behind every surface. Slightly cool
  so that paper-white cards read as raised without needing a shadow.
- **Raised Surface** (`#ffffff`): cards, the sidebar, the header, inputs' surrounding
  chrome. This is the "paper".
- **Sunken Surface** (`#f7f8fa`): a quieter fill for hints, table headers, staged
  panels — a surface that is present but should not compete.
- **Fill Surface** (`#eef0f3`): the default button, pills, switch tracks, code gutters,
  and any control that must read as a control before it is hovered.
- **Hairline** (`#e3e6eb`) / **Hairline Strong** (`#cbd1da`): separators between rows
  and sections; the strong step for kbd caps, switch outlines and scrollbar thumbs.
- **Ink** (`#14161a`): body copy, headings, values in a result grid's hover state.
- **Ink Secondary** (`#494f5a`): supporting prose, nav labels, table cell values.
- **Ink Tertiary** (`#636a75`): metadata — row counts, durations, ids, section kickers,
  uppercase labels, empty-state text. *Must* clear 4.5:1 against both the page canvas
  and the fill surface; it is the token that carries the most small text in the product.

### Named Rules

**The One Voice Rule.** Pine is spent on ≤10% of any screen. It marks action, current
state, focus and success — never decoration, never a heading, never a chart series for
its own sake. Its rarity is what makes the active nav item legible at a glance.

**The Verdict-Is-A-Wash Rule.** Pass, caution and failure are communicated by a tinted
background (green/amber/red wash) plus a coloured icon and heading — never by a border,
a ribbon, or a coloured left edge. Colour changes the room; it does not get applied to
the wall.

**The Error-Is-Not-A-Failure Rule.** An execution error (`--red`) and a wrong answer
(`--amber`) are different colours *and* different words. A learner must never be able
to mistake "your SQL is broken" for "your SQL is wrong".

## Typography

**Display Font:** Space Grotesk (with Geist, then the system sans)
**Body Font:** Geist (with the system sans)
**Label/Mono Font:** Geist Mono (with ui-monospace, SFMono-Regular, Menlo)

**Character:** Space Grotesk supplies a slightly mechanical, engineered display voice —
enough personality to signal "tools", restrained enough that a 40px page title never
looks like a poster. Geist is the neutral, high-legibility workhorse underneath it.
The pairing works because the two share an almost monolinear skeleton; the mono then
marks, by contrast, the one category of text the product refuses to style as prose.

### Hierarchy

- **Display** (Space Grotesk 600; 30px / 1.15 / −0.02em): page titles. Scales down to
  26px in a chapter, 24px in the workbook hero, and up to 40px on Home
  (`−0.035em` at that size). Never used for a subsection.
- **Headline** (Space Grotesk 600; 17px / 1.35 / −0.02em): card titles and section
  headings; 15px in a course hero.
- **Title** (Geist 600; 14px, letter-spacing 0): `h3` inside a panel — switches to the
  body face deliberately so a sub-heading reads as an annotation, not a second title.
- **Prose** (Geist 400; 14.5px / 1.75, max 66ch): chapter body copy and seeded
  markdown. The measure is capped so a long chapter stays readable on a wide monitor.
- **Body** (Geist 400; 14px / 1.6): all app chrome — buttons, nav, table cells at
  12.5px, prose at 13px in a card.
- **Label** (Geist Mono 500; 11px, `0.1em`, uppercase): eyebrows, section kickers,
  `SECTION n`, `COURSE · 9 CHAPTERS`, column headers, `.label` group names. Ten-pixel
  mono is the floor and is reserved for badges inside a pill.

### Named Rules

**The Mono-Is-Material Rule.** Geist Mono is spent on data and metadata only: table
headers and cells, ids, durations, row counts, statuses, dialect names, and the small
uppercase labels that annotate structure. It is never used for prose, never for a
heading, and never to make a screen "feel technical".

**The Uppercase-Is-Mono Rule.** If text is uppercased with letter-spacing, it is mono
and it is a label. Body faces are never letter-spaced into caps.

## Layout

A fixed shell around a single scrolling workspace. The sidebar is 244px wide and holds
the brand, three to four labelled nav groups, and a pinned account row; the shell's
`header` is a fixed 52px containing the breadcrumb, the one global search field, and
engine/theme state. Only `.workspace` scrolls — `.app` is exactly one viewport tall
with `overflow: hidden`, which is load-bearing: it contains the screens' absolutely
positioned visually-hidden nodes so the document cannot be scrolled past one viewport.

Content sits in a `.content` column capped at 1180px and centred, with `28px 32px 72px`
of padding so the last row of a long screen clears the fold. A chapter is a two-column
grid — a 272px in-page chapter rail and the reading column — where the reading column
holds a 720px article and prose blocks cap at 66ch independently of that. There is no
global max-width on the reading text; the cap is per-paragraph so lists, code and
tables can still use the full column.

Rhythm is a 2px-step scale dominated by 4/6/8/10/12/14/16/18/20/22/24, with 28/32 for
major block separation and 40/56/72 reserved for page-level space. Related content
groups tightly (2–6px between a row's own lines), distinct groups separate generously
(16–24px between cards), and a heading always gets more space above it than below.

**Breakpoints.** Three, and they are about what breaks, not device classes:

- **≤1080px** — `.grid2` and the lesson grid collapse to one column, the chapter rail
  is hidden, `.grid3` goes to two, `.stats` to two.
- **≤760px** — the shell rail collapses to 64px of icons (labels hidden but kept for
  screen readers and pointers), all multi-column grids go single, page padding drops to
  16px, and `.hide-small` content is dropped.

Every grid column uses `minmax(0, 1fr)` (`1fr` on its own will lock a grid to the widest
column's min-content and overflow the page) and every nowrap text node in a grid or flex
row carries a `min-width: 0` ancestor or an explicit truncation.

## Elevation & Depth

**The system is flat by default.** Depth is tonal: the page canvas is one step darker
than the paper surfaces that sit on it, and a hairline separates adjacent regions. Cards
have no border and no shadow — the canvas/paper contrast plus the radius is the whole
elevation story. A shadow is never used to indicate that something is a card.

Shadow exists for exactly one purpose: something is floating *over* the page, and the
shadow is what separates the overlay from the content behind it. Those are the tables
drawer, the modal, the search results dropdown, and the toast. Nothing else gets one.

### Shadow Vocabulary

- **Ambient (raised, in-flow)** (`0 1px 2px rgba(20,22,26,.05), 0 10px 30px -18px rgba(20,22,26,.28)`):
  the lightest step. Only for a surface that is genuinely floating in the page flow — a
  selected auth tab, the skip link while focused.
- **Overlay** (`0 24px 60px -30px rgba(20,22,26,.42)`): the tables drawer, the modal, the
  toast. Always paired with a dimmed backdrop (`rgba(20,22,26,.42–.5)`).

Both steps carry a real offset and a wide soft blur; a zero-offset coloured halo is not
in the vocabulary, and the one place a ring is drawn (focus) is a ring and says so.

### Named Rules

**The Flat-By-Default Rule.** Surfaces are flat at rest. A shadow means "floating over
the page", and if an element is not floating it does not get one — the canvas/paper
contrast already did the work.

**The Hairline-Not-Border Rule.** Separation inside a surface is a 1px hairline in the
line token, on the surface's own edge. A card does not carry a border; only genuinely
enclosed, scrollable content (a result grid, a schema table, the SQL editor surface)
gets a 1px enclosure.

## Shapes

Gently curved, consistent, and never pill-shaped except for things that are genuinely
small and single-line.

Radii run `6 → 8 → 10 → 12 → 14px` on a named scale: 6px for the smallest controls and
inline code, 8px for buttons, inputs, nav rows and small marks, 10px for panels and
tables, 12px for cards and the SQL cell, 14px for the auth card. The pill radius (999px)
is reserved for chips, badges, tags, progress tracks and the switch — all small,
single-line, and all things whose identity is "a capsule".

Enclosures are 1px hairlines around scrollable content, never around decorative
groupings. The SQL cell is the one component with a full enclosure, and it is treated
as a piece of equipment on the bench: a header strip (`celltop`), a code surface with a
muted gutter, a control strip, and a result area, each separated by a hairline rather
than nested boxes.

## Components

### Buttons

- **Shape:** gently curved (8px radius), inline-flex with a 7px gap between icon and
  label, one-line text.
- **Primary:** solid deep pine with white text, 600 weight; the dark palette inverts the
  text to a near-black pine so contrast holds on the brighter accent. Padding `8px 13px`.
- **Hover / Focus:** background steps one token darker/lighter on hover over `180ms`
  with `cubic-bezier(.2,.7,.3,1)`. Focus is a *ring* — `0 0 0 2px <surface>, 0 0 0 4px
  var(--accent)` — never an outline, and it must survive on a pill, which is why the
  ring's radius is inherited rather than forced.
- **Secondary:** the fill surface as a background; this is the default `button`, and it
  reads as a control before it is hovered.
- **Ghost:** transparent until hovered; used for the third action in a row and for icon
  buttons. `button.small` steps to 12px text and `6px 11px` padding for in-cell rows.
- **Disabled:** 40% opacity with a not-allowed cursor. Disabled is the only state that
  dims the whole control; a refused action keeps its colour and explains itself in text
  beside it instead.

### Chips

- **Style:** the pill radius, fill surface background, secondary ink, 12px 500 weight,
  no border. A leading 6px `.dot` carries status and is the only colour in the chip.
- **State:** `.active` swaps to pine wash with pine-ink text. `.subtle` steps the
  background down to the sunken surface for metadata that should not read as a filter.
- **Badges:** the same capsule at 10–11px mono with `0.04–0.06em` tracking, used for
  dialect, level and enrolment. A badge inside a table row (`.tag`) drops to a 4px
  radius because it is inline with mono text.

### Cards / Containers

- **Corner Style:** 12px radius.
- **Background:** raised surface on the page canvas — no border, no shadow.
- **Shadow Strategy:** none. See Elevation & Depth. This is the flat-by-default rule
  applied.
- **Border:** none by default. Enclosed scrollable content (result grid, schema table)
  gets a 1px hairline enclosure at a 10px radius.
- **Internal Padding:** `20px 22px` for a full card, `18px 20px` for a course card,
  `16px 18px` for a drawer panel.

### Inputs / Fields

- **Style:** fill surface background, 8px radius, transparent 1px border so the box does
  not move when it focuses. Placeholder uses the tertiary ink token and must clear
  contrast — a placeholder is still text.
- **Focus:** border steps to pine and a 3px pine wash ring appears. The editor inside a
  SQL cell is a borderless code surface and takes an *inset* ring on its wrapper instead,
  because the cell clips with `overflow: hidden`; without that, the one control a learner
  cannot avoid would be the one with no visible focus.
- **Error:** field-level errors are inline red text below the field; form-level errors are
  a red-wash panel with an icon and a heading.

### Navigation

- **Style:** sidebar rail, 13px 500, secondary ink on transparent, 8px radius. Groups are
  introduced by a mono uppercase 11px label. The current item takes the fill surface and
  full ink, and its icon switches to pine — that icon is the only accent in the rail.
- **Mobile (≤760px):** the rail collapses to 64px of centred icons; the label is hidden
  visually but preserved for pointers (`title`) and screen readers (`aria-label`).
- **In-page:** chapter and section navigation uses the same row vocabulary at a smaller
  scale — a numbered 20–24px mark, a truncating label, and metadata on the right.

### SQL Cell (signature component)

The product's defining object: an exercise or example rendered as a piece of equipment.
A mono header strip names the cell (`Exercise 4` / `Runnable example`) and its contract;
the body is a code surface with a non-selectable line-number gutter and a self-growing
textarea; a control strip carries Run / Check answer / Hint / Show solution / Keep query
/ Reset with the keyboard hint pinned right; the result grid and the feedback panel
render below, each with `:empty { display: none }` so an unused cell is compact. An
exercise cell is marked with a pine hairline enclosure; an example is not.

### Verdict Panels

A wash background (green passed, amber failed, red error, plain surface for a hint),
an icon-and-heading row, an optional checklist of contract checks, and a row of
next actions. A neutral panel carries no verdict colour at all, so "hint" cannot be
mistaken for "passed".

## Do's and Don'ts

### Do:

- **Do** keep the accent at ≤10% of a screen — action, current state, focus, success.
- **Do** express depth as canvas-vs-paper contrast plus a hairline; reserve `box-shadow`
  for surfaces that float over the page (drawer, modal, dropdown, toast).
- **Do** use `minmax(0, 1fr)` for every grid column and give every nowrap node in a grid
  or flex row a `min-width: 0` ancestor or an explicit ellipsis; `1fr` alone locks a grid
  to its widest column's min-content and pushes the page wide.
- **Do** spend Geist Mono only on data and metadata, and keep uppercase text mono with
  `0.09–0.12em` tracking.
- **Do** cap prose at 66ch and let the surrounding column stay wider.
- **Do** give every control a visible focus ring, including the code editor, the scrollable
  result grid and the search hits.
- **Do** verify small metadata text for contrast against the surface it actually sits on,
  including tinted washes, which lower contrast on both palettes.
- **Do** distinguish an execution error from a wrong answer in colour **and** in words.

### Don't:

- **Don't** add a border to a card, or a shadow to a surface that is not floating.
- **Don't** introduce a second accent hue; the semantic verdict colours are for verdicts
  only and are never used decoratively.
- **Don't** use mono to make a screen feel technical, or uppercase a body face.
- **Don't** use a coloured `border-left` on a card, list item or callout above 1px.
- **Don't** flatten the primary task and the surrounding chrome to equal weight; the SQL
  cell, the primary action and the current nav item must dominate their neighbours.
- **Don't** rename product terminology (chapter, exercise, result contract, workbook,
  challenge lab, workspace, dialect, plan) or rewrite the product's copy in a design pass.
- **Don't** let a dark palette be derived by inverting the light one — the dark palette is
  a second palette with its own accent value (`#34d3a6`) so contrast holds on a dark
  surface.
