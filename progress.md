# progress.md — live work tracker

Updated as work lands. The active goal is the **full product**: every remaining
milestone in `docs/technical-design.md` §14 (M5, M6) plus the cross-cutting work
the owner asked for — SQLite on the roadmap, markdown + Mermaid in chapter
content, three or more complete authored courses, local login/signup with user
scoping, real end-to-end AI verification, and a UI that reads as a product
rather than a mock.

Plan of record: `docs/m5-implementation-plan.md` (M5 "SQL performance lab").
Design source: `docs/technical-design.md` §14. Shipped: M2 `v0.2.0`, M3
`v0.3.0`. M4 is code-complete and verified; its plan of record stays at
`docs/m4-implementation-plan.md`.

Status legend: **done** (verified), **wip** (in flight), **todo**, **blocked**.

## Completion goal (owner-set, 2026-10-15)

> Complete the app. Do not stop until it is complete.

**"Complete" means:** every known open item is closed and every gate is green,
with the app runnable end-to-end from a clean checkout (`make build && make serve`,
`make verify` / `make verify-mock`). Committing and releasing the pending tags
remain the owner's call (AGENTS.md: do not commit or tag without asking), so
completion is *code-complete + gate-green + honestly tracked here*, not a tag.

The remaining open items, in dependency order:

