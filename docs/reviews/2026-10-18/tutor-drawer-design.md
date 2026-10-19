# Ask Tutor drawer: design

Date: 2026-10-18. Author: product designer, delegated direction.
Scope: the v1 drawer from `docs/reviews/2026-10-18/expert-tutor-drawer.md` §8.
Mode: Operate. The learner completes the exercise; the drawer serves that task.

Companion: `tutor-drawer-mockup.html`, the answering state on desktop and the
bottom sheet on mobile.

## 1. Placement, and the Tables drawer next to it

The Ask Tutor button is the last control in the exercise `runrow`, after
`Show solution` and before `Keep query` (`web/src/components/SqlCell.tsx:344`).
It reads `<Icon name="sparkles" /> Ask tutor` in the existing `small ghost`
style. The sparkles glyph already marks the inline tutor call, so the drawer
keeps one icon for the tutor.

The drawer is the second right-side panel in the app. It is a sibling of
`TablesDrawer` and shares its slot, width, motion and focus contract:

- `.drawer` at `min(440px, 92vw)`, top to bottom, `translateX(100%)` closed,
  `220ms var(--ease)` open (`web/src/styles/querycraft.css:553`).
- `.drawer-back` scrim at `rgba(20, 22, 26, .42)`.
- `.drawerhead` and `.drawerfoot` on `--surface`; `.drawerbody` on `--bg`.
- `useDrawerFocus(open, asideRef, composerRef)`, the one focus-trap owner.

Only one drawer occupies the right edge at a time. The two describe one
exercise from two sides: the Tables drawer describes the learner's workspace
and where a reset belongs, and the tutor drawer holds the conversation about
it. The tables strip (§3) swaps the tutor drawer for the Tables drawer rather
than stacking a second panel. The chat lives in session state keyed by
exercise, so the swap loses no turns.

One conflict resolved here, recorded for review. The brief says the drawer
"must not trap focus". The incumbent drawer traps focus. The task names
`useDrawerFocus` as the one trap owner. Two right-side drawers that behave
differently for the keyboard is a consistency failure an Operate surface cannot
afford. The tutor drawer traps like the Tables drawer. The composer is the
initial focus, Escape closes, and focus returns to the Ask Tutor button.

## 2. Anatomy, top to bottom

| # | Region | What it holds |
| --- | --- | --- |
| 1 | Head | Eyebrow `Ask tutor`; title `Exercise 1.1 · Electronics by price`; a badge row with the dataset pill `commerce · 4 tables` and the dialect badge `PostgreSQL`; the Close button |
| 2 | Context summary | The honesty affordance. One sentence, then an expandable "What the tutor sees" list (§3) |
| 3 | Tables strip | One chip per table in the dataset, plus `Open tables` (§3) |
| 4 | Your latest query | A read-only code block with its run state (§4) |
| 5 | Turn list | Alternating learner and tutor turns (§5) |
| 6 | Chips | Four authored suggestion buttons above the composer (§6) |
| 7 | Composer | Textarea, `Send`, and the `Enter to send · Shift+Enter for a new line` hint |
| 8 | Foot note | `This chat is not saved.` in `--text-3`, 12px |

Regions 1 and 8 mirror the Tables drawer head and foot, so the two panels read
as one family. Regions 2 to 5 scroll inside `.drawerbody`; regions 6 to 8 stay
pinned in `.drawerfoot` (`margin-top: auto`). The composer is the one control
the learner returns to, so it does not scroll away.

## 3. The context summary and the tables strip

The summary renders from the `context` object the gateway returns, never from a
client guess. It answers one question: what did the tutor actually see?

Collapsed, one sentence built from the fields:

| `context` value | The sentence says |
| --- | --- |
| `expected: "included"`, `learner_result: "ran"` | This exercise, the commerce tables (4), your last run, and the expected result. |
| `expected: "columns_only"` | This exercise, the commerce tables (4), your SQL, and the expected columns. You have not attempted yet, so the tutor cannot see the expected rows. |
| `expected: "unavailable"` | This exercise, the commerce tables (4), and your SQL. This exercise carries no expected result for the tutor. |
| `learner_result: "not_run"` | This exercise, the commerce tables (4), and your SQL. You have not run it, so there is no result yet. |
| `learner_result: "unavailable"` | This exercise, the commerce tables (4), and your SQL. The gateway could not run it, so the tutor sees no result. |

