# Accessibility: what is verified, and how

The M2 exit gate says *keyboard-only use of a full lesson works*. This document is
the evidence for that claim: what was driven, with which tool, and what the app
answered back. It is also explicit about what has **not** been verified, because a
partial pass reported as a full one is worse than no pass at all.

**Result: the walkthrough passes. One real defect was found and fixed along the
way (the SQL editor had no focus ring); one trap defect was found and fixed
(the Tables drawer could not see its own tables). A later batch turned the
Progress and Workbook chapter rows into real buttons and gave the prompt dialog a
focus trap (sections 8 and 9).**

## How it was driven

There is **no automated accessibility runner in this repo**, no axe-core, no
pa11y, no `lighthouse-ci`, and adding one was out of scope for M2. The evidence
below is a scripted keyboard walkthrough with targeted DOM assertions, run over
the Chrome DevTools Protocol against the live PGlite adapter:

```sh
cd web && npm run dev            # http://localhost:5173, in-browser PostgreSQL
# drive it with agent-browser (CDP): press Tab, read document.activeElement
```

Each check reads state the browser itself computes, `document.activeElement`,
`Element.matches(':focus-visible')`, `getComputedStyle(el).boxShadow`, and the
accessibility attributes in the DOM, rather than a screenshot judgement.

## 1. Tab order, from a cold page

Reload, blur, then press Tab and record `document.activeElement` at every stop.

| # | Stop | # | Stop |
| --- | --- | --- | --- |
| 1 | `a.skiplink` “Skip to the lesson content” | 13 | “Create a course” (page body) |
| 2 | nav “Home” | 14 | “Continue chapter 1” |
| 3 | nav “SQL Foundations” | 15 | “Course home” |
| 4 | nav “Library” | 16 | “Challenge lab” |
| 5 | nav “Workbook” | 17 | “Browse library” |
| 6 | nav “Progress” | … | the rest of the screen, in reading order |
| 7 | nav “Challenge lab” | | |
| 8 | nav “Create a course” | | |
| 9 | nav “Appearance” | | |
| 10 | `input[type=search]` “Search the course and your workbook” | | |
| 11 | “Toggle theme” | | |
| 12 | “Create a course” | | |

**24 focusable stops on the home screen, 54 on a lesson, 0 gaps.** Every stop is
in document order. The stop after the last element is `<body>`; that is the
browser leaving the page, and the next Tab wraps back to the skip link, not a
hole in the page.

## 2. Skip link

Tab once from the top, then Enter:

```
focused: "Skip to the lesson content"
hash:    "#main"
active:  "main / MAIN"          # <main id="main" tabindex="-1">
```

It is positioned on-screen only while focused (`.skiplink:focus-visible{left:12px}`).

## 3. A full lesson, keyboard only