| # | Item | Owner | State |
| --- | --- | --- | --- |
| 1 | Agent-run audit trail (transcripts: owner-scoped, redacted, bounded, over HTTP) | delegated | **done — verified** |
| 2 | Design craft pass on `web/` (Impeccable) + generated `PRODUCT.md`/`DESIGN.md` | delegated | **done — verified** |
| 3 | `exercise_count` unsatisfiable request (a request for fewer exercises than chapters can never be satisfied) | delegated | **done — verified** |
| 4 | Backend simplification pass in `internal/**` — verify what (if anything) landed; close the track | — | **todo** |
| 5 | App metadata → SQLite (users/sessions/progress/events/courses/challenges/patches/transcripts), PostgreSQL stays the practice engine | — | **todo** (owner-raised; lands after 4, which also touches `internal/store`) |
| 6 | Outline approval — pause after the outline, learner approves or discards, then chapters build | — | **done — verified** (`docs/outline-approval-design.md`) |
| A1 | Release blocker — account-scoped local progress (was: progress bled across accounts on a shared browser) | delegated | **done — verified** |
| A2 | Release blocker — empty-admin-password exposure (loopback default + refusal on non-loopback + hint only when true) | delegated | **done — verified** |
| A3 | Release blocker — brittle `audit-shell` (was red on a correct stylesheet) | — | **done — verified** |
| F2 | Architecture review finding — dialect-aware `POST /environment/reset` (a SQLite chapter's "restore my data" reset PostgreSQL today) | — | **done — verified** |

Items 4 and 5 share `backend/internal/store`, so they run in sequence, not
in parallel.

### Parallel worktrees (owner-set, 2026-10-16)

The owner asked for parallel work in separate worktrees, merged when done.

**Blocker found and fixed first:** `delegate_task isolation:"worktree"` starts from
the parent's *committed HEAD*, and HEAD was `7c951de11 "M4 step 1"` while M5–M8
lived only as 233 uncommitted files. A worktree would have handed an agent a
three-milestones-old app (no SQLite, no perf lab, no course generation) that
cannot verify anything. So the owner approved a **checkpoint commit**:
`e7f39de1a`, a verified-green snapshot (`go test ./... -count=1` all 15 packages;
`web` typecheck + build clean) with the full M5–M8 app. Agent tooling dirs
(`.agents/ .codex/ .gemini/ .opencode/ .impeccable/`) were gitignored so they
never enter the product repo. No tag, no push — a checkpoint, not a release.

**First two parallel worktree tracks** (both branch from `e7f39de1a`, verified to
contain M6/M8):

| Track | Worktree | Branch | Scope |
| --- | --- | --- | --- |
| Login throttling | `t3-665821a8` | `t3/665821a8` | per-IP/account failed-login throttle on `POST /auth/login`, 429, bounded map |
| SQLite env cap + reaper | `t3-779bab08` | `t3/sqlite-workspace-eviction` | bound the `byAccount` executor cache (F4), idle eviction, keep isolation |

Both are asked to **commit in their worktree** and report branch + sha, so they
can be merged back after verification. Main checkout continues item 6 while they
run — genuinely parallel, disjoint files.

Also landed this session (Luna, Codex `gpt-6-luna` xhigh/priority — verified live
first): **self-hosted fonts.** `web/public/fonts/*.woff2` (14 subsets, 220 KB),
`web/src/styles/fonts.css` imported by `querycraft.css`, Google Fonts `<link>`s
removed from `index.html`. Verified: `typecheck`+`build` clean, `dist/` has **0**
`fonts.googleapis.com`/`gstatic.com` references and all 14 woff2 copied. The app
now renders fully offline, which is its own premise.

### Polish stream (owner-set, 2026-10-16) — small refinements toward a good v1

> "Keep improving the app. Add small refinements, reproductions, UI/UX
> enhancements, small interactions, micro-interactions that make the app better."

Guardrails for this stream: **extend, never redesign** (AGENTS.md preserves the
visual design and copy); every change must be verifiable and must not weaken an
honesty rule; nothing is "polish" if it makes the app claim something it does
not do. Ranked by "is this a mock tell or a real dead end" first, cosmetic last.

Landed:

- **F2 — dialect-aware `POST /environment/reset`** (architecture review finding).
  Done and verified; see below.
- **"Explain my mistake" is now a real tutor call, not a stub.** It toasted *"In
  a live app, a tutor call would…"* — the classic "it's a mock" tell the tracker
  warns about — although the gateway already serves `POST /tutor`
  (`SqlCell.tsx`). It now calls `s.api.tutor({sql, objective, error_code,
  error_message, hint_level, result_summary})` and renders the hint with its
  level, with three honest states: a spinner while asking, the hint (naming
  whether it reveals the approach), and — on the offline/mock adapters or AI-off,
  where the seam fails with `AI_UNAVAILABLE` — a plain "Tutor unavailable" that
  says the exercise still grades normally. It never invents an answer. The
  "ask for a firmer hint" action escalates `hint_level` from the previous
  answer (without it the second ask repeated the first — the level is the whole
  difference between a nudge and a worked approach). The live region announces
  all four states. `npm run typecheck` + `npm run build` clean.

Still open in this stream (candidates, not yet done):

- "Report a problem" (`SqlCell.tsx`) and "Save challenge"
  (`ChallengeLabScreen.tsx:551,627`) are still toasts; both have real gateway
  endpoints (`/challenges/{id}/critique`, `/challenges`) and are the same
  mock-tell class as the tutor button that is now fixed.
- The render-blocking third-party Google Fonts `<link>` (`web/index.html:8-10`)
  — self-host for an offline-first app (flagged by both the craft pass and the
  architecture review).



An independent read-only critic drove the built binary and read the source. Its
verdict: the one-sentence promise ("learn SQL by writing it; graded on the
result") is **delivered** — `course-audit.mjs` ran 53/53 authored solutions to
green across 8 courses and both dialects, correct/wrong submissions grade
correctly, and error paths leak no stack or credential. But it named three
release blockers. All three were independently re-verified here:

- **B-A1 (privacy, silent).** `qc.progress` is one global localStorage key
  (`web/src/state/store.tsx:62`) and `login`/`logout` never reset `progress`
  (`:725-752`), while hydration early-returns when the new account has no server
  snapshot (`:663`). On a shared browser, learner B sees learner A's passed
  exercises/notes/saved queries, and B's next write uploads that history into
  B's row. Confirmed in source.
- **B-A2 (security).** The seed creates `admin@querycraft.local` with an **empty
  password** unless `QC_ADMIN_PASSWORD` is set (`backend/internal/auth/auth.go:163-164`,
  warn-only), and the sign-in screen advertises it unconditionally
  (`AuthScreen.tsx:169-174`) regardless of whether a password was set. No login
  throttling. Confirmed in source.
- **B-A3 (release gate red).** `make test` failed `audit-shell` — **fixed here**:
  it was a brittle test, not a broken layout. `scripts/shell-audit.mjs` read only
  the *first* `@media(max-width:760px)` block; the craft pass added a new one
  earlier in `querycraft.css`, so the audit could no longer see
  `.navtext{display:none}` (now at `:599`). The audit now searches every 760px
  block. Verified non-vacuous: it still fails when the rule is genuinely removed
  and passes when it merely moves.

Should-do items it found (real, not blocking): two learner-facing buttons are
still stubs that toast ("Explain my mistake", "Report a problem",
`SqlCell.tsx:222,225`) though the gateway already serves `/tutor`; "Save
challenge" is a toast not a save (`ChallengeLabScreen.tsx:551,627`); a generated
course's `setup.statements` are applied on manual workspace reset only
(`TablesDrawer.tsx:129`), not on course open; README says "three courses / five
milestones" where the tree ships 8 courses and M1–M8. It also flagged the
render-blocking third-party Google Fonts `<link>` (found independently by the
craft pass) and unverified `exercise_count` (item 3, in flight).

Defer (safe for v1): metadata → SQLite (item 5), the `/runs` UI consumer, login
rate-limiting beyond A2, bundle code-splitting, an automated a11y runner.

### Architecture expert review (2026-10-15) — Claude Haiku 5.5 via Codex, reasoning `max`

An independent outsider review (no edits, opinion only) of the whole codebase —
README, AGENTS.md, the spec, the ADRs, `progress.md`, `backend/internal/**`, the
`ExecutionService` seam and its three adapters, `store.tsx`, `contracts/`.
Verdict: **the product promise and the core safety design are real** (one
`Execute` path with caps, restricted role, dialect profile as data, owner scoping
from the token); the structural risk is **grading and routing re-implemented per
adapter in two languages with no shared executable spec.**

Findings re-verified here (the review is a judgement, not gospel):

- **F1 — Grading differs by adapter; authored checks are dead outside the mock.**
  Confirmed: `curriculum.ts` ships **175** `test: function(s){ return /…/.test(s) }`
  closures (e.g. `:37-39`); the mock runs them (`execution.ts:419-432`); PGlite
  grades by *result* (`:767`) and never reads `block.checks`; the HTTP adapter
  sends no `checks` (`:1118-1148`). This is the deepest fault: the learner's
  checklist changes with the adapter, and the spec's "grade the result, not the
  text" rule is violated in the mock. **Worth fixing** — it is the one latent
  correctness gap with no test watching it.
- **F2 — SQLite reset resets the wrong database on the HTTP adapter.** Confirmed:
  `execution.ts:1168` drops `_dialect`; `handleEnvironmentReset`
  (`environment.go:155`) has no dialect input and only drives the PostgreSQL
  manager; `httpapi/sqlite.go` has no reset path at all. On a SQLite chapter
  "restore my data" returns `ok`, resets PostgreSQL, and leaves the SQLite file
  untouched — the honesty rule broken on the second dialect. **Worth fixing.**
- **F3 — learner→workspace privacy fix *was* half-done.** *Already resolved since
  the review read the tree:* the write path now uses `progressKeyFor` throughout
  (`store.tsx:585,713,724,733`) and an owner change resets progress state
  (`:572-582`). This is the A1 work landing. Recheck when A1 reports.
- **F4 — two practice-environment managers, no shared abstraction.** Confirmed:
  `envdb.Manager` (Postgres, capped/reaped) vs `httpapi/sqlite.go` `byAccount`
  map (`:63,133,153`) with no cap, no reaper, no reset. "SQLite is second-class"
  is accurate.
- **F5 — `envdb` serialises all accounts behind one `m.mu`.** Confirmed:
  `Ensure:375`, `Reset:443`, `EnsurePerf:267`, `reapOnce:185` all lock `m.mu`
  across `CREATE`/`DROP DATABASE`; ADR 0002 measured a 2.9 s first drop.
- **F6 — `store.Store` embeds `ai.Depot`** (27 methods, `store.go:47-77`) and
  imports `internal/ai`; boot silently falls back to memory (`main.go:264-282`).
- **F7 — default bind is all-interfaces.** Confirmed: `QC_ADDR` defaults to
  `:8080` (`main.go:43`), contradicting the spec's "loopback by default", and
  combined with the empty-password admin (`auth.go:164`, warn-only) + the
  advertised hint, a fresh `make serve` on shared Wi-Fi exposes an admin login.
  This overlaps release blocker **A2**.
- Also noted: exercise shape accepts historical spellings with no canonical form
  (`eval.go:60-95`); the hand-written `sqlite_raw.go` driver (justified by a
  measured defect); Makefile writes shared `/tmp/progress.cjs`,
  `/tmp/curriculum.cjs` (`Makefile:253-371`) — racy and not hermetic; the
  `SqlCell.tsx:222,225` stub buttons (already tracked).
- **Explicitly praised and told us not to touch:** ADR 0001–0007; the single
  `Execute` cap path; the dialect profile as data
  (`contracts/dialects/sqlite.json`) — called "the best seam in the repo"; the
  dataset SQL imported by both languages; owner scoping from the token; the
  routing-matrix tests; the honest-refusal design.

Its top-3 structural changes: (1) make grading one executable spec the Go
evaluator owns, so every adapter sends the same bundle; (2) one practice-
environment manager with a dialect-aware reset; (3) split `store.Store` from
`ai.Depot` and fix the bind/empty-password defaults. Judgment: F1 and F2 are the
release-relevant ones; F4–F6 are real but post-v1; F7 folds into A2.
Notes it left at `/tmp/review/notes.md`; no edits, no commits.



The sandbox restarts unpredictably and cancels any in-flight delegated child
agent (it is not resumed). A recurring "Querycraft completion drive" task,
bound to this thread, recovers: it leaves a genuinely-running track alone,
relaunches one killed by a restart whose work is not on disk, verifies any
finished track against the real files, and advances the first unblocked item.
Three batches were killed by restarts before this; the fourth is the first to
land work.

Drive tick (latest, uptime ~228 min, no restart): **two worktree tracks running,
both far along, neither blocked.**
- Login throttling — worktree `t3-665821a8`, running: has written
  `login_throttle.go` + `login_throttle_test.go` and edited `auth.go`,
  `server.go`, `openapi.go`, `cmd/api/main.go`, `backend/README.md` (7 files).
  **Its branch builds clean.**
- SQLite env cap + reaper — worktree `t3-779bab08`, running: `sqlite.go` +
  new `sqlite_cache_test.go` + `cmd/api/main.go` + `backend/README.md`
  (4 files). **Its branch builds clean.**

Item 6 is done and verified (see above). Both worktree agents are still running —
almost certainly their own gate suites — so they are left alone. Neither branch
has committed yet. Main checkout is quiet; full gates run once the worktrees land
and are merged.

Item 2 (craft pass) — **done and verified (2026-10-15).** The agent's report was
checked against disk: `npm run typecheck` and `npm run build` both clean; the
token split is real (`--accent-ink`/`--green-ink`/`--amber-ink`/`--red-ink`
separate accent-as-fill from accent-as-text, `querycraft.css:28-46`),
`minmax(0,1fr)` on the page grids, `.resultfoot` defined (`:374`), the Mermaid
theme `MutationObserver` in `Markdown.tsx:32`, and the two `aria-label`s in
`WorkbookScreen.tsx:132,152`. Artifacts: `PRODUCT.md`, `DESIGN.md` (North Star
"The Working Terminal"), `.impeccable/design.json`. Measured before → after:
course-screen h-scroll (1121 vs 1036 wide) → 0; 18 contrast failures on
`#/library` → 0; gutter drift 1.75 px/line → 0.00; hit targets 36×26 → ≥28 px.
Two items it flagged rather than fixed (correctly out of a refinement pass):
the render-blocking third-party Google Fonts `<link>` in `web/index.html:8-10`
(should be self-hosted for an offline-first app), and ~9 px internal min-content
deltas at 320 px only (no page scroll).

Release blockers A1 + A2 — **done and verified (2026-10-15/16).** Verified against
disk, not the agent's summary: `go test ./... -count=1` green on **all 15
packages**; `go build`/`go vet` clean; `web` `npm run typecheck` + `npm run build`
clean.

F2 (dialect-aware reset) — **done and verified (2026-10-16).** Implemented
directly (not delegated). `POST /environment/reset` now takes `?dialect=`, absent
meaning PostgreSQL; a SQLite request restores the learner's *file* via the new
`SQLiteEnvironments.Reset` (`sqlite.go`), which closes the cached executor,
removes the file family (`-journal`/`-wal`/`-shm` too, so a stale journal cannot
replay over the fresh seed), and re-seeds from the committed fixture under the
same lock — the SQLite shape of ADR 0003's drop-and-copy. The HTTP adapter no
longer drops the dialect (`execution.ts` `environmentResetPath` sends it), and
OpenAPI documents the parameter. Three new tests in `dialect_test.go` assert by
*querying the engine*, not by trusting the response body: writes are discarded
(31→30, the row gone), one learner's reset does not touch another's file, and an
anonymous caller is refused. `go build ./... && go vet ./...` clean; full
`go test ./... -count=1` green on all 15 packages (including `httpapi`, 49 s).

*A1 (progress bleed).* The fix is real and covers all three failure points:
storage is owner-scoped (`progressKeyFor` → `qc.progress.<userId>`, `.local` for
the offline workspace, `store.tsx:70-71`); a `useLayoutEffect` swaps the
in-memory snapshot before paint on every owner change (`:575-582`); and hydration
now treats an **absent** server snapshot as authoritative on the `http` adapter —
it clears the local copy instead of returning early (`:723-742`), which is
exactly the path that let B see A's history. Offline adapters keep their local
snapshot so the local workspace still survives a reload. Honest gap the agent
declared: the multi-account *browser* reproduction was not run (no browser
attached; the repo has no DOM/JS test harness at all — no vitest/jest/jsdom and
zero `*.test.*` files), so the frame-level behaviour rests on `useLayoutEffect`
semantics verified by reading, not exercised at runtime. Server-side owner
scoping was proven against the real binary + embedded PostgreSQL.

*A2 (empty admin password).* `defaultAddr = "127.0.0.1:8080"` (`main.go:288`),
`isLoopbackAddr` fails closed on wildcard/`0.0.0.0`/LAN/IPv6-any/non-`localhost`
(`:296`, unit-tested in `main_test.go:18`, passing), and startup is **refused**
with exit 1 (no password/hash printed) when the admin password is empty and the
bind is not loopback (`:175-178`). The check reads the *stored hash* and calls
`auth.Verify(hash, "")` — correct, because the seeded blank password is a real
bcrypt hash, not an empty string. `/health` gains the additive, non-secret
`admin_password_empty` boolean (`handlers.go:38`, `openapi.go:1047`), and the
sign-in hint renders only when it is true (`AuthScreen.tsx:174`). Harness binds
were updated to loopback so the gates still boot (`Makefile:27`
`SMOKE_ADDR=127.0.0.1:18080`; `scripts/lib/gateway.mjs`, `scripts/load-test.mjs`)
— validated live: wildcard+empty → refuses; loopback+empty → starts, hint true,
blank login 200; non-loopback+password → starts, hint false, blank login 401.

Not built (declared out of scope): login throttling. Recommended follow-up.

Item 6 (outline approval) — **done and verified (2026-10-16).** Verified against
disk, not the agent's summary: `go build ./...` + `go vet` clean, `gofmt -l`
clean, **`go test ./... -count=1` green on all 15 packages**, and `web`
`npm run typecheck` + `npm run build` clean. Route registered
(`server.go:197 POST /courses/{id}/build`); `StatusAwaitingApproval`
(`api.go:94`) and `GenerationRequest.RequireApproval` (`:163`); the resume data
change is real — `CourseRecord.Request *GenerationRequest` (`course.go:170`)
persisted as a nullable `request jsonb` (`postgres.go:99`), nil-tolerant for
legacy rows. 5 new `ai` tests + 6 new `httpapi` tests exist, including the
verbatim end-to-end (submit → pause → read the index → approve → active) and
`TestTheDefaultCourseRunIsUnchanged`. The default path is genuinely unchanged:
the pause is gated on `req.RequireApproval` (`course_agent.go:754`), so a client
that never sets the flag sees exactly the old behaviour. Frontend: the opt-in
checkbox ("Show me the outline before the chapters are written",
`CreateScreen.tsx:57`) and the index with **Build it** / **Discard**
(`BuildingScreen.tsx:729`). Contract changes it declared and justified:
`contracts/ai.schema.json` (new status + `require_approval`) and
`contracts/run.schema.json` (`runStatus`) — required, because a response or
transcript carrying the new status must validate.


Item 3 (`exercise_count`) — **done and verified (2026-10-15).** Verified against
disk: `go build ./internal/ai/` + `go vet` clean (the only build failure on the
tree is `cmd/api/main.go`, which the concurrent release-blockers agent is
mid-edit — not this work), `gofmt -l internal/ai/` empty, and
`go test ./internal/ai/ -count=1` green (38.5 s). Decision, and it is the right
one: **actionable refusal at the outline gate, not a request-time floor** —
because the model picks the chapter count in the outline call, so "raise
exercise_count to at least the chapter count" is undefined at request time, and
a guess would silently rewrite the learner's ask. `courseExerciseBudget`
(`course.go:394`) is `clampInt(req.ExerciseCount, 2, maxCourseExerciseTotal)` —
bit-identical to the old inline expression, so the success path is unchanged in
behaviour (the report's "byte-stable" wording was loose: the line now calls the
helper, but it computes the same number). `courseOutlinePrompt` appends one
sentence naming the budget and the at-most-N-chapters coupling as a strict
suffix; `validateCourseOutline` takes the budget and returns
`courseOutlineExerciseError`, which names the number to ask for instead of the
raw provider-shaped `chapters[0].exercises`. Three new tests
(`course_agent_test.go:175,250,354`) including the verbatim defect case, which
now fails with no scratch environment spent.

Item 1 (audit trail) — **done and verified (2026-10-15).** Verified against disk,
not the agent's summary: `go build ./...` and `go vet ./...` clean;
`go test ./... -count=1` green on **all 13 packages**. `app.runs` DDL with an
owner index (`postgres.go:118,131`); bounds `MaxTranscriptEntries` 512 /
`MaxTranscriptBytes` 256 KiB / `MaxRunList` 100 (`transcript.go:44-55`);
`GET /runs` + `GET /runs/{id}` registered with `404` (never `403`) asserted for
another owner (`runs_test.go:139,174,416`); redaction covered by 7 credential
shapes plus the provider's own scrub (`transcript_test.go:15,54,263`); documented
in `contracts/README.md`, `backend/README.md` and the OpenAPI. The agent also
fixed a real race it exposed: `course_agent.go` published the "chapters verified"
counter before `storeBuilding()` wrote the chapter, so a poll could see a
verified chapter the record did not yet have; storage now precedes publish.

## Milestones

| Milestone | State | Evidence |
| --- | --- | --- |
| M1 — contracts + mock adapter | done | `contracts/`, `web/src/api/` |
| M2 — complete workbook (browser) | done, tagged `v0.2.0` | commit `2e7eac2be`, `make verify` green |
| M3 — execution gateway (Go + native PostgreSQL) | done, tagged `v0.3.0` | this file |
| M4 — AI Challenge Lab | done | `make test` green; `make audit-ai` 6/6; **real-model live audit green** (below) |
| M5 — SQL performance lab | done | all 8 steps landed; `make audit-perf` green on all four clauses (below); `docs/m5-implementation-plan.md` |
| M6 — second dialect: SQLite | done | all 9 steps landed; `make audit-parity` green for both dialects (PostgreSQL 119/119, SQLite 49/49, 0 diverge); `docs/m6-implementation-plan.md` |
| M7 — real course generation | done | a live model generated a four-chapter course; every exercise's reference, cross-check and adversarial fixtures executed in a throwaway database, and the activated plan carried the engine's own rows. `backend/internal/ai/course_test.go` (6 tests, real PostgreSQL), `httpapi/ai_test.go` |
| M8 — a course that builds itself, one chapter at a time | **landed, verified live** | tool-calling agent loop in one conversation (`backend/internal/ai/course_agent.go`): outline first, then one `save_chapter` per chapter, each chapter verified and persisted before the next is asked for. Live evidence below. `make test` green including `audit-course-gen` |

## M7 / M8 — real course generation, and the streaming rewrite

### What M7 changed (the honesty fix)

Before M7 the "Create a course" path did not call the gateway at all. The
summary said the request was rejected, because the activated course arrived
with `plan: undefined`: `asAiOutcome` in `web/src/api/ai.ts` parsed the
envelope but never parsed `plan`/`chapters`, so a course that had genuinely
been generated, verified and stored was read by the screen as a refusal. The
draft studio then told the learner "the course was rejected at the
verification stage" — a false statement about work that had succeeded.

Fixed with strict parsers (`asCoursePlan`, `asCourseChapter`,
`asCourseExercise`, `asCourseSection`, `asExpectedResult`, `asExerciseResult`,
`asChapterResults`): a plan is all-or-nothing, and a missing measured answer
stays missing rather than being invented. 18 assertions in
`web/scripts/ai-adapters-audit.mjs`.

Three further defects were found and fixed on the way:

| Defect | Why it mattered | Fix |
| --- | --- | --- |
| every AI call used the 15 s query deadline with 2 retries | a ~147 s generation fired three aborted requests and reported "Could not reach the execution gateway" | per-call `AiPerformOptions{timeoutMs, retries}`; generation/tutor/critique/report get the long deadline and **zero** retries (`web/src/api/aiDeadline.ts`) |
| the provider call budget was 45 s | a real course bundle needed 58.5 s, so the *default* path could never complete | `defaultAICallTimeoutMS = 120000` (`backend/cmd/api/ai.go`) |
| the stage checklist turned "Done" on a 420 ms client timer | four of six steps showed Done before the request had been answered | steps only turn Done on the gateway's own reported stages; the client-paced lines are only the client's own two acts |

### What M8 changed (the shape of a run)

M7 asked for the whole course in **one** reply and blocked until it arrived
(147 s measured). Nothing was real until the end, one rejected exercise
discarded every chapter, and nothing was in the database while the run worked.

M8 replaces that with one conversation and a tool per chapter. The model is
asked for an outline (small, fast, and it is what the screen draws while the
chapters are still being written), then calls `save_chapter` once per chapter.
The gateway verifies that chapter's references, its cross-checks and its
adversarial fixtures in the course's scratch environment and persists it **as
soon as it passes**; the tool result carries the engine's own verdict back into
the conversation. `finish_course` closes the run and the stored row moves from
`building` to `active`.

### Live evidence (real provider, this session)

Provider `openai-compatible` / `deepseek-v4.1-flash`, against the built binary
serving its own embedded PostgreSQL:

* `POST /courses/generate` (`exercise_count: 6`) returned a job; polling
  `GET /generation/{id}` showed the outline (5 chapters) at **10 s**, chapter 1
  verified at **26 s**, and the run finishing `active` with **5/5 chapters
  verified**.
* Course `course-eed16f68c1438c9b`: *Windows Without Collapsing Rows* (1
  exercise), *Ranking Rows* (2), *Running Totals and Window Frames* (1),
  *Comparing Rows to Their Neighbours* (1), *Putting Windows Together* (1) —
  6 exercises, every one executed and cross-checked by the engine.

### Defect found by driving it: an unsatisfiable request

`exercise_count` is omitted from the request by default and reads as `0`,
which normalizes to **1** exercise. The outline gate requires at least one
exercise *per chapter*, so a request for one exercise across several chapters
can never be satisfied — the run hard-fails with
`chapters[0].exercises: a chapter must plan at least one exercise`, a
provider-shaped validation error for a request the contract permits. Open:
needs either a floor (`exercise_count` raised to at least the chapter count)
or an up-front refusal that says what to ask for.

### The learner's database was not the verifier's database (open)

`plan.setup.statements` were applied when the course was verified
(`pipeline.prepare`, `course_pipeline.openScratch`) but never in the learner's
environment: the gateway clones a sealed template per account, and the browser
PGlite adapter loads `schema.sql`/`seed.sql` raw. A course whose setup created
a scratch table would therefore verify green and then fail for the learner with
`relation does not exist`. The gate permits exactly those statements
(`validate_test.go`: "setup CREATE TABLE accepted"). Fix in flight: thread the
course through `resetEnvironment` on both adapters so the learner's environment
is the environment that was verified.

## In flight (parallel tracks, this session)

| Track | Owner | Scope |
| --- | --- | --- |
| Backend simplification | `McClintock` | dead code, duplicated rules and long functions in `backend/internal/**`; behaviour and test-greenness preserved |
| Harness disk leak | `Dirac` | find the path that still leaks `qc-*` dirs / `embedded_postgres_log*` per run; add teardown and a disk-usage target |
| Agent-run audit trail | `Mencius` | persist each generation's transcript — every message, tool call and tool result — owner-scoped in the database, redacted, bounded, readable over HTTP |

## Roadmap decision — app metadata belongs in SQLite (owner-raised)

The owner has now raised this twice ("for the POC we don't even need pg, we
could have used sqlite"; "why not save the transcripts and tool calls in the
SQLite db"). The transcript work makes the trade-off concrete, so it is
recorded here as a decision to make rather than a detail to discover later.

The app metadata store is small and already has a clean seam: one `Store`
interface (`backend/internal/store/store.go`) with a `postgres` and a `memory`
implementation, seven `app.*` tables, 332 lines in `postgres.go`. And
`modernc.org/sqlite` — pure Go, cgo-free, so it keeps the single binary — is
already a dependency for the SQLite dialect. The practice environment is the
part that genuinely needs PostgreSQL: the product teaches Postgres, and
per-account writable databases, template cloning and roles are Postgres
features.

So the boundary to draw is:

* **SQLite** — what the app knows: users, sessions, progress, events, courses,
  challenges, patches, and the new run transcripts. One file, no server, no
  `embedded_postgres_log*`, and something you can copy and read to audit a run.
* **PostgreSQL** — what the learner practices on: the sealed dataset, the
  per-account writable workspace, the plan environments.

Not started. It must land *after* the simplification pass, because both touch
`internal/store`.

## UI pass — defects found by driving the product (2026-10-15)

The goal asks for the app to be driven *as a learner drives it*: click the
buttons, follow the links, take the notes, submit, retake. Two defects below were
found that way and are fixed on this branch. Neither was reachable from the test
suite — the audits exercise the API and the graders, and no audit renders a page.

### The document scrolled, so the whole shell slid out from under the learner

Found by opening a chapter and looking at where the sidebar had gone: it was at
`y = -220`. The sidebar and header are `height:100vh` inside
`.app{overflow:hidden}`, so they should never move — but `document.scrollingElement.scrollHeight`
was **4994 px**, not the 800 px of the viewport, so the browser could (and did)
scroll the document itself, taking the nav and the header with it.

The cause was `.visually-hidden{position:absolute}` with **no positioned
ancestor**. An absolutely-positioned box whose containing block is the initial
one is not clipped by a `static` ancestor's `overflow:hidden`; the lesson page
carries four of these (a live-region and three table captions), each sitting at
its static position ~4000–5000 px down the page, so they extended the document's
scrollable overflow by that much. Anything that revealed a focused control —
clicking **Run**, Tab, `scrollIntoView` — then scrolled the shell.

| Probe | Before | After |
| --- | --- | --- |
| `document.scrollingElement.scrollHeight` on `#/course/sqlite-essentials/s1` | 4994 | 800 |
| `scrollWidth` (a closed `.drawer` is `translateX(100%)`) | 1270 | 1280 |
| sidebar `getBoundingClientRect().top` after clicking **Run** | −220 | 0 |

Fix: `.app` is now `position:relative`, so those nodes have a containing block
inside the clipped subtree. `position:relative` with `z-index:auto` creates no
stacking context, so the header (`z-index:20`), drawer (`45/46`) and toast (`80`)
are unaffected — verified with the drawer open, the search panel open and the
theme toggled.

### The nav rail below 760 px was nine anonymous icons

`.brandname,.navtext,.label,.navbadge{display:none}` at `max-width:760px` leaves
a 64 px icon rail, and the icons are `aria-hidden`. Nothing carried the label:
no `text` (hidden), no `title`, no `aria-label`. On a phone the app was nine
unlabelled buttons — unusable rather than merely unlabelled, since there is no
hover on touch to recover the name.

| Probe (390 px viewport) | Before | After |
| --- | --- | --- |
| `button.nav` with a non-empty accessible name | 0 of 9 | 9 of 9 |

Fix: `NavButton` and the recent-chapter buttons in `web/src/components/Shell.tsx`
carry `aria-label` and `title`. Additive — the visible type is unchanged.

### Also driven, no defect found

- Working example runs on `SQLite 3.50.3` through the gateway and `3.53.4` in the
  browser; a correct `ex.s1.1` submission returns **Passed** with the four
  contract clauses itemised; the wrong one returns `ROW_COUNT_MISMATCH`.
- Note → workbook, bookmark → workbook, **Keep query** → saved queries: all three
  round-trip and read back with their chapter number.
- Search returns chapters, exercises, sections and objectives; a section hit
  opens the chapter it belongs to (it lands at the top of the chapter, not at the
  section — the fragment names no anchor, which is a limitation, not a fault).
- The tables drawer is a real modal: `Escape` and the backdrop both close it and
  restore `pointer-events:none`; while open it traps Tab and blocks the page, as
  its own doc comment says.
- Theme cycles light ⇄ dark from the header with the Appearance screen holding
  the third state; Mermaid renders (one 683×615 SVG on `s3`).

### Every lesson rendered the fallback prose: the reading content never loaded

The chapters are authored as markdown, seeded into `app.content_docs` and read
back out of the database — that is what "the database is what the lesson renders"
means. On `#/course/sqlite-essentials/s3` the *In this chapter* list showed
`01 Concept 2 parts · 02 Exercise s3.1`: the short curriculum outline, not the
chapter's own document, which holds five sections (`Every ordinary row has an
address` … `Check yourself`). `store.content` held **0 of 52** modules.

The first thing found was real and worth fixing on its own: `web/src/state/store.tsx`
loaded the content with `Promise.all(...).catch(() => {})`, so *one* module
failing blanked all fifty-one others and nothing anywhere said so. It now settles
each module on its own, retries the ones that failed, warns with the module ids,
and tells the reader when nothing loaded at all (a lesson that quietly shows
summary prose in place of the chapter is the "it's a mock" tell).

But that was not the cause. With the retry in place a fresh tab still showed the
outline, and a single `fetchModuleContent('s3')` issued from the page was **still
pending after 53 s** — the reads never settled, so there was nothing to retry.

The cause is the engine's identity. PGlite's `PGliteWorker` elects a leader
across every worker that shares an `id`, and the app's ids were constants
(`querycraft-dataset-commerce`), deliberately, so two datasets would not share
one election. Constants also mean two *tabs* share one election: the losing tab
proxies every query to the winner, so when the winning tab is reloaded or closed
the follower waits on a leader that will never answer — forever, silently, and
with no timeout anywhere in the boot to end it.

| Probe | Before | After |
| --- | --- | --- |
| `fetchModuleContent('s3')` in an orphaned tab | pending after 53 s | 5 sections |
| `store.content` modules, fresh tab | 0 of 52 | 52 of 52 |
| `In this chapter` on `s3`, fresh tab | `01 Concept 2 parts` (fallback) | 6 groups, the authored 5 + the exercise |

Fix, both parts in `web/src/api/pglite.ts`:

- the election id carries a per-page-load suffix, so one tab is one engine and
  one engine is one election (`TAB_ID`);
- the boot is bounded (`BOOT_TIMEOUT_MS`, 20 s). A worker that cannot answer is
  terminated and the engine is built on the page instead, which is announced in
  the console because a main-thread engine cannot pre-empt a runaway statement
  (that is already what `engineCapabilities().placement` tells the UI).

Verified by driving: a fresh tab renders the authored chapter within ~3 s, and
two tabs open on two different courses each render their own authored markdown
and stay independent of each other.

### The guard, and what it does not prove

The engine defect is now gated: `scripts/boot-audit.mjs` (`make audit-boot`,
inside `make test`) bundles the shipping adapter with a worker whose `create`
never settles and checks that the boot returns, takes the page engine, says so
once, and then really runs the seeded dataset (exactly 80 orders) — and that the
election id is per dataset *and* per page load, by booting two module instances
and comparing the ids they presented. Both halves were negative-tested: with the
id made constant it fails 1 check, with the main-thread fallback removed it fails
3. It cannot see a real browser's `Worker` or `BroadcastChannel`, so the election
itself is still checked by driving the app; the adapter's decisions are not.

The remaining defects were unreachable from `make test`, because no audit in this
repo renders a page: the audits drive the API, the graders and the content. There is
no Chrome, Puppeteer, Playwright or axe installed here either (`docs/accessibility.md`
records the same constraint), so the two rules are held by a source audit rather
than a rendering one — `scripts/shell-audit.mjs`, wired in as `make audit-shell`
and part of `make test`.

It asserts that the clipping shell is a containing block, that `.visually-hidden`
is still `position:absolute` (so the first rule stays load-bearing rather than
coincidental), that the 760 px block still hides `.navtext`, and that the nav
buttons therefore still carry `aria-label`/`title`. It was verified to fail: with
`position:relative` removed from `.app` and `aria-label={label}` removed from
`NavButton`, it reports both and exits 1.

It proves **neither** that a page is 800 px tall **nor** that a screen reader
announces "Workbook" — only that the rules those facts rest on are still in the
source. The measurement stays what it was: a browser drive.

## M5 step board

Plan of record: `docs/m5-implementation-plan.md`. Steps 1–8 done. Steps 1–5 were
the Go track, step 6 the browser track, step 7 the authored `plans` course, and
step 8 the exit gate. The two schemas in step 1 are the frozen seam both tracks
code against.

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | The plan seam | shared | done | `contracts/plan.schema.json`, `contracts/perf-contract.schema.json`, README endpoint map — frozen seam |
| 2 | Engine: capture a plan | backend | done | `engine.Explain` over the `Executor` seam; `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` under the query's own limits; node/byte caps; `timing_precision` |
| 3 | Gateway: `POST /explain` + the `plan` capability block | backend | done | anonymous analyze refused; `ENVIRONMENT_UNAVAILABLE` with no database; OpenAPI + `querycraft_plans_total` |
| 4 | The performance dataset and its template | backend | done | `perf.sql` (2000/500/10000/20000, `generate_series` arithmetic, no `random()`), sealed `qc_perf_template`, deliberate index gap, status/customer skew |
| 5 | Plan-based evaluation | shared | done | `internal/perf/grade.go` (structural codes only; `TestGradeReadsNoTiming` AST-walks the package), 10 shared fixtures incl. 2 captured from a real plan |
| 6 | Frontend: the plan viewer and the perf exercise | frontend | done | `PlanView.tsx`, `explain` on all three adapters, mock refuses honestly, 82 additive CSS lines |
| 7 | The `plans` course, authored | content | done | `p1..p8.md` (757 lines) + `PLANS_MODULES`; the course left preview and is enrolled |
| 8 | Exit-gate audit and release | shared | done | `make audit-perf` — all four clauses green (below); tag waits on the owner |

### M5 evidence

`make audit-perf` (`scripts/perf-audit.mjs`) boots the real binary on its own
embedded PostgreSQL and asserts the four gate clauses against it, validating
every bundle it sees against `contracts/plan.schema.json`:

| Clause | Measured |
| --- | --- |
| Plans are reproducible on pinned fixtures | the same analytic statement planned three times produces **one** structural plan (node types, relations, indexes, row estimates); the excluded fields — `actual_time_ms`, `duration_ms`, `loops`, `buffers_read` — are printed with a reason each, and the observed durations (`23.4, 21, 21` ms) are reported and never compared |
| Results report engine version, scale and indexes | `environment.engine_version` = `18.3`, `scale_factor` = `10000` (equal to the rows actually in `orders`), per-relation row counts, the index list, and the ten planner settings that change a plan |
| Results do not overstate timing precision | the capability advertises exactly `analyze`/`estimate`/`none`; an `estimate` bundle carries no per-node timing anywhere; an `analyze` bundle carries at least one; a `none` bundle carries no plan and no numbers |
| A plan that cannot be produced honestly is refused | an anonymous `ANALYZE` is refused `ENVIRONMENT_UNAVAILABLE` naming the missing session; a broken statement is a failed bundle with a code from the shared taxonomy; a SQLite exercise is refused with the dialect the lab *can* plan; with `QC_NO_LOCAL_DB=1` the capability says unavailable with a reason and the route invents nothing |

`make audit-dataset-map` and `audit-perf` are both inside `make test` now.

### Defects the M5 integration pass found (all fixed)

Six parallel tracks produced six pieces that each passed their own tests and did
not compose. These are the seams, and they are the reason the integration pass
exists rather than a `git merge`.

| Defect | What it would have done | Fix |
| --- | --- | --- |
| `environment.scale_factor` was hard-coded to `1` in `httpapi/plan.go` | a plan over 10 000 orders reported its scale as 1 — the exact overstatement the exit gate forbids, in a field the gate names | `dataset.FactRowCount(id)` (declared per dataset) + `TestScaleFactorMatchesTheSeed`, which counts the rows in both datasets and fails if the declaration drifts |
| `courseForModule` in `web/src/lib/contentSeed.ts` had no case for the `p` prefix | the whole `plans` course's markdown would have been seeded onto `sql-analytics`, so the new lessons rendered the wrong course's content | a prefix table with `p: 'plans'`, plus a content-audit check that every published module maps back to its own course (the old check asserted four hand-written pairs and could not notice a fifth) |
| `lesson-audit.mjs` and `course-audit.mjs` hard-coded their course lists | the `plans` course was never audited: 8 modules and every authored solution unrun, while the audits reported green | both derive their list from `c.COURSES`; lesson coverage went 23 → 31 with 0 problems |
| the audits read a bundle `make` builds into `/tmp` | running a script directly audits the previous revision and passes — which is what happened twice in this session before the bundles were rebuilt | `scripts/lib/bundle.mjs` `assertFreshBundle` compares mtimes and fails with the make target to run; verified by touching `curriculum.ts` and watching it exit 1 |
| the TS grader compared `bundle.environment.engine` while the Go grader compared top-level `bundle.engine` | the browser and the gateway would disagree about `PERF_DIALECT_MISMATCH` for the same bundle | the TS grader now reads the top-level field, matching `internal/perf/grade.go` |
| `make audit-perf` / `make audit-dataset-map` had no targets | the two new exit gates were not in `make test`, so they could not fail a build | both targets added and wired into `make test` |

### Defects found by driving the M5 course (round two)

Every one of these was found by running the app's own course against the
fixture it describes, not by reading code. The `plans` course is the first
course whose lessons state *numbers*, which is what made a whole class of stale
claim visible.

| Defect | What it did | Fix and evidence |
| --- | --- | --- |
| `perf.sql` was authored against an imagined dataset | the seed used `delivered`/`pending`/`refunded` while every authored lesson and result row said `shipped` (9000) / `completed` (400) / `cancelled` (300) / `pending` (250) / `refunded` (50): 11 of 16 authored result rows were false, and `LIKE 'Aurora%'` matched 25 rows where the lesson said 1 | the fixture was rewritten to the taught facts — five status values at exactly that distribution, customer skew 490/430/400/360/340/320/184/156/132/108 then 152 for customers 11–50 and 4 each for 51–300, `customer_id` and `status` seeded from two *independent* permutations so a customer's statuses are a real mix, `ordered_at` strictly increasing, exactly two lines per order, and the verbatim commerce catalogue for ids 1–20. Real captures now pin it: `TestPerfDatasetCarriesTheTaughtFacts`, and `make audit-perf-content` |
| the fixture was too narrow for its own lessons | at ~100 pages for 10 000 rows a 0.5% predicate is genuinely cheaper to read sequentially, so `ex.p4.1` ("the plan must go through `orders_status_idx`") asserted a decision the planner is *right* to refuse | five realistic columns (`carrier`, `tracking_code`, `shipping_address`, `billing_address`, a ~1.3 kB `notes`) make the tuple realistically wide: `pg_relation_size('dataset.orders')` = 20 480 000 bytes = 2500 pages, and `WHERE status = 'refunded'` now plans as `Index Scan on orders_status_idx` (measured in PGlite 18.6) |
| **both engines dropped every bitmap access** | PostgreSQL prints no `Relation Name` on a `Bitmap Index Scan` — the relation is on the `Bitmap Heap Scan` above it — so `summary.index_scans` and `summary.relations` silently lost it. `ex.p8.1`, whose plan performs two index accesses, was graded as having one: `PERF_NODE_MISSING` + `PERF_INDEX_NOT_USED` on a correct solution | `summarizePlan` in `web/src/api/planContract.ts` **and** `backend/internal/engine/explain.go` now reads a relationless node back against its parent's relation (identical walks, one comment each). Pinned three ways: `TestExplainCountsABitmapIndexScanAsAnIndexAccess`, a real captured fixture `contracts/fixtures/perf-bitmap-index-scan.json`, and a new re-derive clause in the conformance audit |
| **the two graders disagreed at the threshold** | `forbid_nodes.above_plan_rows` was exclusive (`>`) in Go and inclusive (`>=`) in TypeScript, so the browser failed a plan the server passed — the same bundle, two verdicts | `planContract.ts` now uses `>`, matching `backend/internal/perf/grade.go`'s explicit comment. The existing fixture `perf-forbid-at-threshold-is-legal.json` is the pin: the Go test passed it while the web grader failed it, which is how the divergence was found |
| five hand-authored fixtures carried summaries no walk could produce | their `plan` nodes never named a relation the stored `summary` claimed, so the fixtures pinned numbers the app never emits — a fixture that proves nothing | `scripts/perf-fixture-normalize.mjs` (`make audit-perf-fixtures`) repairs the *plan* only when the walk cannot reach the claimed relation, so a captured plan stays verbatim, then re-derives `summary`. The web conformance audit now also re-derives every fixture's summary from its own plan and re-grades it by the pinned verdict |
| `ex.p7.1` taught something untrue on this fixture | the lesson said the cast hides the index, but with `ORDER BY id LIMIT 5` the planner walked `orders_pkey` and stopped early — cast or no cast, the plan was `Limit → Index Scan on orders_pkey` and `orders_customer_id_idx` was never used | re-authored to `ORDER BY ordered_at`, which nothing indexes: the fix now genuinely needs `orders_customer_id_idx` (`Limit → Sort → Bitmap Heap Scan`), and the cast genuinely forces `Limit → Sort → Seq Scan`. The exercise discriminates instead of merely being unfailable |
| `ex.p8.1`'s contract over-specified the planner's choice | `require_nodes` demanded a literal `Index Scan` on the inner side of a join, but for two rows the planner legitimately answers with a bitmap on one engine and a plain index scan on another, so the exercise passed or failed by machine | the node requirement is gone; `require_indexes: ['orders_pkey','order_items_order_id_idx']` + `min_index_scans: 2` say the same thing in an engine-independent way (and now count bitmaps) |
| the perf-content audit could not see any of this | its timestamp comparison formatted a `Date` with `String(...)` (a locale string) against the authored `'YYYY-MM-DD HH:mm'`, so no authored timestamp row could ever match; and it read `bundle.dataset` from the bundle root, where the field does not exist — every exercise reported "planned against the small one" while `scale_factor` was 10000 | `wallClock()` formats a `Date` in the same local time the driver parsed it in, `jsValue` writes that form back, and the dataset check reads `bundle.environment.dataset`. `make audit-perf-content` is now green: 16/16 authored rows, 8 prose claims, 4/4 contracts, 4/4 dataset routings |
| **the content-truth rule existed twice and the copies had drifted** | `perf-content-audit` compared an authored timestamp to the engine's by the minute; the new `perf-seam-audit` compared `'2026-12-28 01:48'` to the engine's `'2026-12-28 01:48:00'` strictly and reported three *false* failures (`ex.p3.1`, `ex.p7.1`, `ex.p8.1`) — the kind of red that teaches you to ignore the gate | one copy, `scripts/lib/rowtruth.mjs`, imported by both audits. The distinction it documents is the point: the **learner-facing** comparator (`compare.ts` / `compare.go`, design §6.1 rule 7, "no implicit text normalization") must stay strict and is never asked to make `10:12` equal `10:12:00`, because a learner is graded against a reference the *same* engine produced; the **content-truth** comparator asks whether the lesson's claim is true of the fixture, and the engine's text for an instant is dialect-shaped (PostgreSQL prints seconds, SQLite's ISO-8601 text does not — `docs/sqlite-dialect.md` §5). Evidence: `make audit-perf-content` and `make audit-perf-seam` both green on the same rows |

Four new gates came out of this pass, all inside `make test`:
`make audit-perf-content` (the course against the fixture it describes — every
authored row, every number the *prose* states, every perf contract against its
own solution), `make audit-perf-fixtures` (every fixture's summary is the walk of
its own plan), the re-derive clause inside `make audit-perf` (the same document,
both languages, one verdict), and `make audit-perf-seam` (the dataset seam,
driven through the shipping browser adapter — see step 9 below).

### The dataset-routing defect: fixed and verified (M5 step 9)

**Was:** `/explain` routed a perf exercise to the 10 000-row perf fixture, but
`/query` and `/answers/evaluate` always ran against **commerce** (80 orders
anonymously, the learner's own commerce database when signed in). So the `plans`
course was unpassable *in the product* while being provably correct in the
audit: a learner submitting any `plans` exercise was graded against rows from a
different fixture.

**Fixed, two tracks that met in the middle.** A request now carries a `dataset`,
and one resolver decides what it means.

* backend (`Bohr`, complete) — `internal/httpapi/dataset.go` is the only reader
  of the dimension: `resolveDataset(explicit, exercise)` walks explicit →
  `exercise.perf` → commerce, and canonicalises `""`/`"commerce"` to the same
  value so an old client cannot mean two things. `statementExecutorFor` in
  `dialect.go` resolves dialect **and** dataset for one statement; `/query`,
  `/answers/evaluate` and `/explain` share one `statementRequest{Dialect,Dataset}`
  shape, and `plan.go` calls the same resolver rather than keeping its own copy.
  `POST /environment/reset?dataset=` and the tables response are dataset-aware
  too. Pins: `dataset_test.go` (an 11-case resolution table, the two anonymous
  refusals, and a real `pgtest` run proving perf = 10 000 rows while commerce =
  80), `TestTablesServesBothDatasets`, `TestEnvironmentResetServesBothDatasets`.
* frontend (mine, after the `Turing` subagent failed silently) — `web/src/lib/dataset.ts`
  is the only reader on this side (`datasetFor`/`datasetReason`, so every result
  can say *why* it ran where it did); `web/src/api/pglite.ts` holds a registry of
  engines, one per dataset, perf seeded lazily from `perf.sql`; `execution.ts`
  dispatches the dataset on all three adapters and sends it on all three HTTP
  requests. The **mock refuses perf** (`ENVIRONMENT_UNAVAILABLE`) rather than
  fabricating perf rows, which is the seam's oldest rule.

**Verified end to end, in the browser, as a learner:**

* `#/course/plans/p4` exercise `ex.p4.1` → `REFUNDED 50`, and Analyze graded
  **4/4 green** (`Index Only Scan` on `orders_status_idx`, estimate 50, 2 nodes).
* `#/course/plans/p5` join Run → 490/430/400 orders in 4.5 s.
* the tables drawer on `plans` reports `perf · dataset_version 1` with
  `customers 2000 / orders 10000 / order_items 20000 / products 500`.
* `make audit-perf-seam` (new, below) drives the same seam headlessly.

**The browser-only defect this section exists for.** PGlite's `PGliteWorker`
derives its cross-tab leader-election channel from the worker URL when no `id` is
given, so the second dataset's worker joined the *first* one's election. The
symptom was `ENVIRONMENT_UNAVAILABLE / "The in-browser database could not start."`
with `Leader changed, pending operation in indeterminate state`; the danger was
worse than the symptom — had the other worker won the election, perf SQL would
have run against the commerce tables. Every unit test passed; only driving the
app found it. Fix: an explicit per-dataset `WORKER_ID`
(`querycraft-dataset-commerce` / `-perf`) in `web/src/api/pglite.ts`, so two
engines are two engines.

**One more, found while driving it:** the first `Run` against perf took ~38 s
(WASM boot + 10 000-row seed + `ANALYZE`; the seed alone is ~930 ms headlessly).
`ExecutionService.warmUp(dataset)` now starts a course's engine when the course
opens, so the cost lands on the chapter, not on the first keystroke.

**The gate:** `scripts/perf-seam-audit.mjs` (`make audit-perf-seam`, inside
`make test`) bundles the shipping adapter and drives the real seam: dataset
resolution per course and exercise, perf = 10 000 vs commerce = 80 from
`executeSql`, the drawer's row counts on both, every authored perf row against
the fixture it names, every perf plan graded green with
`environment.dataset === 'perf'`, the *same* SQL planning differently on the two
fixtures, and a perf write + reset that leaves commerce untouched.
`scripts/content-audit.mjs` also asserts that an exercise carrying a perf
contract lives in a course whose `dataset` is `perf`, which is what would have
caught this class of mistake at author time.
## M6 step board

Plan of record: `docs/m6-implementation-plan.md`. Steps 2–3 are in flight as one
additive track (`Helmholtz`): the dialect profile, the SQLite dataset and its
checked mapping. Nothing existing changes behaviour.

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | Dialect in the contracts | shared | done | `contracts/query-result.schema.json` gained the required `dialect` and the ninth code `DIALECT_MISMATCH` (closed enum); `exercise.schema.json` gained `setup.dialect`; every course carries `dialect: 'postgresql'`; `web/src/lib/dialect.ts` is the only reader; `make audit-sqlite-errors` in `test` |
| 2 | SQLite dataset + checked mapping | shared | done | `schema.sqlite.sql`, `seed.sqlite.sql`, `mapping.json` (22 rows, 5 named mismatches each with a reason); `make audit-dataset-map` green |
| 3 | Dialect profile: errors + value encoding | shared | done | `contracts/dialects/sqlite.json` (30 ordered message rules, affinity, 28 capabilities) mirrored into `internal/dialect` with a drift test; `docs/sqlite-dialect.md` |
| 4 | Gateway SQLite adapter | backend | done | cgo-free `SQLiteExecutor` over `modernc.org/sqlite` (+15 tests: an unbounded recursive CTE under a 300 ms deadline returns `QUERY_TIMEOUT` at 301 ms and the driver genuinely stops); per-account files + a shared read-only `commerce.db`; `engine.ClassifyError` on the executor seam so a SQLite syntax error is `QUERY_SYNTAX_ERROR`; optional `dialect` on `/query`/`/answers/evaluate`/`/explain`; `DIALECT_MISMATCH` names the served dialects; `/health` reports the real engine; `make check-cgo` proves the binary stays cgo-free |
| 5 | Per-learner SQLite isolation (D14) | backend | done | ADR `0006-sqlite-learner-isolation.md` — one file per account, reset by re-copy; file-per-account measured live (signed-in `CREATE TABLE` lands in the account file and a second account's identical query is `QUERY_RUNTIME_ERROR: no such table`; the anonymous shared file stays at 30 rows) |
| 6 | Browser SQLite adapter (D12) | frontend | done | ADR `0007-browser-sqlite-engine.md`; `@sqlite.org/sqlite-wasm` 3.53.4 in a Worker (`sqlite.ts` → `sqlite.worker.ts` → `sqliteEngine.ts`), the profile read from `contracts/dialects/sqlite.json`, driver framing stripped in `sqliteFraming.ts`; `make audit-sqlite-errors` green |
| 7 | Dialect badge + actionable refusal | frontend | done | `lib/dialectChoice.ts` (the learner's local override, keyed by module id) + the chapter badge (`.dialbadge` with `data-dialect`/`data-dialect-state`/`data-engine`) and the one-action switch in `SqlCell`; `Schema`/`TablesDrawer` take the dialect through to the seam |
| 8 | Per-dialect parity audit | shared | done | `--dialect postgresql|sqlite` in `scripts/adapter-parity-audit.mjs`; `make audit-parity` runs both and fails if either is red. PostgreSQL 119/119 match; **SQLite 49/49 match, 0 diverge**, 8 exclusions each printed with its reason and verified (rowid, file-layer pragmas, materialised dates). `docs/adapter-parity.md` §"The SQLite dialect" |
| 9 | M6 release | done (tag pending the owner) | `make test` green (21 audit targets, incl. `audit-courses`); `make verify` green — SPA + API + auth + both dialects on one binary, `verify: OK (embedded PostgreSQL :54340, SPA + API + auth on :18080)`; `make verify-mock` green — `OK (no database, honest degradation, auth still enforced)` |

### M6 evidence (measured)

**The browser SQLite engine, driven through the shipping seam** (`preview_evaluate`
against `npm run dev` on :5199, the real `web/src/api/sqlite.ts`):

| Probe | Measured |
| --- | --- |
| boot | 111 ms → `SQLite 3.53.4` |
| a grouped lesson query | 5 rows, columns `{status,TEXT}` and `{orders,expression}` — the declared type verbatim, the profile's own word for an expression |
| syntax errors | `incomplete input`, `near "SELEC": syntax error`, `unrecognized token: "'abc"` all → `QUERY_SYNTAX_ERROR` (this is what the frame-strip exists for: without it every one of them is a runtime error) |
| runtime errors | `no such table: nope`, `UNIQUE constraint failed: products.id` → `QUERY_RUNTIME_ERROR` |
| the deadline | `QUERY_TIMEOUT: interrupted` at the wall clock, and the connection **survives** it (80 rows the next statement) |
| a primary key column | labeled from `sqlite3_column_decltype`, `INTEGER` — not `expression` |

**The lesson loop in a real browser, as a user** (`#/course/sqlite-essentials/s3`):
the worked example ran on the wasm engine (`1 row · SQLite 3.53.4`, `MAX_ID 20 / MAX_ROWID 20 / ROWS 20`), a
correct submission to `s3.1` was **Passed** with the contract breakdown
(columns and order ✓, row count ✓, row values ✓, row order ✓), and a deliberately
wrong submission was **Not yet — row count** with `ROW_COUNT_MISMATCH` and the
recovery controls (hint, solution, accept, report). A fresh load of
`#/course/sqlite-essentials/s3` lands on chapter 3 — the deep link is honoured
on the first render.

**Mermaid and markdown in chapter content**: `.qc-mermaid` holds a rendered
683×615 SVG on `s3` (the diagram is authored as a ` ```mermaid ` fence in
`web/src/data/content/s3.md`, seeded into `content_docs`, read back by the lesson
page).

**Local accounts and per-user scoping, against the real binary** (built from
this tree, its own embedded PostgreSQL 18.3 and its own SQLite root, 17/17 checks):

| Check | Measured |
| --- | --- |
| the seeded admin | `admin@querycraft.local` + empty password → HTTP 200, `role: admin` |
| signup | two learners → HTTP 201, `role: learner`; `GET /auth/me` names its own account |
| progress is owner-scoped | A's `PUT /progress/joins` is invisible to B (`snapshot: null`) and read back by A |
| workspaces are owner-scoped | A's `CREATE TABLE` succeeds; B's `SELECT` from it is `QUERY_RUNTIME_ERROR: no such table` |
| anonymous | `exists:false, writable:false`; reads the shared dataset; a write is refused |
| the gateway's SQLite | `SELECT COUNT(*) FROM customers` → `SQLite 3.50.3`, 30 rows, `dialect: "sqlite"` |

**A real model, end to end** (`make audit-ai-live`, `deepseek-v4.1-flash` over an
OpenAI-compatible endpoint — set `QC_AI_BASE_URL`, `QC_AI_MODEL`, `QC_AI_KEY_FILE`,
and `QC_GO` if `go` is not on PATH): HTTP 201 `status=active` in 38.2 s on
2026-10-15 — the model's bundle passed `validate_artifact`, its 4 setup statements
applied to a scratch database, its reference returned 7 rows, the cross-check
agreed on 7, 3/3 adversarial fixtures were rejected, two repairs were requested,
and the activated artifact is graded by the real evaluator (reference passes,
wrong query does not). Provenance attributes the model, not the transport.

| Live run (`deepseek-v4.1-flash`) | Before | After |
| --- | --- | --- |
| outcome | `AI_REPAIR_EXHAUSTED`, twice, for two different reasons | `status=active`, first attempt, **0 repairs** |
| wall clock to a decision | 21.2 s / 42.0 s to a rejection | 6.7 s to activation |
| setup statements | `[""]`, then `CREATE TABLE customers`, then `DROP` | `[]` — the correct answer over the sealed dataset |
| what the model was told | "the commerce dataset", no columns | every table, every column and its type, from `dataset.Tables` |
| repeatability | 1 active, 2 rejected in the three runs that day | **4 consecutive runs all `active`** (6.7 s, 16.1 s, 8.5 s, 5.4 s; 3 of them with no repair at all) |

Both prompt defects are now gated from `backend/internal/ai/prompt_test.go`
(`go test ./internal/ai/...`): the brief must name every table and every column
the dataset package reports — so a column added to the seeder is required in the
prompt by the same commit — and it must say that a setup list may be empty and
that the loaded tables are not to be created, altered or dropped.

**Why the live run used to be a coin flip, and what was wrong.** Two runs on the
same day failed with `AI_REPAIR_EXHAUSTED` for two different, both self-inflicted
reasons, and neither could be blamed on the model picking bad SQL:

1. the advertised schema allowed an empty statement. `setup.statements` was
   `{"type":"array","items":{"type":"string"}}`, so `[""]` satisfies it; the
   bundle is refused by `validateArtifact` only after the repair budget has been
   spent, and the repair gets the same schema back. Every content string in
   `challengeSchema` now carries `minLength: 1` (and `objectives` /
   `result_contract.columns` `minItems: 1`, matching what `validateArtifact`
   already required). `setup.statements` carries `minLength: 1` on its items but
   **no** `minItems` — an empty list is the correct answer over the sealed
   dataset, and demanding one entry pushed the model into inventing
   `CREATE TABLE customers`.
2. the prompt never said what the environment already contains. The model was
   told to "design one exercise over the commerce dataset" and nothing else, so
   it wrote setup DDL for tables that are already loaded: one run failed
   `validate_artifact` with `"DROP" may not start a setup statement`, the next
   failed `scratch_environment` with `relation "customers" already exists`. The
   request now names the four loaded tables and states the two shapes
   `validateSetupStatement` accepts, so the model is not guessing at a rule the
   gate then applies.

Found while reading the failure text itself: the repair context said
`setup.statements: statement 0: setup.statements: the statement is empty`, with
the field named twice — the same doubled prefix would have reached the model on
any setup repair. It is trimmed once, in the caller, the way
`cross_check_solution` already did.

### M6 step 8 notes

**A verification hole found and closed while finishing M6.** `make audit-courses`
(the exercise-solution audit) POSTed `{sql}` and nothing else, so every statement
was run as PostgreSQL over the commerce dataset and the script never set an exit
code. Once SQLite chapters existed that meant 16 exercises reported `BROKEN` for
a reason that had nothing to do with their content, 5 more reported `ok` only
because their SQL was *also* valid PostgreSQL (a false pass), every `plans`
exercise was graded against the wrong fixture, and the whole thing exited 0 — it
could not fail a build. It now resolves the dialect and dataset through the app's
own `dialectFor`/`datasetFor`, grades as a signed-in learner (the perf fixture
lives in a per-account environment), asserts the engine that answered is the
dialect that was asked for, and is a `Reporter` inside `make test`. 53/53
solutions run and pass their own contract.

**A `DATE`-declared column used to diverge between the two SQLite adapters. It is
fixed, and the exclusion is gone.** A `DATE`-/`TIMESTAMP`-declared column was the
one value the two adapters spelled differently: the wasm driver reports the
stored text (`2025-11-02`), while `modernc.org/sqlite` materialised the column
into a `time.Time` before the executor saw it and the profile's `time` rule
rendered that as RFC3339 UTC (`2025-11-02T00:00:00Z`). `contracts/dialects/sqlite.json`
says the stored text is correct, so the **gateway** was the side that had to
change — and it could not be done through the driver: modernc v1.38.2 has no DSN
knob for the read-side conversion (`_time_format` is write-side only) and its
connection type is unexported, so nothing downstream can see the column.

So the gateway now reads SQLite rows through the raw `sqlite3` API
(`backend/internal/engine/sqlite_raw.go`): a `database/sql/driver` over
`modernc.org/sqlite/lib`, the same cgo-free engine, reading each column in its
storage class (`sqlite3_column_type` → `sqlite3_column_int64/double/text/blob`)
with `sqlite3_column_decltype` supplying the label. `database/sql` still owns the
pool; only what a row value *is* changed. Three mechanics were measured rather
than assumed and are recorded in the file's own comments: `sqlite3_prepare_v2`
out-parameters have to live in C memory (a Go stack slot is lost when the
goroutine stack moves), a deadline stops a statement through `sqlite3_interrupt`
armed for its whole life, and the cursor's own context check ends a statement
that never reaches a step.

The parity audit now compares the two probes as ordinary statements. `make audit-parity`:
**51 statements compared, 51 match, 0 diverge, 6 excluded** — the two remaining
exclusion kinds are `rowid` (2) and `storage-layer-pragma` (4), both being
properties of a *file* rather than values from the dataset, and both verified.
`make check-cgo` stays green: no cgo, no Docker.

## M4 step board

Plan of record: `docs/m4-implementation-plan.md`. All eight steps done. The
provider decision is ADR `0005` (provider-neutral interface; deterministic fake
default), not assumed.

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | The AI generation seam | shared | done | `contracts/ai.schema.json`, `backend/internal/ai` (Provider/Depot/Runner/Limits/Service), `web/src/api/aiContract.ts`; commit `7c951de11` |
| 2 | Provider: deterministic fake + OpenAI-compatible transport | shared | done | `fake.go` (offline, deterministic), `http_provider.go` (thin `/chat/completions`, server-side key); ADR `0005` |
| 3 | The §11.1 pipeline | backend | done | nine stages in the declared order; `Limits` enforced; provenance + attempts; `AI_REPAIR_EXHAUSTED` |
| 4 | HTTP routes + the `ai` capability block | backend | done | `GET /challenges`, `GET /challenges/{id}`, `POST /challenges/{id}/reset`, `POST /tutor`, `…/critique`, `…/report`, `…/patches/{patchID}`; `ai` on `/capabilities`; OpenAPI |
| 5 | Persistence: survive restart, reset on demand | backend | done | `Depot` over `app.challenges`/`app.patches`; the §6.5 defect → verify → propose → approve flow; reset deletes the challenge, not the attempts |
| 6 | Frontend seam + the lab UI | frontend | done | stages rendered from the server; generation disabled with a reason when AI is off; explain-on-request never shows the reference solution |
| 7 | AI-off proof + exit-gate audit | shared | done | `make audit-ai` — all six gate clauses green; `make audit-ai-adapters` inside `make test` |
| 8 | M4 release | shared | done | `make verify` green; **live-provider audit green** (see "Live-provider run") |

## M3 step board

## Live-provider run (real model, end to end)

The M4 exit gate proves the pipeline with a deterministic fake. That is not
evidence the *transport* works, so `scripts/ai-live-audit.mjs` (`make audit-ai-live`)
drives the same pipeline with a hosted OpenAI-compatible model
(`amr-link.open-design.ai`, `deepseek-v4.1-flash`). It asserts the pipeline's
honesty, not a happy path.

**Run 1 — failed, and it found three real defects.** The run stopped at
`request_bundle` after 20.0 s with `AI_UNAVAILABLE`, which the audit correctly
reported as an honest refusal. It was not: it was three bugs in the product.

| Defect | What it caused | Fix |
| --- | --- | --- |
| The pipeline's own run budget defaulted to 20 s (`ai.DefaultLimits`) while a single provider call was allowed 45–120 s | the run was cancelled mid-call, so a working model looked unreachable | `DefaultLimits.Timeout` → 120 s; `aiLimitsFromEnv()` derives the run budget from the call timeout × (1 + `MaxRepairs`) and reads `QC_AI_RUN_TIMEOUT_MS` |
| The server's `WriteTimeout` was 30 s | a generation that took longer had its connection closed before the pipeline could answer, turning a stage report into a transport error | `WriteTimeout` → 5 min with the reason recorded inline; read timeouts unchanged |
| `HTTPProvider.userContent` dropped `ProviderRequest.Previous` and `Failure` | the repair pass re-asked the identical question, so the model returned the identical bundle and the budget was exhausted | the repair context is now sent, bounded (16 000 chars for the previous reply, 2 000 for the failure) and clipped on a rune boundary; `TestHTTPProviderCarriesTheRepairContext` and `TestUserContentClipsTheRepairContext` pin it |

**Run 2 — green, the model activated a challenge.** `HTTP 201 status=active`,
every stage `done`, and the run genuinely exercised the loop:

| Stage | Result |
| --- | --- |
| `validate_request` | `joins, aggregation (beginner, 1 exercises)` |
| `request_bundle` | candidate bundle received (18 s) |
| `validate_artifact` | `3 hints, 0 setup statement(s)` |
| `scratch_environment` | `0 setup statement(s) applied; dataset "commerce" readable` |
| `reference_contract` | reference returned 7 row(s) |
| `cross_check` | cross-check agreed on 7 row(s) |
| `adversarial_fixtures` | `3/3 wrong queries were rejected` |
| `repair` | `attempt 2 requested after reference_contract failed` — a real repair, and it worked |
| `activate` | stored as active |

Evidence recorded by the run: `{"reference_rows":7,"cross_check_rows":7,"fixtures_rejected":3,"fixtures_total":3,"repairs":2,"scratch_database":"ai_97c99c0ec65ed825"}`.
The activated artifact is then graded by the real evaluator (reference passes its
own contract, a wrong query does not), and its provenance attributes the model
rather than the transport default.

| # | Step | State | Notes |
| --- | --- | --- | --- |
| 1 | Metrics + diagnostics view | done | `GET /metrics`, `GET /diagnostics`, `make audit-metrics` (hermetic + live); Go tests |
| 2 | Structured request logs completed | done | one `withObservability` middleware: operation, route, duration_ms, status, error_code, request_id; credential redaction |
| 3 | Adversarial suite (§13.3) | done | `make audit-adversarial` (10 cases, green) + `docs/adversarial-suite.md` |
| 4 | Load test (§13.2) | done | `make audit-load` (320 requests, green) + `docs/load-test.md`, recorded numbers |
| 5 | Template environments (D11) | done | spike `dataset/template_test.go` (measured), ADR `docs/adr/0002-challenge-environments.md` |
| 6 | Exit-gate proof | done | 6.1 adapter parity **green: 61 compared / 61 match / 0 diverge** (`make audit-parity`, now inside `make test`; `docs/adapter-parity.md`); 6.2 no-admin-path Go test green; 6.3 clean-machine setup green |
| 7 | Writable learner workspaces (owner request) | done | ADR `0003`; verified end-to-end against the real binary (see "Step 7 notes") |
| 8 | M3 release | done | tagged `v0.3.0` (owner go-ahead given) |

### Step 1 checklist

| Item | State | Where |
| --- | --- | --- |
| Dependency-free metrics registry | done | `backend/internal/metrics/metrics.go` (+ `metrics_test.go`, green with `-race`) |
| `engine.Executor` gains `Stats()` / `Ready()` + `Diagnostician` | done | `engine/engine.go`, `pgx.go`, `unavailable.go` |
| Store reports `Kind()` (postgres vs memory) | done | `store/store.go`, `memory.go`, `postgres.go` |
| Metrics + logs middleware (single recorder) | done | `httpapi/metrics.go` `withObservability` |
| `GET /metrics` (Prometheus text) | done | declared names, pool/ready gauges sampled per scrape |
| `GET /diagnostics` (readiness, versions, pool, sandbox) | done | learner facts read over the restricted connection |
| Enforce `MaxConcurrent` inside the deadline | done | semaphore in `enter`; `QUERY_TIMEOUT` on saturation |
| OpenAPI doc + `openAPIOperations` cover the new routes | done | `openapi.go`, `openapi_test.go` |
| `scripts/metrics-audit.mjs` + `make audit-metrics` | done | in `make test`; `make audit-metrics-live` for the live half |
| Fakes updated for the wider `Executor` interface | done | `engine_test.go`, `eval_test.go`, `server_test.go` |
| Credential redaction (`engine.RedactSecrets`) | done | `engine/redact.go`; applied in `NewError`, `Ready`, diagnostics |

## Verified green (last full run)

- `make test`: frontend typecheck + 9 audit suites + all Go packages — **green**
  (includes `audit-parity`: 61 compared / 61 match / 0 diverge / 1 volatile exclusion).
- `make verify`: build + `make test` + a cold-boot smoke test on an isolated embedded
  PostgreSQL — `/health` → PostgreSQL 18.3, `/query` 200, anonymous `/progress` 401,
  admin login 200, learner signup 201, scoped progress PUT/GET 200, revoked token 401,
  SPA served — **green**.
- `make verify-mock`: honest degraded mode (`/query` → 400 `ENVIRONMENT_UNAVAILABLE`) — green.
- Owner-driven UI pass against the built binary (real PostgreSQL, gateway mode): sign-in
  screen in gateway mode (not the offline "Local workspace"), header `gateway · live`,
  lesson query ran against the engine (`… postgresql`, request id), exercise graded
  (wrong → "Not yet", correct → "passed" + Next chapter), progress survived a reload,
  tables drawer listed the live schema and reset the workspace (222 ms), Workbook /
  Progress / Challenge lab / Library / Appearance / Create-a-course all rendered.

### Step 1–2 notes

- The metrics registry is deliberately dependency-free (`backend/internal/metrics/`);
  a client library would be more code than the thing it measures.
- `MaxConcurrent` had been advertised in `/capabilities` and never enforced. It now is:
  a semaphore acquired inside the request's own deadline. Waiting spends the same budget
  the query would have, so saturation reports `QUERY_TIMEOUT` — an existing code in the
  closed contract enum — rather than inventing one or queueing unboundedly.
- Two middleware layers (logging and metrics) would each hold their own status and error
  code; they were merged into one so the log line and the counter cannot disagree.

### Step 6 notes (exit-gate proof)

- **6.2 No path runs learner SQL on an administrative connection — proven by behaviour.**
  `backend/internal/httpapi/adminpath_test.go` boots its own embedded cluster, builds a
  real executor on the *learner* DSN, and drives `POST /query`: `current_user` and
  `session_user` both answer `querycraft_learner`; `app.progress`, `public.progress`,
  `pg_read_file`, `pg_ls_dir`, `COPY … TO PROGRAM`, `CREATE ROLE`, `CREATE DATABASE`,
  `ALTER SYSTEM`, `DROP TABLE dataset.orders`, `TRUNCATE`, `SET ROLE` and `pg_authid`
  are all refused with `QUERY_RUNTIME_ERROR` (HTTP 400, never 5xx); the dataset counts and
  the admin-side sentinel are unchanged afterwards. `RESET ROLE` is a documented no-op on
  a learner session (it resets the *current* role to the session user, which is already the
  learner) and the test asserts that instead of a fake error.
- **6.3 Setup works on a clean machine from the documented command — measured.**
  The built binary was started with an empty data directory *and* an empty binaries cache:
  it downloaded PostgreSQL 18.3 once (60 MiB), ran `initdb`, applied schema + seed and
  served `/health` (`"version":"PostgreSQL 18.3"`), `POST /query`
  (`{"status":"succeeded",…"rows":[[80]],…}`), `/` (SPA, 200) and `/docs` (200). The
  documented flow is recorded in `README.md` §"Verified from nothing on a clean machine".
- **6.1 Adapter parity** is `scripts/adapter-parity-audit.mjs`, extended into a real
  instrument: it reports *which* side produced *what* for each mismatch, excludes the
  genuinely volatile statements (`EXPLAIN`, `EXPLAIN ANALYZE`, `random()`, `now()`,
  `pg_sleep`) with a printed reason each unless `--include-volatile` asks for them, and
  runs the naive-`timestamp` case under both a UTC and a non-UTC `TZ` so the host-zone
  shift can be watched appear and disappear.
- **Three real divergences the audit found, all now fixed in the product, not the test.**
  (a) The gateway emitted `numeric` as a JSON number, so `118.00` arrived as `118` —
  the learner's own price lost its scale; `internal/engine/normalize.go` now returns
  PostgreSQL's text for it. (b) A naive `timestamp` was decoded into a time value and
  re-rendered, which moves the wall-clock time by the host's UTC offset (measured:
  `10:30` → `05:00` under `Asia/Calcutta`); the same text rule fixes it, and the browser
  adapter now asks PGlite for the same text via per-OID parsers instead of letting it
  materialise a JS `Date`. (c) The gateway called a missing relation a *syntax* error
  (`42P01`/`42703` were matched by the `42` class prefix) where design §4.2 files
  "missing relation" under `QUERY_RUNTIME_ERROR` — and the browser adapter already said
  runtime, so the same mistake was described two ways; `classify` now shares one table
  with `web/src/api/pglite.ts`, pinned on both sides by tests. The canonical type-name
  spelling is PostgreSQL's own `pg_type.typname` (`int4`, `int8`, `bool`, `float8` …),
  which is what §4.2's example uses.
- The Go half is proven by `internal/engine/encoding_test.go`, which runs against a real
  server (the default test timezone here is `Asia/Calcutta`, the one that exposed the
  bug) and asserts the exact text for `date`/`time`/`timestamp`/`interval`/`numeric`,
  that `int4`/`float8`/`bool` stay JSON scalars, that `timestamptz` equals the server's
  own `::text`, and that the serialized body contains `"118.00"` and no `2024-01-15T…`.
  The cross-adapter number lands here when the audit is green end to end.

### Step 7 notes (writable learner workspaces)

The owner's objection is correct and is now a milestone: **a SQL course that cannot
write cannot teach SQL.** Both adapters wrapped learner SQL in a read-only transaction
and the learner role held a `SELECT`-only grant, so `INSERT`, `UPDATE`, `DELETE`, DDL and
transactions were untouchable — and the curriculum contains *zero* DML lessons, which is
the symptom rather than the fix.

Settled in `docs/adr/0003-writable-learner-workspaces.md`:

- Learner SQL is writable. Signed-in learners get their **own environment database**,
  copied from the sealed template (ADR 0002) and granted `SELECT, INSERT, UPDATE, DELETE`
  plus `USAGE, CREATE` on `dataset` — no `TRUNCATE`, no ownership, no `REFERENCES`, no
  `TRIGGER`.
- Reset is DROP + copy-from-template (~35 ms create, ~25 ms drop per ADR 0002), exposed as
  `POST /environment/reset`, with `GET /environment` for facts.
- **Anonymous stays read-only** on the shared dataset: no stable identity to hang an
  environment on, and a cookie-keyed environment would make every cookie-less client a
  `CREATE DATABASE`.
- The shared `dataset` schema is never writable in place; the gateway's own database keeps
  the `SELECT`-only grant, so "the learner cannot INSERT" becomes "the learner cannot
  INSERT into the shared dataset, and cannot reach another learner's environment".

Landed: the Go library layer (`dataset` writable template grants, `engine` writable mode
with commit-on-drain, `internal/envdb` per-account manager), the HTTP surface
(`GET /environment`, `POST /environment/reset`, wired into `cmd/api` with a reap loop and
prometheus counters), and the web layer (PGlite writable + `resetEnvironment()`, the
`ExecutionService` seam, and a two-step-confirm reset control in the tables drawer that
cancels on Escape and re-focuses the idle button).

**Verified against a real binary, not just unit tests** (built `./cmd/api`, its own
cluster on `:19011`/`:19012`, 2026-10-14):

| Step | Observed |
| --- | --- |
| anonymous write | `UPDATE products SET stock = 0` → 400 `QUERY_RUNTIME_ERROR` "cannot execute UPDATE in a read-only transaction" |
| anonymous `GET /environment` | `{"ok":true,"environment":{"exists":false,"writable":false}}` — it does not invent a workspace |
| signup → `GET /environment` | lazily created `qc_env_acct_1ed246…`, `exists:true`, `writable:true`, `tables:5`; gateway log `challenge environment ready … created_in=7.0ms` |
| learner write | `UPDATE products SET stock = 0` → `succeeded`; read back `[[1,0],[2,0],[3,0]]` |
| reset | `POST /environment/reset` → `{ok:true, duration_ms:401}`; read back `[[1,40],[2,25],[3,15]]` — the sealed dataset |
| DDL in the workspace | `CREATE TABLE scratch(id int)` → `succeeded`; the same statement anonymously → refused |
| OpenAPI | `/environment` and `/environment/reset` both present |

Both `POST /query` and `POST /answers/evaluate` route through the same
`statementExecutor`, so grading also runs in the learner's own workspace rather than the
shared dataset. Content for DML/DDL/transactions follows once the course is written, so
the lessons actually teach what the engine now allows.

### Step 8 notes (release)

Everything the release needs is green; only the tag waits on the owner's
go-ahead (`docs/m3-implementation-plan.md` step 8 holds the checklist):

| Check | Evidence |
| --- | --- |
| `make verify` (build + typecheck + every audit + all Go tests + cold-boot smoke) | green, `/health` → PostgreSQL 18.3 |
| `make verify-mock` (honest degradation) | green — `/query` 400 `ENVIRONMENT_UNAVAILABLE`, auth still enforced |
| Adapter parity | 61 compared / 61 match / 0 diverge (`docs/adapter-parity.md`) |
| `make audit-load` | green — concurrency cap proven, pool cross-checked against `/metrics` |
| `make audit-metrics-live` | green — deadline stop → `QUERY_TIMEOUT`, row cap → `truncated` at exactly the cap, both counted |
| `make audit-adversarial-live` | green — ten §13.3 cases, dataset intact after the suite |
| `make audit-courses` (live gateway) | green — **every authored solution in all four courses** runs on the gateway and grades against itself |
| `make audit-load-live` | green — concurrency cap enforced on a running gateway, pool bounded, healthy afterwards |
| Owner UI pass on the built binary | gateway mode, real engine queries, grading, retake, drawer + reset, authoring |
| Version bump + tag `v0.3.0` | **done** — `web/package.json` `0.3.0`, tag created with the owner's go-ahead |

### Step 3 notes (adversarial suite)

- `scripts/lib/gateway.mjs` is the shared harness for every live audit: `freePort`,
  `buildGateway`, `startGateway`/`stopGateway` (kill by PID *and* process group, never
  `pkill`), `liveEnv`, `getJSON`/`getText`/`postJSON`, `seriesValue` (subset label match)
  and `Reporter`. `scripts/adversarial-audit.mjs` and `scripts/load-test.mjs` are both
  built on it; there is one way to boot and stop a gateway, not three.
- Three real defects the suite found and that are now fixed: SQLSTATE `42501`
  (permission denied) was reported as `QUERY_SYNTAX_ERROR` and is now
  `QUERY_RUNTIME_ERROR`; a pool-acquire deadline was reported as
  `ENVIRONMENT_UNAVAILABLE` and is now `QUERY_TIMEOUT` with a message naming the wait;
  and hitting the row cap did not cancel the server, so `generate_series(1,1e9)` drained
  for 4003 ms holding a connection — it now cancels at the cap and returns in 4 ms.
- `docs/adversarial-suite.md` publishes the table **and** a "What is not protected"
  section: the learner can enumerate databases via `pg_database`, read role names,
  read operational settings (host paths are hidden), and read `pg_stat_activity`; the
  suite is not a resource-exhaustion proof and does no protocol fuzzing.

### Step 4 notes (load test)

- `scripts/load-test.mjs` builds and boots its own gateway (embedded PostgreSQL on a
  free port), drives N workers against `POST /query` mixing a real three-table
  join/aggregate with a statement that must hit its deadline, and reports the
  client-observed numbers *next to* the gateway's own `/metrics` and `/diagnostics` so
  the two can be compared rather than trusted.
- Recorded run (2026-10-14, i5-1135G7, 8 vCPU, WSL2, PostgreSQL 18.3, 8 workers × 40
  requests, `max_concurrent=4`): 320 requests in 1601.8 ms = **199.8 rps**; overall
  p50 16.3 ms / p95 123.3 ms / p99 130.7 ms; analytic p50 15.2 ms / p99 59.3 ms;
  288×200 and 32×400, every 400 a `QUERY_TIMEOUT`. `requests_total` delta and the
  latency-histogram count delta both equal 320, so the server's tally matches the
  client's.
- The concurrency cap is proven by a deterministic saturation probe, not by "nobody was
  refused": fill every slot with an 800 ms sleep, then two requests with a 120 ms
  deadline must be refused with the cap named in the message, and the slot-holders must
  still succeed. `docs/load-test.md` says plainly that the numbers are loopback, single
  host, ~1.6 s, and not a capacity plan.

### Step 5 notes (D11 — template environments)

- Decided: **one database per challenge environment**, copied from a sealed template
  (`ALTER DATABASE qc_template IS_TEMPLATE true ALLOW_CONNECTIONS false`). A schema cannot
  be copied; a shared schema with row scoping loses to a learner typing `SELECT * FROM orders`.
- New, additive code only: `backend/internal/dataset/template.go` (create / load / seal /
  drop / reset / reap / size / connection facts) and `backend/internal/dataset/template_test.go`
  (the spike). Nothing was wired into the request path; `engine/`, `httpapi/`, `Makefile` and
  `web/` are untouched.
- Measured on this machine, PostgreSQL 18.3, 2026-10-14: template build 57–71 ms + 55–63 ms;
  **copy median 34–44 ms** (first copy has no cold penalty); burst of 8 = 353–457 ms;
  **drop median 24–29 ms but the first drop pays a ~2.9 s checkpoint**; FORCE drop with a live
  session 128–133 ms; reset 164–228 ms; **7.6 MiB per environment**; grants, rows and the
  cluster-wide `search_path=dataset` all travel with the copy.
- Connection budget: `max_connections=100`, 97 usable by a non-superuser role (measured 96);
  one environment costs 0 slots at rest, 1 idle / up to 8 busy with the current pool default.
- Orphans survive a restart and nothing reaps them today; `ReapEnvironmentDatabases` is the
  M4 hook. The learner role can also CONNECT to the metadata database (PUBLIC default) — M4
  should revoke it.

## Known defects carried into M3

- `Config.MaxConcurrent` was advertised in `/capabilities` but never enforced. Fixed in
  Step 1 by a semaphore acquired inside the request deadline (`QUERY_TIMEOUT` on saturation,
  which reuses the existing closed error-code set).
- `/capabilities`, `/health` and (new) `/metrics`/`/diagnostics` are unauthenticated by
  design; none may ever print a DSN, password or hash. Checked in the adversarial suite.

## Defects found by driving the app (owner request, M3)

- **"Create a course" renamed the built-in course instead of creating one. Fixed & verified.**
  The authoring stub (`store.submitBuild`) wrote `COURSES[0].title = …`, so one run destroyed
  "SQL Analytics in the Real World" for the session, and `startLearning` then enrolled that
  same built-in course — the "new" course showed the old course's blurb and 11% progress.
  Found by the owner-driven UI pass (Create → review → "Add to my learning" → Library).
  Now `submitBuild` pushes a distinct course (`created-<epochms>`) with a blurb derived from
  the learner's goal (`Built from your goal: …`) and its own `modules` whose module and
  exercise ids carry a `@created-<epochms>` suffix, so progress and attempts cannot leak
  (`web/src/state/store.tsx` `draftModulesFor`/`blurbFromGoal`; `draftCourse` is exposed and
  `BuildingScreen`/`ReviewScreen`/`startLearning` use it). Verified against the fresh binary:
  the draft shows **0%** with its own blurb, and "SQL Analytics in the Real World" still
  shows **11%** with its original blurb; `npx tsc --noEmit` clean, `make verify` green.
- **`TablesDrawer` kept the last reset note across reopen. Fixed & verified.** The last
  `resetNote` ("Workspace restored in … ms.") survived a close/reopen, so a stale duration
  appeared beside a drawer that had just opened and reset nothing. The drawer now clears
  `resetNote`/`resetError` when it opens (`web/src/components/TablesDrawer.tsx`). Verified in
  the browser: after a reset the footer shows "Workspace restored in 368 ms."; after
  close/reopen the note is gone while the workspace line stays.

## Constraints (do not violate)

- Greenfield: no migrations, no backward compatibility, no backfill.
- No Docker needed locally; embedded PostgreSQL is the default.
- Preserve the visual design and copy — this is a port, not a redesign.
- All frontend data access goes through `ExecutionService` (`web/src/api/`); never `fetch`
  from a screen, never fabricate query results in the UI.
- The gateway stays honest with no database: `ENVIRONMENT_UNAVAILABLE`, never invented rows.
- TypeScript `strict`; `web` must stay clean under `npx tsc --noEmit`.
- Go over Bun is settled (`docs/adr/0001-backend-runtime-go-not-bun.md`).
- Do not commit or tag without asking.