Expanded under "What the tutor sees", one row each: exercise id and title;
dataset and dialect; the table names; the learner query state; the expected
result state. Expanded by default, because the honesty affordance is worth the
space. A `tables_truncated: true` adds one row, `Tables shown: first N of M`.

Before the first turn the client has no `context` yet. The summary then states
only the facts the client will send: the exercise, the dataset tables, and the
learner's run state. It never prints an expected-result line before the gateway
has answered, because the client does not know whether the gateway included it.

The summary never shows the expected rows, the reference query, another
learner's data, a credential, or a duration it was not given. It shows row
counts and column counts, which is what the response carries.

The tables strip is a wrapped row of small buttons, one per table name, with
`Open tables` at the end. A table chip opens the Tables drawer with that table
in view. In the empty state, a learner who does not know the schema reads this
strip first, so it sits above the SQL block.

## 4. The learner's SQL block

A label `Your latest query` in `.eyebrow`, then the SQL verbatim in a read-only
`pre` on `--code-bg` with `--code-line` and radius 6px, the same shape as the
reference-solution block (`SqlCell.tsx:517`). No editor is rendered: the block
is not editable here, and the cell above owns editing.

The run state sits under the block in 12px `--text-3`, in the words the engine
used:

- `ran` with the count: `Ran · 0 rows returned`
- `error`: `Error · UNDEFINED_COLUMN`, with the engine's message on the next line
- `not_run`: `Not run yet. The tutor sees your SQL but no result.`
- `unavailable`: `The gateway could not run this SQL, so the tutor sees it without a result.`

The SQL is the untrusted data the gateway fences in the prompt. The drawer
shows the learner their own text back, so the block is a mirror, not a claim.

## 5. A tutor turn

Each turn stacks three parts.

1. Role label. `You` or `Tutor`, mono, 10.5px, `.08em`, uppercase, `--text-3`.
   The tutor's label carries the sparkles glyph in `--accent-ink`.
2. Body. The learner's text is a plain paragraph on `--surface-2`, radius
   `--r-md`. The tutor's reply renders through `Markdown`
   (`web/src/components/ui/Markdown.tsx`), so fenced code becomes a code block
   on `--code-bg` and inline code stays inline. The reply sits on `--surface`
   with a 1px `--line` border.
3. Note. Under each tutor reply, 12px `--text-3`: `Guidance, not the answer, a
   gentle nudge. Ask a follow-up for more.` This is the `SqlCell` sentence
   (`SqlCell.tsx:502`), kept verbatim so the inline hint and the drawer speak
   with one voice. If `reveals_solution` is ever true, the note becomes an
   amber panel: `This reply names the approach. Your result still decides the
   pass, and any correct formulation counts.` The contract says the field is
   false; the UI does not trust it silently.

The not-saved note is conversation level, not per turn: `This chat is not
saved. Reloading starts a new one. Your SQL draft is kept.` one line in the
drawer foot.

## 6. The five states, with copy

**Empty.** The intro paragraph, the chips, and nothing else.

> Ask about this exercise. The tutor sees your query, the tables it runs on,
> and the result once you run it. It explains the problem, it does not hand
> over the answer.

**Thinking.** The learner turn appears at once, and a pending row takes its
place under it:

> `Asking the tutor…`

The spinner is the existing `.spin`, the row uses `.fb.pending` typography
(mono 12px, `--text-3`). The composer is disabled while the call is out. No
streamed text: the reply is one bounded response (brief §1).

**Answering.** The reply is already complete when it renders. The tutor turn
shows with its note, and the composer is live. Any typewriter effect would be
cosmetic, so v1 does not add one. This is the state the mockup shows.

**Error.** Fails for any code except `AI_RATE_LIMITED`:

> The tutor could not answer. `AI_UNAVAILABLE`
>
> The tutor call failed. Nothing here changed your exercise. Ask again in a
> moment.

Actions: `Retry`, which re-sends the same question, and `Dismiss`. The gateway's
own message is shown above the retry, because it names the cause better than a
generic string.

The unavailable case shares this panel with one changed sentence, the
`SqlCell` pattern (`SqlCell.tsx:487`):

> The tutor needs the gateway. This exercise still grades normally. The tutor is
> a helper, not a gate.

**Rate-limited.** `AI_RATE_LIMITED`, HTTP 429:

> Tutor limit reached.
>
> You have sent the tutor 30 questions this hour. It frees up again at 3:42 PM.
> Your exercise still grades normally.