Reached without a pointer: Tab to the nav item, Enter; Tab to a chapter's
**Start**/**Review**, Enter.

| Step | Keys | What the app reported |
| --- | --- | --- |
| Reach the exercise editor | 51 × Tab (lesson `f4`) | `TEXTAREA` named **“SQL editor for exercise f4.1”** |
| Run a wrong query | type, `Ctrl+Enter` | live region: “Query returned 30 rows.” |
| Grade it | Tab to **Check answer**, Enter | live region: “Not yet: row count.” |
| Run the reference solution | type, `Ctrl+Enter` | live region: “Query returned 4 rows.” |
| Grade it | Tab to **Check answer**, Enter | live region: **“Your answer passed.”**; workbook 0% → 14% |

`Cmd+Enter` is accepted as well as `Ctrl+Enter`.

## 4. Result grid

After a run, the grid is `role="region"` with `tabindex="0"`, a hidden
`<caption>`, column `<th scope="col">`, and an `aria-label` that names the shape:

```
role=region tabindex=0
aria-label="Query result with 1 column (category) and 4 rows, scrollable"
caption   ="Query result with 1 column (category) and 4 rows"
```

It is reachable by Tab (four stops after the editor: Run, Keep query, Reset,
region) and takes the focus ring:

```
matches(':focus-visible')  true
box-shadow                 rgb(244,245,247) 0 0 0 2px, rgb(13,125,104) 0 0 0 4px
```

## 5. Tables drawer: trap, cycle, restore

Enter on **Tables** opens a real dialog:

```
role=dialog  aria-modal=true  aria-label="Database tables"  tabindex=-1
inert        removed while open, restored when closed
```

| Check | Observed |
| --- | --- |
| Focus moves in on open | yes: lands on the **Close** button |
| Tab stays inside | `Close → customers → order_items → orders → products → Close`, never leaving |
| Shift+Tab at the first stop | wraps to the last table region |
| Escape | closes, `inert` returns |
| Focus restored | yes: back on the **Tables** button |

This is where the run found its second defect. The drawer's tables were plain
scrollable `<div>`s with no `role`, no accessible name and no `tabindex`; Chrome
still makes a scroll area focusable, so they *appeared* in the tab order while the
trap's selector could not see them, Tab therefore escaped to the document body
once per cycle. `Schema.tsx` now gives each table the same treatment as
`ResultGrid` (named region + hidden caption + `scope="col"`), and
`TablesDrawer.tsx` uses a selector that cannot match SVG `<use href>` and a
`getClientRects()`-based visibility test (`offsetParent` is `null` for anything
inside a `position: fixed` panel).

## 6. Visible focus on every interactive element

For every stop in the tab order, `matches(':focus-visible')` is true **and** a ring
renders, the global `:focus-visible{box-shadow:var(--ring)}`, or the inset accent
ring on the editor wrapper.

The one gap this found: the SQL editor suppressed its own outline (`.editor{outline:none;box-shadow:none}`) and nothing drew one in its place, so the single
control a learner cannot avoid using was the one control with no visible focus.
Fixed with an inset ring that survives the cell's `overflow:hidden`:

```css
.editorwrap:focus-within{box-shadow:inset 0 0 0 2px var(--accent)}
```

## 7. Live regions

Each cell carries exactly one visually hidden `role="status" aria-live="polite"`
announcing run state and verdict (“Query returned 4 rows.”, “Your answer passed.”,
“Not yet: row count.”). A failed statement is a `role="alert"`; `.cellresult`
carries `aria-busy` while a query is in flight.

## 8. Rows that became buttons

The chapter rows on the Progress and Workbook screens were
`<div className="reportrow" onClick>`. A `div` takes no focus and Enter does
nothing, so the keyboard could not reach those rows. Both now render
`<button className="reportrow action">`, which is focusable and activates on
Enter and Space without a `tabIndex` or a `role`.

A computed-style comparison against the old `div` row matched 31 of 32
properties. The last, `text-align: start` against `left`, draws the same in
left-to-right text. The reset that gets there sits beside `.reportrow` in
`querycraft.css`.

Chrome 155 over CDP drove `npm run dev` and pressed Tab. `document.activeElement`
answered:

| Screen | Focusable stops | Chapter rows among them | Key | Result |
| --- | --- | --- | --- | --- |
| Home | 25 | 0 | | |
| Progress (“Your history”) | 15 | 9, stops 7 to 15 | Enter on “Read the data model” | opens the lesson “Read the data model” |
| Workbook (“SQL Analytics in the Real World”) | 22 | 9 | Space on “1. Read the data model” | opens the lesson “Read the data model” |

Every chapter row is a stop, and both activation keys open the chapter. Section 1
records 24 home stops; the same drive measured 25, so the count moved since that
pass.

## 9. The prompt dialog traps focus

`PromptDialog` carried `role="dialog"`, `aria-modal="true"`, an autofocus and an
Escape handler, but no trap. Tab walked out of the dialog and into the shell
behind it, which the learner cannot see. `PromptForm` now cycles Tab inside the
form, the same way `TablesDrawer` does, in the capture phase so the trap sees Tab
before the browser moves focus.

Driven the same way, from “Add a note” on the Workbook:

| Check | Observed |
| --- | --- |
| Focus moves in on open | yes, it lands on the text input |
| Tab stays inside | `input → Cancel → Close → input`, never leaving `.modal` |
| Shift+Tab at the first stop | wraps to Close |
| Escape | closes |
| Backdrop click | closes, the pointer path beside the keyboard one |

React Doctor's `prefer-html-dialog` finding cleared with the trap. Its reason was
the missing trap, not the absence of a `<dialog>` element.

## What is *not* verified

- **No automated runner.** Nothing re-checks this on every commit; the walkthrough
  was driven by hand (well, by script). A regression in any of the above would not
  fail `make test`.
- **No screen reader was listened to.** The checks assert roles, names and live
  regions in the DOM: the accessibility-tree inputs, not the speech an NVDA,
  JAWS or VoiceOver user actually hears.
- **Contrast was not measured with a tool.** The palette is unchanged from M1;
  no ratio is claimed here.
- **Zoom and reflow were not tested** at 200% or at a 320 px viewport.
- **Focus is not moved on SPA route changes.** After activating a nav item or a
  chapter, `document.activeElement` falls back to `<body>`. The skip link is the
  deliberate mitigation: one Tab from the top reaches it, but a keyboard user
  who wants the nav again pays for it. Recorded as a known limitation; the fix
  (move focus to `<main>` on route change, but not on first paint) is a follow-up.
- **The drawer stays in the DOM when closed.** It is `inert`, so it is out of the
  tab order and the accessibility tree, but it is not removed.