The time comes from `Retry-After` when the gateway sends one, otherwise `Try
again in a few minutes.` `Retry` stays disabled until that time.

## 7. Suggestion chips

Four authored buttons, not generated text. A chip fills the composer and
focuses it; it never sends.

- Why is my result wrong?
- Which tables should I use?
- Walk me through the approach
- What does this error mean?

On the empty state the chips carry the intro. With turns on screen they stay in
one row above the composer, so they remain reachable on every follow-up. The
row scrolls horizontally on narrow widths.

## 8. Keyboard and focus path

- `Ask tutor` is a real button in the `runrow`, in tab order.
- Opening moves focus to the composer through `useDrawerFocus`'s `initialFocus`
  argument.
- `Enter` sends. `Shift+Enter` inserts a newline.
- `Escape` closes and `useDrawerFocus` returns focus to the Ask tutor button.
  `App.tsx:49` gains one branch beside the tables branch.
- Tab is trapped in the panel, as in the Tables drawer, and the panel is
  `inert` while closed.
- The reply is announced once through a single `aria-live="polite"` region,
  the `SqlCell` pattern at `SqlCell.tsx:309`.
- A chip is a button; activating it fills and focuses the composer.

## 9. Mobile

At `max-width: 760px`, the breakpoint where the shell shows the hamburger and
drops the nav, the drawer becomes a bottom sheet. It sits at `left: 0; right: 0;
bottom: 0; top: 12%`, with radius `--r-lg --r-lg 0 0`, a top hairline instead of
a left one, and the same 220ms slide up from `translateY(100%)`. The head keeps
the eyebrow and title. The chips and composer dock at the bottom, where the
thumb and the software keyboard are. The drawer body scrolls between them.

The Tables drawer stays a right panel. The split is deliberate: a schema panel
is read, and a chat composer is typed into, so the two want different edges on
a phone.

## 10. What it never does

- **No fabricated context.** Every fact in the summary comes from the response
  `context`. The client sends facts, never derived context. It never renders a
  result row, a table list, or an expected row the gateway did not return.
- **No reference query.** The drawer has no control that reveals SQL the tutor
  did not write. The `Show solution` control stays on the cell, where the event
  is recorded.
- **No solution-dump affordance.** No `Give me the answer`, no `Show the
  query`, no `Is my answer correct`. The composer accepts a question and Send.
- **No verdict.** The tutor never sets a status, claims a pass, or accepts an
  answer. Grading stays in Check answer.
- **No silent degradation.** A context the gateway could not build says so on
  its own row.
- **No persistence.** The foot says the chat is not saved.

## 11. Component inventory

| File | Role |
| --- | --- |
| `web/src/components/TutorDrawer.tsx` | New. The panel, its eight regions, the five states, the single live region, the composer |
| `web/src/components/ui/useTutorChat.ts` | New. The session chat machine: `Map<exerciseId, Turn[]>`, `ask`, `retry`, `reset`, and the state selector |
| `web/src/components/ui/useDrawerFocus.ts` | Reused. The one focus-trap owner; `initialFocus` is the composer |
| `web/src/components/ui/Markdown.tsx` | Reused. Tutor replies, so fenced code renders as code blocks |
| `web/src/components/ui/Icon.tsx` | Reused. `sparkles`, `x`, `table`, `alert`, `info`, `refresh`, `play` |
| `web/src/components/SqlCell.tsx` | Edited. The `Ask tutor` button in the `runrow` |
| `web/src/state/store.tsx` | Edited. `tutorOpen`, the open exercise, `openTutor`/`closeTutor`, beside `tablesOpen` |
| `web/src/App.tsx` | Edited. One `Escape` branch |
| `web/src/styles/querycraft.css` | Edited. A `.tutor*` block beside `.drawer*`, and the `760px` bottom-sheet rule |
| `web/src/types.ts`, `web/src/api/ai.ts` | Edited. `AiTutorContext` and its decoder, mirroring the contract |

`Markdown` is the right renderer for answers: it sanitises through DOMPurify
and already styles code and lists as the chapter prose does.

## 12. Unfinished

- Retry time text depends on a `Retry-After` header the current brief does not
  promise. v1 falls back to `Try again in a few minutes.`
- The turn list has no collapse or edit affordance. The brief does not ask for
  one, and `MaxTutorTurns` bounds the list at eight.
- The bottom sheet's keyboard avoidance is a device concern, not a CSS one.
  Test on a real phone before shipping; the draft uses the browser's own
  viewport shrink.
