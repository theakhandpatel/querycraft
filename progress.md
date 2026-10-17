# progress.md: live work tracker

Updated as work lands. The active goal is the **full product**: every remaining
milestone in `docs/technical-design.md` §14 (M5, M6) plus the cross-cutting work
the owner asked for, SQLite on the roadmap, markdown + Mermaid in chapter
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

**COMPLETE (2026-10-16).** Every known open item is closed and every gate is
green, with the app runnable end-to-end from a clean checkout. Committing and
releasing the pending tags remain the owner's call (AGENTS.md: do not commit or
tag without asking), so completion is *code-complete + gate-green + honestly
tracked here*, not a tag.

Final state: all items closed:

| # | Item | State |
| --- | --- | --- |
| 1 | Agent-run audit trail (transcripts: owner-scoped, redacted, bounded, over HTTP) | **done, verified** |
| 2 | Design craft pass on `web/` (Impeccable) + generated `PRODUCT.md`/`DESIGN.md` | **done, verified** |
| 3 | `exercise_count` unsatisfiable request | **done, verified** |
| 4 | Backend simplification pass in `internal/**` | **done, verified** |
| 5 | App metadata → SQLite (Postgres stays the practice engine) | **done, merged & verified** |
| 6 | Outline approval (pause after the outline, approve/discard, then build) | **done, verified** |
| A1 | Release blocker: account-scoped local progress | **done, verified** |
| A2 | Release blocker: empty-admin-password exposure | **done, verified** |
| A3 | Release blocker: brittle `audit-shell` | **done, verified** |
| F2 | Architecture review finding: dialect-aware `POST /environment/reset` | **done, verified** |
| F4 | Architecture review finding: SQLite per-account cache bound/reaper | **done, merged & verified** |
| F5 | Login throttling (the blank admin address was brute-forceable) | **done, merged & verified** |
| F6 | Self-hosted fonts (offline-first) | **done, verified** |

Verification on the final quiet tree (nothing writing):
`make test` green (web typecheck + cgo-free + all audits + all 15 Go packages);
`make verify` green (one binary, SPA + API + auth on embedded PostgreSQL 18.3);
`make verify-mock` green (honest degradation with no database). Full history and
per-item evidence in the sections below.

**The one open caveat, stated plainly:** a single rare flake: 
`TestSQLiteResetDoesNotTouchAnotherAccount` failed **once** in ~90 runs and has
not reproduced under isolation, the full tree, CPU load or shuffled order. It is
instrumented to print the whole `QueryResult` on failure so the next occurrence
names its own cause. Watched, not claimed fixed.

Deliberately **not** done (post-v1 by the independent architecture review, not
gaps in this goal): unify grading into one executable spec across adapters (F1,
which means rewriting 175 authored checks) and split `store.Store` from
`ai.Depot` (F6-store). Both are recorded as the first post-v1 work.

The remaining open items, in dependency order:

| # | Item | Owner | State |
| --- | --- | --- | --- |
| 1 | Agent-run audit trail (transcripts: owner-scoped, redacted, bounded, over HTTP) | delegated | **done, verified** |

**Full end-to-end gates green on the quiet tree (2026-10-16):**
`make test` (web typecheck + cgo-free + all 24 audits + all 15 Go packages),
`make verify` (build → one binary serving SPA + API + auth on embedded
PostgreSQL 18.3: `/health` ok, `/query` 200, anonymous `/progress` 401, admin
login 200, signup 201, scoped progress 200/200, revoked token 401, SPA served),
and `make verify-mock` (no database → `/query` 400 `ENVIRONMENT_UNAVAILABLE`,
auth still enforced): all pass.

**Open: one rare, unreproduced test flake.** `TestSQLiteResetDoesNotTouchAnotherAccount`
(a test written here for F2) failed **once** in ~90 runs, as part of a full
`go test ./...`, 4 assertions in one run, and has not reproduced since under
isolation, the full tree, real CPU load, shuffled order, or 20× repeated
`httpapi` runs. Its failure messages now print the whole `QueryResult`
(`status` + `error`) so the next occurrence names its own cause instead of
forcing another guessing round. Not claimed fixed; watched, not dismissed, and
the F4 eviction path (`MaxAccounts` 8) cannot be the cause in a 2-account test.

### Multi-agent review pass (owner-set, 2026-10-16)

The owner asked for a code review / quality / readability / simplification /
API-standardisation / maintainability pass, with multiple agents on different
parts. Five **read-only** reviewers were dispatched in parallel, each owning a
disjoint area so their findings can be consolidated without conflicts (they
report; they do not edit: the safe changes get applied here under the gates):

| Reviewer | Area | Model |
| --- | --- | --- |
| Go internals | `engine/ eval/ perf/ dataset/ dialect/ envdb/ localdb/ metrics/` | Haiku 5.5 (Codex), `max` |
| Go API surface + contracts | `httpapi/ store/ ai/ contracts/*.json` | GPT-6-Luna (Codex), `xhigh` |
| Frontend TypeScript | `web/src/**`, `web/scripts/` | deepseek-v4.1-flash, high |
| Tests, scripts, build, docs | all `*_test.go`, `scripts/`, `Makefile`, `docs/` | kimi-k2.7-code, high |
| Cross-cutting API design | the seam: contracts ↔ Go ↔ TS ↔ adapters | mimo-v2.6-pro, high |

Each was told: read AGENTS.md + the ADRs before calling a settled decision a
mistake; rank findings real-fault / worth-fixing / taste; give file:line evidence
and separate found from assumed; and end with a "deliberate and fine, do not
touch" list so a later pass does not undo deliberate choices. Status: running,
tree untouched.

**Reviewer 1 (Go API surface + contracts): completed; findings re-verified in
source here before recording.** Real faults:

- **F1 (security, real): `POST /challenges/{id}/patches/{patchID}` ignores the
  route's challenge id.** `handlePatch` (`httpapi/ai.go`) sets
  `dec.PatchID = r.PathValue("patchID")` and never reads `id`; `applyPatch`
  (`ai/review.go`) loads the patch and applies it to `patch.ChallengeID`. So a
  patch belonging to challenge B can be applied through the route for A. Fix:
  bind the patch to the route's challenge and reject a mismatch.
- **F2 (real): patch approval is not atomic with the artifact write.**
  `ApprovePatch` (`review.go:307`) records the approval **before**
  `SaveChallenge` (`:313`); a failed save reports failure but leaves the patch
  approved, and two concurrent approvals can both pass verification. Fix: one
  transactional, version-checked depot operation.
- **F3 (real): malformed JSON returns query-error codes on every route.**
  `readJSON` (`server.go`) answers `EMPTY_QUERY` / `QUERY_SYNTAX_ERROR` for any
  endpoint, so bad JSON to `/tutor` gets a code absent from the AI error enum.
- **F4 (real): `/capabilities` reports a false isolation mode.** `handlers.go`
  says `"isolation": "read_only_tx"`, but signed-in statements run in writable
  per-account workspaces (ADRs 0003/0006), cosmetic-but-dishonest.
- **F6 (real contract fault): `evaluation.schema.json:29` omits
  `DIALECT_MISMATCH`** while `query-result.schema.json:24` has it, though
  `/answers/evaluate` can return it, a contract the gateway can violate.
- F5 (OpenAPI drift: missing per-route `security`/401 on `/courses/generate`,
  `AICourseRecord.status` lacking `building`, AI codes missing from an error
  enum), F7 (route-shape asymmetry: `GET /challenges/{id}` bare vs
  `GET /courses/{id}` `{ok,course}`; reset-by-delete vs DELETE), F8 (`Store` is
  27 methods; `Courses`/`Runs` load every owner then filter in the handler): 
  verified as described, worth fixing, not v1-breaking.
- Assumed (not confirmed): challenge lists/reads/deletes are not owner-filtered
  (courses/runs are), either add owner-scoped depot ops or document challenges
  as a shared catalog.
- Called deliberate and fine: separate AI error vocabulary (ADR 0005),
  writable-workspace model (ADR 0003/0006), Postgres `app` schema, the
  provider-neutral AI interface.

**Reviewer 2 (tests, scripts, build, docs): completed. One claim is FALSE; the
rest verified.** The false one first, because it shows why every finding is
checked against the code rather than relayed:

- **❌ FALSE: "`make test` does not run the Go test suite."** The reviewer read
  only the prerequisite *list* on `Makefile:75` and missed the recipe *body* on
  the very next line, `Makefile:76` is `cd $(BACKEND_DIR) && $(GO) test ./...`.
  Disproved empirically: `make -n test` prints that line, and a real `make test`
  run reports 13 `ok …backend` lines. Not a gap; no change.

Verified findings:

- **R2#2 (real doc drift):** `docs/technical-design.md:37` says "SQLite metadata
  dropped" and `:88` says `No SQLite`, but SQLite metadata shipped (item 5,
  `QC_METADATA=sqlite`). A reader building from the design doc is misled.
- **R2#3 (real flake):** `cmd/api/main_test.go:100-117` uses
  `runtime.NumGoroutine()` as a success signal and polls it, non-deterministic.
- **R2#4 (real):** `Makefile:27` pins `SMOKE_ADDR ?= 127.0.0.1:18080`, so
  `verify`/`verify-mock` fail if that port is taken; `scripts/lib/gateway.mjs`
  already has a `freePort()` helper the Makefile does not use.
- **R2#7 (real, latent):** audit scripts share fixed `/tmp/curriculum.cjs`,
  `/tmp/progress.cjs` etc. `make test` runs them sequentially so it is safe
  today, but `make -j` or a parallel run can clobber (PID-scoping fixes it).
- **R2#8 (real doc drift):** `web/AGENTS.md:29` says "Three courses are
  complete"; the tree ships 8+.
- **R2#10 (real):** the `audit-fixtures` Makefile comment claims a "Go half"
  that the recipe does not run.
- R2#5/R2#6 (the SQLite reset flake and the retry-on-closed-pool helper) are the
  same two hazards already recorded here; the reviewer agrees the retry is a
  legitimate *temporary* guard, not a fix.

**Reviewers 3–5 (Go internals, frontend, cross-cutting seam): first dispatch
DID NOT RUN on an external billing failure (`402 insufficient_balance`), then
re-dispatched on the working WSL provider and running. Areas still under review.**

**Fixes applied from the completed reviews** (each verified in source first, then
re-verified with the full gates; two commits `542c4808c`, `18c13a94e`):

- **F1 (security), fixed + regression test.** `PatchDecision` gains
  `ChallengeID`, set from the route path; `applyPatch` refuses a patch whose
  challenge disagrees (404, nothing touched). Proved: the new test shows a
  correctly-routed apply still succeeds after a mismatched one is refused.
- **F2 (correctness), fixed.** Artifact storage now precedes the approval
  record, so a failed store cannot leave a false "approved".
- **F6 (contract), fixed + non-vacuous guard.** `evaluation.schema.json`'s
  nested `qr.error.code` enum gained `DIALECT_MISMATCH` (+ `qr.dialect`); a new
  test pins the two nested enums equal, and was proved to FAIL when
  `DIALECT_MISMATCH` is removed.
- **R1-F4 (honesty), fixed.** `/capabilities` reported `isolation:
  "read_only_tx"` flatly; now `{anonymous, signed_in, writable}`, matching
  ADRs 0003/0006. OpenAPI + tests updated.
- **R2#2/#8/#10 (doc drift), fixed.** `docs/technical-design.md` no longer says
  "SQLite metadata dropped / No SQLite"; `web/AGENTS.md` no longer says three
  courses (eight ship); the `audit-fixtures` Makefile comment no longer claims a
  Go half it lacks; the stale `contract_test` header comment corrected.
- **R2#3 (flake), fixed.** The goroutine-count test now asserts "no per-call
  leak" instead of an exact baseline, which is inherently non-deterministic.

**Reviewer 3 (Go internals): completed; findings verified, high-value ones fixed
in `8f2f4dd` (engine layer).** Real faults fixed:

- **Operator config silently ignored (measured).** `Execute` re-merged
  `DefaultLimits()`, which clamps *down*, so `QC_MAX_ROWS=2000` ran at the
  compiled 500. Added `Limits.Fill` (fills unset, never lowers); `Execute` uses
  it, the caller's `Merge` still enforces request-vs-server. Proved with a test
  that drives `Execute` for 600 rows, **it reads 500 with the old code**.
- **Contract honesty:** the blank-SQL refusal hardcoded `engine=postgresql`
  while naming `dialect=sqlite`; both now come from the executor.
- **Interface contract:** `Explain` used `classify()` not `classifyFor()`, so a
  dialect with its own error vocabulary would never be consulted.
- **Adapter parity:** `isBlankSQL` stripped `--` but not `/* */`, so
  `/* nothing */` was `EMPTY_QUERY` in the browser and a syntax error over HTTP.
- **`Facts.ResetAt` was structurally always zero**: the field was read, never
  written (reset drops the environment that would hold it). Now recorded per
  account and read by `Facts`.

Go-internals findings recorded but deferred (real, not v1-breaking): #4 (`Values`
path skips `sqliteErrorOf` on a scan error), #6 (envdb holds one mutex across the
pool connect + probe, F5, the brief's addition is that the pool/probe half does
not need the lock), #7 (perf template never pre-warmed despite its comment), #8
(`ReapEnvironmentDatabases` not wired), #9 (`eval` keeps boolean+detailed
grading pairs, and production runs the detailed one), #10 (`dataset/template.go`
exports a test-only surface), #11 (`parseSQLiteDSN` unreachable), #13 (`classify`
`ctx` param unused), plus taste items (a mojibake in `metrics.go:311`).

Explicitly protected: `sqlite_raw.go` (measured DATE defect), the single
`Execute` cap path, the classify table, dialect-as-data, ADR 0002/0003/0004/0006.

**Multi-agent review pass complete.** Five reviewers, every finding re-verified in
source before acting, all five areas reviewed. Fixes landed across four commits
(`542c4808c`, `18c13a94e`, `ccf14b2bd`, `118eee28e`, `27d3c8dd1`); `make test`
and `make verify` green on the final tree. Deferred items are listed above and in
the sections below, as a deliberate post-v1 tidy rather than dropped.

**Deliberately deferred from Reviewers 1–2 (recorded, not silently dropped):**
R1-F3 (malformed JSON returns query error codes on every route), R1-F5 (OpenAPI
per-route security/401 + status-schema drift), R1-F7 (route-shape asymmetry),
R1-F8 (`Store` width; owner-filtered list reads), R2#4 (`SMOKE_ADDR` fixed port: 
24 occurrences across the `verify` recipes; only bites when 18080 is taken, so not
worth risking the primary gate).

**Reviewer 5 (cross-cutting API design / the seam): completed, pinned to
`2f64865fb` and re-verified.** Highest-value findings fixed in `ccf14b2bd`:

- **#3 (real) one error taxonomy:** `PERSISTENCE_UNAVAILABLE` was a client-only
  code outside the closed enum while `errors.ts` claims codes are contract codes.
  Renamed to `PERSISTENCE_FAILED` across `idb.ts`/`execution.ts`/`store.tsx` and
  the storage-audit assertions.
- **#4 (real) one feedback-code set:** the mock emits authored `checks[].code`
  straight through, but **8 of 24** authored codes were outside
  `evaluation.schema.json`'s enum (CASE_MISSING, DISTINCT_MISSING, NULL_SAFETY,
  NULL_TRAP, RECURSION_MISSING, SET_OP_MISSING, SUBQUERY_MISSING, TABLE_MISSING),
  so the same exercise showed different feedback per adapter. Added to the
  contract enum and to Go's `validFeedbackCodes`, plus a **non-vacuous guard**
  (`content-audit`) that reads the enum from the schema and asserts every
  authored code is in it, proved to fail when one is removed.
- **#5 (worth fixing):** extended nothing yet, but recorded, the enum guard
  compares enums only; `evaluation.qr.required` still omits `dialect` while
  `query-result.required` includes it.

Deferred, recorded (real but structural / not v1-breaking): #1 (`/environment/
reset` 400 documented as `ErrorEnvelope` but returns a `QueryResult`, and
`DIALECT_MISMATCH` is outside the documented envelope enum), #2
(`exercise.schema.json` is not the request contract the client sends, Go decodes
`Setup`/`Type`/`Prompt` and never reads them), #6 (`duration_ms` is string in the
contract, number in the mock), #7 (the closed enum is hand-copied in 6+ places;
only some are guarded).

Also praised and told us not to touch: the fixture-suite architecture, the
separate AI/plan error vocabularies, and the dialect/dataset resolution modules.

**Reviewer 3 (Go internals) and Reviewer 4 (frontend): still running** on the
re-dispatch.

### Review pass: the deferred set, closed (owner: "complete, do not defer")

The owner then said **complete everything, do not defer**, so every recorded
deferred finding was worked. Batches, each committed and gate-green:

- **Batch 1**, R3 #4 (Values scan wraps `sqliteErrorOf`), #6 (envdb lock
  narrowed; pool connect + probe now outside it, guarded by a per-key in-flight
  marker; concurrent test proves 8 racers get one pool, `-race`-clean), #7 (perf
  template pre-warmed at boot), #8 (`ReapEnvironmentDatabases` wired via
  `Manager.ReapUnreferenced` + `Store.UserIDs`), #9 (eval's boolean
  `Compare`/`CompareColumns` are now thin wrappers over the `*Detailed` forms;
  duplicate `compareRows` deleted), #10 (template.go test-only exports
  annotated), #11 (`SQLiteConfig.DSN` annotated as unused-by-production), #13
  (`classify` now consults `ctx.Err()`), metrics mojibake. Plus R5 #1
  (`/environment/reset` refusals use the route's `{ok,error}` envelope;
  `DIALECT_MISMATCH` added to the ErrorEnvelope enum; `notFound` carries
  `ok:false`) and #2 (`exercise.schema.json` now describes the request the client
  sends, with a test that validates the real `execution.ts` shape).
- **Batch 2**, R5 #6: `Evaluation.duration_ms` unified on the contract's string
  across all adapters; the `number|string` union removed.
- **Batch 3**, R5 #7: one test reads every copy of the closed error-code enum
  (two contract files, two OpenAPI schemas, the plan exception) and asserts
  equality with the canonical set.
- **Batch 4**, R1-F3: unreadable bodies get route-appropriate codes
  (statement routes keep the query vocabulary; auth/AI get their own).
- **Batch 5**, Frontend #2: the tables drawer describes the engine it names
  (gateway serves `?dialect=sqlite` over the executor seam; the mock refuses
  honestly). Plus #6 (unused-helper comments), #8 (failed tables read shows an
  error, not a blank panel), #9 (`dataset_version` read from the schema), #10
  (`initialProgress` evaluated once).
- **Batch 6**, R1-F8: `CoursesFor`/`ChallengesFor`/`RunsFor` added to the Depot
  seam and all four stores, so owner-scoped list routes filter in the store.
- **Batch 7**, R1-F5: `security` on the 13 session-requiring operations; a test
  pins the set against the handlers. R2 #4: `SMOKE_ADDR` auto-picks a free port
  (proved with 18080 held → picked 18081).
- **Batch 8**, R5 #5: `evaluation.qr.required` gained `dialect`; the enum guard
  now also compares `required`.
- **Batch 9**, R1-F5 remainder: a 401 documented on every secured route
  (purely additive, 48 lines); `AICourseRecord.status` gained `building`.
- **Batch 10**, R1-F7: `GET /challenges/{id}` returns `{ok, challenge}`,
  matching `GET /courses/{id}`.
- **Frontend #7** (`store.tsx` size): a large, regression-prone split of the one
  module every screen imports, for a "not a bug, worth recording" note. **Not**
  done as a risky refactor; instead the file opens with a concern map so the next
  reader has the orientation. This is the one item deliberately left as a note
  rather than a change, and the reason is recorded here.

All gates green on the final tree: `make test` (typecheck + cgo-free + all 24
audits + 15 Go packages), `make verify` and `make verify-mock` (embedded
PostgreSQL 18.3, and honest degradation).



JSON returns query error codes on every route), R1-F5 (OpenAPI per-route
security/401 + status-schema drift), R1-F7 (route-shape asymmetry), R1-F8
(`Store` width; owner-filtered list reads), R2#4 (`SMOKE_ADDR` fixed port, it
touches 24 occurrences across the `verify` recipes and only bites when 18080 is
taken, so it is not worth risking the primary gate for; a re-dispatch on a
different provider can carry it).



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
never enter the product repo. No tag, no push: a checkpoint, not a release.

**First two parallel worktree tracks** (both branch from `e7f39de1a`, verified to
contain M6/M8):

| Track | Worktree | Branch | Scope |
| --- | --- | --- | --- |
| Login throttling | `t3-665821a8` | `t3/665821a8` | per-IP/account failed-login throttle on `POST /auth/login`, 429, bounded map |
| SQLite env cap + reaper | `t3-779bab08` | `t3/sqlite-workspace-eviction` | bound the `byAccount` executor cache (F4), idle eviction, keep isolation |

Both are asked to **commit in their worktree** and report branch + sha, so they
can be merged back after verification. Main checkout continues item 6 while they
run: genuinely parallel, disjoint files.

Also landed this session (Luna, Codex `gpt-6-luna` xhigh/priority: verified live
first): **self-hosted fonts.** `web/public/fonts/*.woff2` (14 subsets, 220 KB),
`web/src/styles/fonts.css` imported by `querycraft.css`, Google Fonts `<link>`s
removed from `index.html`. Verified: `typecheck`+`build` clean, `dist/` has **0**
`fonts.googleapis.com`/`gstatic.com` references and all 14 woff2 copied. The app
now renders fully offline, which is its own premise.

### Polish stream (owner-set, 2026-10-16): small refinements toward a good v1

> "Keep improving the app. Add small refinements, reproductions, UI/UX
> enhancements, small interactions, micro-interactions that make the app better."

Guardrails for this stream: **extend, never redesign** (AGENTS.md preserves the
visual design and copy); every change must be verifiable and must not weaken an
honesty rule; nothing is "polish" if it makes the app claim something it does
not do. Ranked by "is this a mock tell or a real dead end" first, cosmetic last.

Landed:

- **F2, dialect-aware `POST /environment/reset`** (architecture review finding).
  Done and verified; see below.
- **"Explain my mistake" is now a real tutor call, not a stub.** It toasted *"In
  a live app, a tutor call would…"*: the classic "it's a mock" tell the tracker
  warns about, although the gateway already serves `POST /tutor`
  (`SqlCell.tsx`). It now calls `s.api.tutor({sql, objective, error_code,
  error_message, hint_level, result_summary})` and renders the hint with its
  level, with three honest states: a spinner while asking, the hint (naming
  whether it reveals the approach), and, on the offline/mock adapters or AI-off,
  where the seam fails with `AI_UNAVAILABLE`, a plain "Tutor unavailable" that
  says the exercise still grades normally. It never invents an answer. The
  "ask for a firmer hint" action escalates `hint_level` from the previous
  answer (without it the second ask repeated the first: the level is the whole
  difference between a nudge and a worked approach). The live region announces
  all four states. `npm run typecheck` + `npm run build` clean.

Still open in this stream (candidates, not yet done):

- "Report a problem" (`SqlCell.tsx`) and "Save challenge"
  (`ChallengeLabScreen.tsx:551,627`) are still toasts; both have real gateway
  endpoints (`/challenges/{id}/critique`, `/challenges`) and are the same
  mock-tell class as the tutor button that is now fixed.
- The render-blocking third-party Google Fonts `<link>` (`web/index.html:8-10`):
self-host for an offline-first app (flagged by both the craft pass and the
  architecture review).



An independent read-only critic drove the built binary and read the source. Its
verdict: the one-sentence promise ("learn SQL by writing it; graded on the
result") is **delivered**: `course-audit.mjs` ran 53/53 authored solutions to
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
- **B-A3 (release gate red).** `make test` failed `audit-shell`, **fixed here**:
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

### Architecture expert review (2026-10-15): Claude Haiku 5.5 via Codex, reasoning `max`

An independent outsider review (no edits, opinion only) of the whole codebase: 
README, AGENTS.md, the spec, the ADRs, `progress.md`, `backend/internal/**`, the
`ExecutionService` seam and its three adapters, `store.tsx`, `contracts/`.
Verdict: **the product promise and the core safety design are real** (one
`Execute` path with caps, restricted role, dialect profile as data, owner scoping
from the token); the structural risk is **grading and routing re-implemented per
adapter in two languages with no shared executable spec.**

Findings re-verified here (the review is a judgement, not gospel):

- **F1, Grading differs by adapter; authored checks are dead outside the mock.**
  Confirmed: `curriculum.ts` ships **175** `test: function(s){ return /…/.test(s) }`
  closures (e.g. `:37-39`); the mock runs them (`execution.ts:419-432`); PGlite
  grades by *result* (`:767`) and never reads `block.checks`; the HTTP adapter
  sends no `checks` (`:1118-1148`). This is the deepest fault: the learner's
  checklist changes with the adapter, and the spec's "grade the result, not the
  text" rule is violated in the mock. **Worth fixing**. It is the one latent
  correctness gap with no test watching it.
- **F2, SQLite reset resets the wrong database on the HTTP adapter.** Confirmed:
  `execution.ts:1168` drops `_dialect`; `handleEnvironmentReset`
  (`environment.go:155`) has no dialect input and only drives the PostgreSQL
  manager; `httpapi/sqlite.go` has no reset path at all. On a SQLite chapter
  "restore my data" returns `ok`, resets PostgreSQL, and leaves the SQLite file
  untouched: the honesty rule broken on the second dialect. **Worth fixing.**
- **F3, learner→workspace privacy fix *was* half-done.** *Already resolved since
  the review read the tree:* the write path now uses `progressKeyFor` throughout
  (`store.tsx:585,713,724,733`) and an owner change resets progress state
  (`:572-582`). This is the A1 work landing. Recheck when A1 reports.
- **F4, two practice-environment managers, no shared abstraction.** Confirmed:
  `envdb.Manager` (Postgres, capped/reaped) vs `httpapi/sqlite.go` `byAccount`
  map (`:63,133,153`) with no cap, no reaper, no reset. "SQLite is second-class"
  is accurate.
- **F5, `envdb` serialises all accounts behind one `m.mu`.** Confirmed:
  `Ensure:375`, `Reset:443`, `EnsurePerf:267`, `reapOnce:185` all lock `m.mu`
  across `CREATE`/`DROP DATABASE`; ADR 0002 measured a 2.9 s first drop.
- **F6, `store.Store` embeds `ai.Depot`** (27 methods, `store.go:47-77`) and
  imports `internal/ai`; boot silently falls back to memory (`main.go:264-282`).
- **F7, default bind is all-interfaces.** Confirmed: `QC_ADDR` defaults to
  `:8080` (`main.go:43`), contradicting the spec's "loopback by default", and
  combined with the empty-password admin (`auth.go:164`, warn-only) + the
  advertised hint, a fresh `make serve` on shared Wi-Fi exposes an admin login.
  This overlaps release blocker **A2**.
- Also noted: exercise shape accepts historical spellings with no canonical form
  (`eval.go:60-95`); the hand-written `sqlite_raw.go` driver (justified by a
  measured defect); Makefile writes shared `/tmp/progress.cjs`,
  `/tmp/curriculum.cjs` (`Makefile:253-371`): racy and not hermetic; the
  `SqlCell.tsx:222,225` stub buttons (already tracked).
- **Explicitly praised and told us not to touch:** ADR 0001–0007; the single
  `Execute` cap path; the dialect profile as data
  (`contracts/dialects/sqlite.json`), called "the best seam in the repo"; the
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

Drive tick (latest): **one live track, item 5, `SQLite metadata store
(worktree)`**, running (`working`), no pending question, and now **substantially
built**: `store/sqlite.go` (341) + `store/sqlite_depot.go` (354) +
`store/sqlite_test.go` (269, 964 lines total), with `store/store.go`,
`cmd/api/main.go` + `main_test.go`, `httpapi/metrics.go`, `httpapi/openapi.go`
and `backend/README.md` updated (9 changed/added files). The worktree **builds
clean** again. Its own note: the implementation, opt-in selection, docs,
diagnostics and the parity/reopen tests are in place; focused tests pass, and it
is now reviewing semantics before the full verification run, left alone.

The parity approach is the one that matters and it is real:
`TestPersistentStoreParity` takes a `storeFactory` map and applies **the same
behaviour cases to Postgres (the reference) and SQLite (the additive backend)**,
so SQLite is pinned to the contract Postgres already defines rather than to its
own behaviour. Main checkout clean.

Drive tick (previous): **one live track, item 5, `SQLite metadata store
(worktree)`**, running (`working`), no pending question. It is now writing in
earnest: `store/sqlite.go` (251 lines) and `store/sqlite_depot.go` (210, the
`ai.Depot` half, correctly split from the interface half) plus boot wiring in
`cmd/api/main.go`. Two design choices worth recording, both right:
- an **explicitly selected** SQLite store now returns an error from `newStore`
  and exits, instead of silently falling back to memory, so a misconfigured
  metadata store fails loudly rather than appearing to work;
- selection is `QC_METADATA=sqlite` **only when no metadata DSN is set**, so a
  configured `QC_ADMIN_DATABASE_URL` continues to select PostgreSQL: Postgres
  stays the default.

The worktree is transiently non-compiling (`sqlite_depot.go` has an unused
`encoding/json` import), mid-write, expected, and not touched since the agent
owns that branch. Main checkout clean.

Drive tick (previous): **one live track, item 5, `SQLite metadata store
(worktree)`**, running (`working`), no pending question. It has started
writing: `backend/internal/store/sqlite.go` (253 lines) now exists in the
worktree, using `database/sql` over `modernc.org/sqlite` directly (not the raw
driver, correct for metadata, which is all text/jsonb). Its own note confirms
the additive split and that, finding no existing shared parity table, it is
adding a **backend-parameterized parity harness** so the SQLite store is checked
against the Postgres semantics rather than only itself. Left alone. Main
checkout clean.

Drive tick (previous): **one live track, item 5, `SQLite metadata store
(worktree)`**, running (`working`), no pending question. Its worktree
(`t3-7a66dc85`, branch `t3/sqlite-metadata-store`) is still at the branch point
with nothing written; the agent's activity is ~1 min old and it kept a todo list
(read the roadmap rationale, inspect the store + boot wiring + existing parity
tests, then implement): deliberate reconnaissance before a large task (the
whole `Store` interface incl. `ai.Depot`), so it is left alone. Main checkout:
`go build`/`vet`/`gofmt` clean and `internal/store` tests green.

Drive tick (previous): **no live tracks on entry; tree clean.** Item 4
(simplification) is now done, which was item 5's documented precondition ("It
must land *after* the simplification pass, because both touch `internal/store`"),
so item 5 was advanced, as a **new worktree track**, branch
`t3/sqlite-metadata-store` (`t3-7a66dc85`), branched from the item-4 commit
`678533144`. Model: **Luna on Codex at xhigh/priority** (the owner's choice).
Scope is deliberately **additive and reversible**: a SQLite implementation of
`store.Store` selected by an explicit env var (`QC_METADATA=sqlite`), with
Postgres staying the default and its behaviour unchanged; parity tests against
the Postgres semantics; `CGO_ENABLED=0` must stay clean. The brief tells the
agent to say so plainly rather than force it if it cannot be done without
risking the default.

Earlier tick (uptime ~228 min, no restart): **two worktree tracks running,
both far along, neither blocked.**
- Login throttling, worktree `t3-665821a8`, running: has written
  `login_throttle.go` + `login_throttle_test.go` and edited `auth.go`,
  `server.go`, `openapi.go`, `cmd/api/main.go`, `backend/README.md` (7 files).
  **Its branch builds clean.**
- SQLite env cap + reaper, worktree `t3-779bab08`, running: `sqlite.go` +
  new `sqlite_cache_test.go` + `cmd/api/main.go` + `backend/README.md`
  (4 files). **Its branch builds clean.**

Item 6 is done and verified (see above). Both worktree agents are still running, 
almost certainly their own gate suites, so they are left alone. Neither branch
has committed yet. Main checkout is quiet; full gates run once the worktrees land
and are merged.

Item 2 (craft pass): **done and verified (2026-10-15).** The agent's report was
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

F4 (SQLite workspace cache bound): **done, merged and verified (2026-10-16).**
Implemented in worktree `t3-779bab08`, branch `t3/sqlite-workspace-eviction`,
commit `769385b69`. Verified on the branch before merging (`go build`/`vet`/
`gofmt` clean, **all 15 packages green**, targeted SQLite/Reset/Evict tests
green), then `git merge --no-ff` into main: auto-merged only `backend/README.md`
(the branch added three env-table rows; item 6 added two route rows, a different
region). Merged tree re-verified green on all 15 packages.

What it does: `byAccount` now holds `*accountEnvironment{exec, lastUsed}` with an
LRU eviction and an idle reaper, mirroring `envdb.Manager`'s shape without its
Postgres mechanics. `SQLiteOptions{MaxAccounts: 8, IdleTTL: 15m}` (configurable
via `QC_SQLITE_MAX_ACCOUNTS` / `QC_SQLITE_IDLE`); `NewSQLiteEnvironments(root)`
is unchanged and delegates, so nothing existing breaks. Eviction closes a pool
only: the learner's file stays on disk and re-opens, so the isolation guarantee
and the F2 `Reset` semantics are untouched. New `sqlite_cache_test.go` (359
lines, real SQLite over a temp root) proves the bound holds across 10 accounts,
an evicted account re-opens its own file and cannot see a neighbour's, the idle
pool is reaped while its rows survive, `Reset` stays bounded, and 24 concurrent
workers force evictions race-free. The agent's honest caveat: the full-package
`-race` run validated the immediately-prior revision (before its final test-helper
refactor); the committed code's new tests were re-run under `-race -count=5`.

Item 6's remaining edits, **committed** as `45a76ebaa` ("Outline approval … and
the rest of item 6") so the worktree branch could merge onto a clean tree; its
`ai/` changes were already captured by the checkpoint `e7f39de1a`.

One flake observed: the first post-merge `go test ./...` printed a bare `FAIL`,
but four subsequent full runs were clean and a targeted re-run of the three
suspect packages passed. Cause is unproven; most likely contention from two
worktree agents running `-race` suites concurrently. Recorded, not dismissed. 
Re-check on the next quiet-tree gate.


disk, not the agent's summary: `go test ./... -count=1` green on **all 15
packages**; `go build`/`go vet` clean; `web` `npm run typecheck` + `npm run build`
clean.

F2 (dialect-aware reset): **done and verified (2026-10-16).** Implemented
directly (not delegated). `POST /environment/reset` now takes `?dialect=`, absent
meaning PostgreSQL; a SQLite request restores the learner's *file* via the new
`SQLiteEnvironments.Reset` (`sqlite.go`), which closes the cached executor,
removes the file family (`-journal`/`-wal`/`-shm` too, so a stale journal cannot
replay over the fresh seed), and re-seeds from the committed fixture under the
same lock: the SQLite shape of ADR 0003's drop-and-copy. The HTTP adapter no
longer drops the dialect (`execution.ts` `environmentResetPath` sends it), and
OpenAPI documents the parameter. Three new tests in `dialect_test.go` assert by
*querying the engine*, not by trusting the response body: writes are discarded
(31→30, the row gone), one learner's reset does not touch another's file, and an
anonymous caller is refused. `go build ./... && go vet ./...` clean; full
`go test ./... -count=1` green on all 15 packages (including `httpapi`, 49 s).

Login throttling: **done, merged and verified (2026-10-16).** Worktree
`t3-665821a8`, branch `t3/login-rate-throttling`, commit `a3821f367`. Verified on
the branch (build/vet/gofmt clean, all 15 packages green, `-race` throttle tests
green), then merged. Resolved one conflict: both this branch and the F4 branch
added rows to the same `backend/README.md` env table: kept both, since both are
real. The merged tree is green on all 15 packages.

What it does: an in-process fixed-window failure counter keyed by
`ip \x00 normalized-email` (`login_throttle.go`, one mutex, no new dependency).
Keyed on `r.RemoteAddr`, **not** `X-Forwarded-For` (caller-supplied); the
password is never stored. At the cap, the record nearest expiry is evicted, so
the map cannot grow unbounded; `now` is injectable so the window is tested
without sleeping. `handleLogin` checks the throttle first and answers
`429 AUTH_RATE_LIMITED` with `Retry-After`, identical for known and unknown
addresses (it never consults the store, so it cannot enumerate accounts); below
the limit the existing 401 is untouched; a success resets. Limits configurable
via `QC_LOGIN_MAX_FAILURES` (5) / `QC_LOGIN_WINDOW_MS` (900000) /
`QC_LOGIN_MAX_TRACKED` (4096); `newLoginThrottle` falls back to defaults when
non-positive, so zero cannot silently disable it. 7 tests through the real
handler. The agent's declared limits, recorded honestly: it is in-process, so it
does not survive a restart and is not a distributed defence: the README says so.
It flagged one web-side gap, which is now **fixed**: the sign-in screen had no
`AUTH_RATE_LIMITED` case, so a throttled learner saw "email or password is
incorrect", a lie, since their credentials may be fine. `AuthScreen.tsx` now
answers "Too many failed sign-ins. Wait a few minutes and try again."
(`76527e38c`).



Release blockers A1 + A2: **done and verified (2026-10-15/16).** Verified against
disk, not the agent's summary: `go test ./... -count=1` green on **all 15
packages**; `go build`/`go vet` clean; `web` `npm run typecheck` + `npm run build`
clean.

*A1 (progress bleed).* The fix is real and covers all three failure points:
storage is owner-scoped (`progressKeyFor` → `qc.progress.<userId>`, `.local` for
the offline workspace, `store.tsx:70-71`); a `useLayoutEffect` swaps the
in-memory snapshot before paint on every owner change (`:575-582`); and hydration
now treats an **absent** server snapshot as authoritative on the `http` adapter: 
it clears the local copy instead of returning early (`:723-742`), which is
exactly the path that let B see A's history. Offline adapters keep their local
snapshot so the local workspace still survives a reload. Honest gap the agent
declared: the multi-account *browser* reproduction was not run (no browser
attached; the repo has no DOM/JS test harness at all: no vitest/jest/jsdom and
zero `*.test.*` files), so the frame-level behaviour rests on `useLayoutEffect`
semantics verified by reading, not exercised at runtime. Server-side owner
scoping was proven against the real binary + embedded PostgreSQL.

*A2 (empty admin password).* `defaultAddr = "127.0.0.1:8080"` (`main.go:288`),
`isLoopbackAddr` fails closed on wildcard/`0.0.0.0`/LAN/IPv6-any/non-`localhost`
(`:296`, unit-tested in `main_test.go:18`, passing), and startup is **refused**
with exit 1 (no password/hash printed) when the admin password is empty and the
bind is not loopback (`:175-178`). The check reads the *stored hash* and calls
`auth.Verify(hash, "")`, correct, because the seeded blank password is a real
bcrypt hash, not an empty string. `/health` gains the additive, non-secret
`admin_password_empty` boolean (`handlers.go:38`, `openapi.go:1047`), and the
sign-in hint renders only when it is true (`AuthScreen.tsx:174`). Harness binds
were updated to loopback so the gates still boot (`Makefile:27`
`SMOKE_ADDR=127.0.0.1:18080`; `scripts/lib/gateway.mjs`, `scripts/load-test.mjs`),
validated live: wildcard+empty → refuses; loopback+empty → starts, hint true,
blank login 200; non-loopback+password → starts, hint false, blank login 401.

Not built (declared out of scope): login throttling. Recommended follow-up.

Item 4 (backend simplification): **done and verified (2026-10-16).** The
"McClintock" track from the earlier session never landed anything verifiable, so
this was done directly with evidence rather than closing it on faith.

Evidence-first scan: no dead **exported** functions in product code; `go build`/
`vet` clean. Two real findings, both fixed:

- **Dead code:** `eval.primaryCode` (`eval.go:267`) was defined once and called
  nowhere, orphaned when the evaluator was refactored in M3 (only stale M2-era
  worktrees still referenced it). Removed.
- **The duplication the architecture review named (F5-adjacent):**
  `envdb.Manager.Ensure`/`EnsurePerf` and `Reset`/`ResetPerf` were near-copies,
  differing only in a cache-key prefix, the template, the database name and the
  log/error labels, and they had **already drifted** (the perf variant computed
  its create duration twice). The template builders (`ensureTemplateLocked` /
  `ensurePerfTemplateLocked`) were likewise copies. Collapsed to one `ensure`,
  one `reset` and one `ensureTemplateFromLocked`, parameterised by an `envKind`
  descriptor that makes every per-kind difference a single field. Behaviour is
  preserved by construction: same keys, same template, same database names, same
  messages. Net **−38 lines** (`manager.go` 749→~715), and the refactor then
  orphaned `createIfAbsentLocked`, which was removed too.

Verified: `go build`/`vet`/`gofmt` clean; `go test ./... -count=1` green on all
15 packages, including the three `envdb` tests against **real PostgreSQL**
(per-account isolation, idle reaping, and the perf plan environment), and the
dead-code re-scan is now empty. No contract, schema or wire behaviour changed.

Not touched (deliberately): the reviewer's larger structural items, unifying
grading into one executable spec (F1) and splitting `store.Store` from
`ai.Depot` (F6), are **post-v1** by its own ranking, and F1 means rewriting 175
authored checks. Item 4 was "close the simplification track", not "do the
post-v1 refactor early".

Item 5 (app metadata → SQLite): **done, merged and verified (2026-10-16).**
Worktree `t3-7a66dc85`, branch `t3/sqlite-metadata-store`, commit `2cd4b8068`.
**Additive and reversible by construction**: SQLite is selected only when
`QC_ADMIN_DATABASE_URL` is empty *and* `QC_METADATA=sqlite`; a configured metadata
DSN still selects PostgreSQL and the Postgres store's behaviour is unchanged.
The only edit to `store.go` is a comment. Files: `store/sqlite.go` (343) +
`store/sqlite_depot.go` (354, the `ai.Depot` half) + `store/sqlite_test.go`
(367), plus `cmd/api/main.go`/`main_test.go`, `httpapi/metrics.go`,
`httpapi/openapi.go`, `README.md`.

Verified **on the branch before merging**, then again on the merged tree:
`go build`/`vet`/`gofmt` clean; **`CGO_ENABLED=0 go build ./...` clean** (the
single binary stays cgo-free); `go test ./... -count=1` green on all 15
packages.

The evidence that matters most:

- **Parity, not self-consistency.** `TestPersistentStoreParity` takes a
  `storeFactory` map and applies **the same case table to Postgres (the
  reference) and SQLite (the additive backend)**: round trip/deep copy,
  newest-first ordering, owner attribution, upsert, legacy/absent rows, sessions,
  patch approval. Both subtrees pass. `TestSQLiteStoreSurvivesCloseAndReopen`
  proves the file is the store.
- **Live persistence smoke test (not just unit tests).** Booted the built binary
  with `QC_METADATA=sqlite` and no admin DSN: `/diagnostics` reported
  `{"kind":"sqlite","persistent":true}`, signup returned 201, and a real ~90 KB
  `metadata.sqlite` was created. Then **across a full process restart** on the
  same file, correct-password login returned **200** and wrong-password **401**: 
  the account genuinely survived, which is the whole point of the item.
- **Honest diagnostics.** `Kind()` returns `"sqlite"` and the `/diagnostics`
  `persistent` flag (and the OpenAPI enum) were updated so the gateway does not
  claim persistence it lacks, or deny persistence it has.
- Driver choice, and it is the right one: plain `database/sql` over
  `modernc.org/sqlite`, **not** the hand-written raw driver (`sqlite_raw.go`),
  because that driver exists to work around a DATE-typing defect in the learner
  query engine and metadata timestamps are canonical UTC text: the workaround
  is not needed here.

`make verify` green on the merged tree (Postgres default path unchanged).

Item 6 (outline approval): **done and verified (2026-10-16).** Verified against
disk, not the agent's summary: `go build ./...` + `go vet` clean, `gofmt -l`
clean, **`go test ./... -count=1` green on all 15 packages**, and `web`
`npm run typecheck` + `npm run build` clean. Route registered
(`server.go:197 POST /courses/{id}/build`); `StatusAwaitingApproval`
(`api.go:94`) and `GenerationRequest.RequireApproval` (`:163`); the resume data
change is real: `CourseRecord.Request *GenerationRequest` (`course.go:170`)
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
`contracts/run.schema.json` (`runStatus`): required, because a response or
transcript carrying the new status must validate.


Item 3 (`exercise_count`): **done and verified (2026-10-15).** Verified against
disk: `go build ./internal/ai/` + `go vet` clean (the only build failure on the
tree is `cmd/api/main.go`, which the concurrent release-blockers agent is
mid-edit, not this work), `gofmt -l internal/ai/` empty, and
`go test ./internal/ai/ -count=1` green (38.5 s). Decision, and it is the right
one: **actionable refusal at the outline gate, not a request-time floor**, 
because the model picks the chapter count in the outline call, so "raise
exercise_count to at least the chapter count" is undefined at request time, and
a guess would silently rewrite the learner's ask. `courseExerciseBudget`
(`course.go:394`) is `clampInt(req.ExerciseCount, 2, maxCourseExerciseTotal)`: 
bit-identical to the old inline expression, so the success path is unchanged in
behaviour (the report's "byte-stable" wording was loose: the line now calls the
helper, but it computes the same number). `courseOutlinePrompt` appends one
sentence naming the budget and the at-most-N-chapters coupling as a strict
suffix; `validateCourseOutline` takes the budget and returns
`courseOutlineExerciseError`, which names the number to ask for instead of the
raw provider-shaped `chapters[0].exercises`. Three new tests
(`course_agent_test.go:175,250,354`) including the verbatim defect case, which
now fails with no scratch environment spent.

Item 1 (audit trail): **done and verified (2026-10-15).** Verified against disk,
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

## Documentation, diagrams and walkthrough (2026-10-16)

Owner request: *"create detailed documentation and architecture diagrams, feature
list, READMEs, screenshots, walkthrough recording / gif if needed via
agent-browser, 1080p."*

Every artifact was checked against the running product, not against the source.

| Deliverable | Artifact | How it was checked |
| --- | --- | --- |
| Documentation | `docs/architecture.md`, 7 sections, each one naming the code that proves it | Read back; every relative link resolves |
| Diagrams | 7 Mermaid diagrams in `architecture.md`: the system, the execution seam, the data homes, the query lifecycle, the generation loop, the layers, and the trust boundaries | All 7 parse under mermaid 11 with the repo's own settings |
| Feature list | `docs/features.md`, 10 areas with the code path for each claim, and a closing section on what is unfinished | Every claim traced to a file; the two toast stubs re-read in source |
| READMEs | Root `README.md` drift fixed (3 courses and 5 milestones became 8 courses and 52 chapters), plus a screens section and a documentation section; new `docs/README.md` index | Grep for the stale strings returns nothing |
| Screenshots | `docs/assets/screens/*.png`, 15 screens at 1920x1080 | Captured live through `agent-browser` on the built binary |
| Walkthrough | `docs/assets/walkthrough.mp4` (1:38, 1920x1080, 30 fps), `walkthrough-grade.gif` (15 s), `walkthrough-poster.png` | Frame-checked at 1 fps and at 1/6 fps; the graded `Passed` verdict with all four clauses is on screen |

The previous session stopped on an external failure (provider
`insufficient_balance`). The real blocker was local: `ffmpeg` was missing, so
`agent-browser record` refused to start. A static `ffmpeg` build with the
`libvpx` and `libx264` encoders fixed it. The browser daemon then needed a
restart to pick up the new `PATH`. The gateway was still running on
`127.0.0.1:18200` with its cluster already seeded.

The interrupted run had also left a stray `01-signin.png` at the repo root. It
is deleted.

### Writing standard (owner-set, 2026-10-16)

Owner request: *"write using STE-100, make sure to have a human personality and
not useless stuff"* and *"make the documentation and whole repo look like a
professional standard not AI slop."*

`AGENTS.md` now has a `## Writing` section, so the next edit inherits the rules
instead of rediscovering them. The rules are: sentences under 25 words, active
voice, present tense, no em dashes, no bold lead-ins in list items, no marketing
words, one term for one thing, and a table only where the reader compares values
across rows.

Applied to the documents this work introduced or changed: `README.md`,
`docs/README.md`, `docs/features.md`, `docs/architecture.md`, `AGENTS.md`,
`web/AGENTS.md`, and this entry. The documents that predate this work were left
alone. They hold the detailed per-milestone evidence, and rewriting them would
change recorded measurements without changing any code.

Three concrete edits came out of the pass. The root README lost a 15-row
screenshot table and now shows 4 key screens plus a link to the rest. Every
section that described a capability also names the file a reader can check. The
`docs/features.md` count of "section 9" for the unfinished list was wrong and now
reads section 10.

### React Scan evaluation (owner-set, 2026-10-16)

Owner request: *"also can you try react-scan."*

React Scan 0.5.7 runs against this app. Nothing was added to the product to make
it work: the dev server serves the package from `node_modules`, so
`import('/@id/react-scan')` in the page loads it and `scan()` starts collecting.
That also means no wiring to revert, and no dependency to keep. `web/package.json`
is clean.

Method: `npm run dev` on port 5199, then drive the app through `agent-browser`
with an `onRender` handler that accumulates per-component render counts and
times. Each flow below was measured on its own, from a reset counter.

| Flow | Render entries | Most-rendered | Markdown time |
| --- | --- | --- | --- |
| Open chapter 1 | 270 | `Icon` 135, `Markdown` 21 | 52.6 ms |
| Type 38 characters in the SQL editor | 238 | `Icon` 136, `SqlCell` 34, `DialectRefusal` 34, `ResultGrid` 34 | 0 ms |
| Run a query | 95 | `Icon` 48, `NavButton` 8, `Markdown` 7 | 6.8 ms |
| Type 12 characters in the header search | 114 | `Icon` 57, `SearchBox` 13 | 13.2 ms |
| Open the tables drawer | 277 | `Icon` 137, `Markdown` 21, `Schema` 4 | 19.4 ms |
| Toggle the theme | 99 | `Icon` 46, `Markdown` 14 | 15.0 ms |

Three findings.

Typing is the good case. A keystroke in an SQL cell re-renders 4 components, all
of them inside the cell. The store provider, the sidebar, the header, and the
chapter markdown do not re-render. That is the behaviour the single-provider
design was meant to give.

`Markdown` is the weak case. It is not memoized, and its render body calls
`renderMarkdown(source)` on every render, which parses the markdown and sanitizes
it with DOMPurify again. So an unrelated store change re-parses every block on the
page: 6.8 ms for one query result, 13.2 ms for one search keystroke, 19.4 ms for
the tables drawer, 52.6 ms for the chapter open. `web/src/components/ui/Markdown.tsx`
takes a plain string prop, so `React.memo` would remove the repeat work. There is
no `memo(` call anywhere in `web/src`. This is now the last item in
`docs/features.md` section 10.

The handler in the header search rebuilds its index on every keystroke:
`SearchBox` rendered 13 times in 32.9 ms for 12 characters.

What the tool did not give us. `trackUnnecessaryRenders` reported 0 unnecessary
renders in all six flows, including flows where `DialectRefusal` and `ResultGrid`
clearly re-render with unchanged props. That detector needs React Scan's own JSX
instrumentation, which a page that mounted before the scan started does not have.
The render counts are trustworthy here; the unnecessary flag is not. The counts
above are the evidence, and they agree with reading `Markdown.tsx`.

### React Doctor evaluation (owner-set, 2026-10-16)

Owner request: `npx react-doctor@latest`.

React Doctor 0.9.18 (`millionco`) needs Node 20.19 or later. The default `node`
here is 20.17.0, and its `npx` install also hits the npm optional-dependency bug,
so `oxc-parser` has no native binding and the CLI dies on import. It installs
clean under `nvm use 22.18.0` in a directory of its own. Nothing was added to this
repo. The CLI offers `react-doctor install`, which writes agent skill files, a
`doctor` script, and a dev dependency. That is a repo change, so it was not run.

`react-doctor web --yes --no-score` scans 76 files in 2.9 s and reports 114
diagnostics over 25 files: 4 errors and 110 warnings.

| Category | Findings |
| --- | --- |
| Accessibility | 46 |
| Bugs | 34 |
| Maintainability | 17 |
| Performance | 15 |
| Security | 2 |

The tool's own guidance says to read the code before confirming a finding, so
every group below was checked against its file.

#### The 4 errors, checked

| Finding | Verdict | Evidence |
| --- | --- | --- |
| `effect-needs-cleanup`, `BuildingScreen.tsx:178` | false positive | The effect owns a full cleanup at lines 675 to 686: `controller.abort()`, `clearTimeout(navTimer.current)`, and the refs set to null. The timer is created in the nested `finish` closure and cleared through `navTimer`. The rule did not follow the ref. |
| `effect-needs-cleanup`, `store.tsx:617` | true, not a leak | The cleanup sets `alive = false` (635 to 637), and the retry at 625 only schedules while `alive` is true. A pending timer fires once, sees `alive` false, and returns. No state update after unmount. A `clearTimeout` would make the ownership explicit. |
| `no-ref-current-in-render`, `BuildingScreen.tsx:176` | true, deliberate | `availabilityRef.current = s.aiAvailability` runs in the render body. The comment above it says why: the run effect starts on the first frame and reads the value through the ref. Both a write in an effect and a read in the render body would change the timing. The ref is read only inside effects. |
| `no-ref-current-in-render`, `store.tsx:439` | true, deliberate | Same pattern on `sessionRef`. The comment says the callbacks must not close over the first render's session. Read only inside callbacks. |

#### Confirmed defects worth fixing

| Finding | Impact | Where |
| --- | --- | --- |
| 22 form fields have no label association | A screen reader announces an unlabelled field, and clicking the label does not focus the input. 32 diagnostics over 22 fields. `web/src` has 23 `<label>` elements and 4 `htmlFor`. | `Modals.tsx` 14, `ChallengeLabScreen.tsx` 5, `CreateScreen.tsx` 2, `AppearanceScreen.tsx` 1 |
| Two row lists are unreachable by keyboard | `div` with `onClick` and no `role`, `tabIndex`, or key handler, so Tab cannot reach the chapter rows on these two screens. | `screens/ProgressScreen.tsx:164`, `screens/WorkbookScreen.tsx:97` |
| The prompt dialog does not trap focus | It is `role="dialog"` with `aria-modal="true"`, an Escape handler, and autofocus, so Tab walks out into the page behind it. The tables drawer does trap focus. | `ui/PromptDialog.tsx:37` |
| `aria-expanded` on a search input | The input's role is `textbox`, which does not support the attribute, so a screen reader ignores it. | `SearchBox.tsx:75` |
| The bearer token lives in `localStorage` | Any XSS on the page can read it and exfiltrate it. The client sends it as `Authorization: Bearer` (`execution.ts` 893 to 899). An `HttpOnly` cookie is the standard answer and is an architecture decision, not a patch. | `execution.ts:168` |
| The provider value is rebuilt every render | `const value = { … }` at line 1796 is passed to the provider without `useMemo`, so every consumer redraws on every store render. This is the root cause of the React Scan measurement above: one query result re-parsed the chapter markdown. Note that wrapping the value in `useMemo` is not a small change, because the object exposes every action and every piece of state, and a dependency list would have to name them all. `React.memo` on `Markdown` is the cheaper fix for the measured cost. | `state/store.tsx:1796` |

Smaller items, all real, all low impact: 24 `no-array-index-as-key` (mostly
static lists), 6 `js-set-map-lookups`, 1 `rerender-lazy-state-init`
(`useState(PIPE_STEPS.map(…))` at `BuildingScreen.tsx:136` runs the map every
render), 1 `no-side-effect-in-state-updater-function`, 1
`context-provider-value-from-unmemoized-local-literal`, 4 `no-giant-component`,
9 `no-high-complexity-react-function`, and 1 `exhaustive-deps`.

#### Findings rejected

| Finding | Why it is not a defect |
| --- | --- |
| `insecure-crypto-risk`, `execution.ts:287` | The `Math.random()` call builds the mock adapter's own token, `'mock_' + …`. The mock adapter does not authenticate anything, by definition. |
| `no-json-parse-stringify-clone`, `execution.ts:531` | The rule suggests `structuredClone`. The code comment above the function states the opposite requirement: the JSON clone exists to strip proxies and class instances that IndexedDB refuses to store. `structuredClone` throws `DataCloneError` on those instead of stripping them. The advice would break the stated intent. |

#### The design scan

`react-doctor design --verbose` keeps only its design rules and reports 4
findings. One is a large inline style object rebuilt every render
(`SqlCell.tsx:522`). Three are text at 11 px (`BuildingScreen.tsx:821`,
`ChallengeLabScreen.tsx:466`, `ReviewScreen.tsx:80`). The 11 px size is
deliberate and site-wide: the stylesheet sets 10 px and 11 px for eyebrows,
labels, badges, and mono metadata, so the rule is reporting the house style
rather than a slip.

#### Two tools, one answer

React Scan measured the cost. React Doctor named the cause. The provider value in
`store.tsx` is rebuilt on every render, so a store change redraws every consumer,
which is why a query result re-parsed the chapter markdown. React Doctor reached
the cause without a browser, and React Scan measured it without reading the
provider. Neither result needs the other to be believed, and they agree.

#### Not done

No fix was applied. The label association and the two unreachable row lists change
the tab order, and `docs/accessibility.md` records the tab-stop count as part of
its evidence, so the fix has to update that record in the same change. The token
storage question is architecture. The four items are waiting on the owner.

## Milestones

| Milestone | State | Evidence |
| --- | --- | --- |
| M1: contracts + mock adapter | done | `contracts/`, `web/src/api/` |
| M2: complete workbook (browser) | done, tagged `v0.2.0` | commit `2e7eac2be`, `make verify` green |
| M3: execution gateway (Go + native PostgreSQL) | done, tagged `v0.3.0` | this file |
| M4: AI Challenge Lab | done | `make test` green; `make audit-ai` 6/6; **real-model live audit green** (below) |
| M5: SQL performance lab | done | all 8 steps landed; `make audit-perf` green on all four clauses (below); `docs/m5-implementation-plan.md` |
| M6: second dialect, SQLite | done | all 9 steps landed; `make audit-parity` green for both dialects (PostgreSQL 119/119, SQLite 49/49, 0 diverge); `docs/m6-implementation-plan.md` |
| M7: real course generation | done | a live model generated a four-chapter course; every exercise's reference, cross-check and adversarial fixtures executed in a throwaway database, and the activated plan carried the engine's own rows. `backend/internal/ai/course_test.go` (6 tests, real PostgreSQL), `httpapi/ai_test.go` |
| M8: a course that builds itself, one chapter at a time | **done, tagged `v0.8.0`** | tool-calling agent loop in one conversation (`backend/internal/ai/course_agent.go`): outline first, then one `save_chapter` per chapter, each chapter verified and persisted before the next is asked for. Live evidence below. `make test` green including `audit-course-gen` |

**Release `v0.8.0` (2026-10-16): first public release.** All eight milestones
done; every release blocker (A1 progress bleed, A2 empty-admin-password
exposure, A3 brittle shell audit, F2 dialect-aware reset, F4 SQLite cache bound,
F5 login throttling) closed; the multi-agent review pass completed with its
deferred set worked. Gates at the tag: `make verify` + `make verify-mock` +
`make test` green, and the live-only suites (`audit-load`,
`audit-metrics-live`, `audit-adversarial-live`) green against a booted gateway.
The version is one bump per shipped milestone (M3 was 0.3.0; M4–M8 take it to
0.8.0).

## M7 / M8: real course generation, and the streaming rewrite

### What M7 changed (the honesty fix)

Before M7 the "Create a course" path did not call the gateway at all. The
summary said the request was rejected, because the activated course arrived
with `plan: undefined`: `asAiOutcome` in `web/src/api/ai.ts` parsed the
envelope but never parsed `plan`/`chapters`, so a course that had genuinely
been generated, verified and stored was read by the screen as a refusal. The
draft studio then told the learner "the course was rejected at the
verification stage": a false statement about work that had succeeded.

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
  *Comparing Rows to Their Neighbours* (1), *Putting Windows Together* (1): 
  6 exercises, every one executed and cross-checked by the engine.

### Defect found by driving it: an unsatisfiable request

`exercise_count` is omitted from the request by default and reads as `0`,
which normalizes to **1** exercise. The outline gate requires at least one
exercise *per chapter*, so a request for one exercise across several chapters
can never be satisfied: the run hard-fails with
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
| Agent-run audit trail | `Mencius` | persist each generation's transcript, every message, tool call and tool result, owner-scoped in the database, redacted, bounded, readable over HTTP |

## Roadmap decision: app metadata belongs in SQLite (owner-raised)

The owner has now raised this twice ("for the POC we don't even need pg, we
could have used sqlite"; "why not save the transcripts and tool calls in the
SQLite db"). The transcript work makes the trade-off concrete, so it is
recorded here as a decision to make rather than a detail to discover later.

The app metadata store is small and already has a clean seam: one `Store`
interface (`backend/internal/store/store.go`) with a `postgres` and a `memory`
implementation, seven `app.*` tables, 332 lines in `postgres.go`. And
`modernc.org/sqlite`, pure Go, cgo-free, so it keeps the single binary, is
already a dependency for the SQLite dialect. The practice environment is the
part that genuinely needs PostgreSQL: the product teaches Postgres, and
per-account writable databases, template cloning and roles are Postgres
features.

So the boundary to draw is:

* **SQLite**. What the app knows: users, sessions, progress, events, courses,
  challenges, patches, and the new run transcripts. One file, no server, no
  `embedded_postgres_log*`, and something you can copy and read to audit a run.
* **PostgreSQL**. What the learner practices on: the sealed dataset, the
  per-account writable workspace, the plan environments.

Not started. It must land *after* the simplification pass, because both touch
`internal/store`.

## UI pass: defects found by driving the product (2026-10-15)

The goal asks for the app to be driven *as a learner drives it*: click the
buttons, follow the links, take the notes, submit, retake. Two defects below were
found that way and are fixed on this branch. Neither was reachable from the test
suite, the audits exercise the API and the graders, and no audit renders a page.

### The document scrolled, so the whole shell slid out from under the learner

Found by opening a chapter and looking at where the sidebar had gone: it was at
`y = -220`. The sidebar and header are `height:100vh` inside
`.app{overflow:hidden}`, so they should never move, but `document.scrollingElement.scrollHeight`
was **4994 px**, not the 800 px of the viewport, so the browser could (and did)
scroll the document itself, taking the nav and the header with it.

The cause was `.visually-hidden{position:absolute}` with **no positioned
ancestor**. An absolutely-positioned box whose containing block is the initial
one is not clipped by a `static` ancestor's `overflow:hidden`; the lesson page
carries four of these (a live-region and three table captions), each sitting at
its static position ~4000–5000 px down the page, so they extended the document's
scrollable overflow by that much. Anything that revealed a focused control, such as 
clicking **Run**, Tab, `scrollIntoView`, then scrolled the shell.

| Probe | Before | After |
| --- | --- | --- |
| `document.scrollingElement.scrollHeight` on `#/course/sqlite-essentials/s1` | 4994 | 800 |
| `scrollWidth` (a closed `.drawer` is `translateX(100%)`) | 1270 | 1280 |
| sidebar `getBoundingClientRect().top` after clicking **Run** | −220 | 0 |

Fix: `.app` is now `position:relative`, so those nodes have a containing block
inside the clipped subtree. `position:relative` with `z-index:auto` creates no
stacking context, so the header (`z-index:20`), drawer (`45/46`) and toast (`80`)
are unaffected: verified with the drawer open, the search panel open and the
theme toggled.

### The nav rail below 760 px was nine anonymous icons

`.brandname,.navtext,.label,.navbadge{display:none}` at `max-width:760px` leaves
a 64 px icon rail, and the icons are `aria-hidden`. Nothing carried the label:
no `text` (hidden), no `title`, no `aria-label`. On a phone the app was nine
unlabelled buttons: unusable rather than merely unlabelled, since there is no
hover on touch to recover the name.

| Probe (390 px viewport) | Before | After |
| --- | --- | --- |
| `button.nav` with a non-empty accessible name | 0 of 9 | 9 of 9 |

Fix: `NavButton` and the recent-chapter buttons in `web/src/components/Shell.tsx`
carry `aria-label` and `title`. Additive: the visible type is unchanged.

### Also driven, no defect found

- Working example runs on `SQLite 3.50.3` through the gateway and `3.53.4` in the
  browser; a correct `ex.s1.1` submission returns **Passed** with the four
  contract clauses itemised; the wrong one returns `ROW_COUNT_MISMATCH`.
- Note → workbook, bookmark → workbook, **Keep query** → saved queries: all three
  round-trip and read back with their chapter number.
- Search returns chapters, exercises, sections and objectives; a section hit
  opens the chapter it belongs to (it lands at the top of the chapter, not at the
  section: the fragment names no anchor, which is a limitation, not a fault).
- The tables drawer is a real modal: `Escape` and the backdrop both close it and
  restore `pointer-events:none`; while open it traps Tab and blocks the page, as
  its own doc comment says.
- Theme cycles light ⇄ dark from the header with the Appearance screen holding
  the third state; Mermaid renders (one 683×615 SVG on `s3`).

### Every lesson rendered the fallback prose: the reading content never loaded

The chapters are authored as markdown, seeded into `app.content_docs` and read
back out of the database: that is what "the database is what the lesson renders"
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
pending after 53 s**: the reads never settled, so there was nothing to retry.

The cause is the engine's identity. PGlite's `PGliteWorker` elects a leader
across every worker that shares an `id`, and the app's ids were constants
(`querycraft-dataset-commerce`), deliberately, so two datasets would not share
one election. Constants also mean two *tabs* share one election: the losing tab
proxies every query to the winner, so when the winning tab is reloaded or closed
the follower waits on a leader that will never answer: forever, silently, and
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
once, and then really runs the seeded dataset (exactly 80 orders), and that the
election id is per dataset *and* per page load, by booting two module instances
and comparing the ids they presented. Both halves were negative-tested: with the
id made constant it fails 1 check, with the main-thread fallback removed it fails
3. It cannot see a real browser's `Worker` or `BroadcastChannel`, so the election
itself is still checked by driving the app; the adapter's decisions are not.

The remaining defects were unreachable from `make test`, because no audit in this
repo renders a page: the audits drive the API, the graders and the content. There is
no Chrome, Puppeteer, Playwright or axe installed here either (`docs/accessibility.md`
records the same constraint), so the two rules are held by a source audit rather
than a rendering one: `scripts/shell-audit.mjs`, wired in as `make audit-shell`
and part of `make test`.

It asserts that the clipping shell is a containing block, that `.visually-hidden`
is still `position:absolute` (so the first rule stays load-bearing rather than
coincidental), that the 760 px block still hides `.navtext`, and that the nav
buttons therefore still carry `aria-label`/`title`. It was verified to fail: with
`position:relative` removed from `.app` and `aria-label={label}` removed from
`NavButton`, it reports both and exits 1.

It proves **neither** that a page is 800 px tall **nor** that a screen reader
announces "Workbook": only that the rules those facts rest on are still in the
source. The measurement stays what it was: a browser drive.

## M5 step board

Plan of record: `docs/m5-implementation-plan.md`. Steps 1–8 done. Steps 1–5 were
the Go track, step 6 the browser track, step 7 the authored `plans` course, and
step 8 the exit gate. The two schemas in step 1 are the frozen seam both tracks
code against.

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | The plan seam | shared | done | `contracts/plan.schema.json`, `contracts/perf-contract.schema.json`, README endpoint map: frozen seam |
| 2 | Engine: capture a plan | backend | done | `engine.Explain` over the `Executor` seam; `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` under the query's own limits; node/byte caps; `timing_precision` |
| 3 | Gateway: `POST /explain` + the `plan` capability block | backend | done | anonymous analyze refused; `ENVIRONMENT_UNAVAILABLE` with no database; OpenAPI + `querycraft_plans_total` |
| 4 | The performance dataset and its template | backend | done | `perf.sql` (2000/500/10000/20000, `generate_series` arithmetic, no `random()`), sealed `qc_perf_template`, deliberate index gap, status/customer skew |
| 5 | Plan-based evaluation | shared | done | `internal/perf/grade.go` (structural codes only; `TestGradeReadsNoTiming` AST-walks the package), 10 shared fixtures incl. 2 captured from a real plan |
| 6 | Frontend: the plan viewer and the perf exercise | frontend | done | `PlanView.tsx`, `explain` on all three adapters, mock refuses honestly, 82 additive CSS lines |
| 7 | The `plans` course, authored | content | done | `p1..p8.md` (757 lines) + `PLANS_MODULES`; the course left preview and is enrolled |
| 8 | Exit-gate audit and release | shared | done | `make audit-perf`: all four clauses green (below); tag waits on the owner |

### M5 evidence

`make audit-perf` (`scripts/perf-audit.mjs`) boots the real binary on its own
embedded PostgreSQL and asserts the four gate clauses against it, validating
every bundle it sees against `contracts/plan.schema.json`:

| Clause | Measured |
| --- | --- |
| Plans are reproducible on pinned fixtures | the same analytic statement planned three times produces **one** structural plan (node types, relations, indexes, row estimates); the excluded fields, `actual_time_ms`, `duration_ms`, `loops`, `buffers_read`, are printed with a reason each, and the observed durations (`23.4, 21, 21` ms) are reported and never compared |
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
| `environment.scale_factor` was hard-coded to `1` in `httpapi/plan.go` | a plan over 10 000 orders reported its scale as 1: the exact overstatement the exit gate forbids, in a field the gate names | `dataset.FactRowCount(id)` (declared per dataset) + `TestScaleFactorMatchesTheSeed`, which counts the rows in both datasets and fails if the declaration drifts |
| `courseForModule` in `web/src/lib/contentSeed.ts` had no case for the `p` prefix | the whole `plans` course's markdown would have been seeded onto `sql-analytics`, so the new lessons rendered the wrong course's content | a prefix table with `p: 'plans'`, plus a content-audit check that every published module maps back to its own course (the old check asserted four hand-written pairs and could not notice a fifth) |
| `lesson-audit.mjs` and `course-audit.mjs` hard-coded their course lists | the `plans` course was never audited: 8 modules and every authored solution unrun, while the audits reported green | both derive their list from `c.COURSES`; lesson coverage went 23 → 31 with 0 problems |
| the audits read a bundle `make` builds into `/tmp` | running a script directly audits the previous revision and passes: which is what happened twice in this session before the bundles were rebuilt | `scripts/lib/bundle.mjs` `assertFreshBundle` compares mtimes and fails with the make target to run; verified by touching `curriculum.ts` and watching it exit 1 |
| the TS grader compared `bundle.environment.engine` while the Go grader compared top-level `bundle.engine` | the browser and the gateway would disagree about `PERF_DIALECT_MISMATCH` for the same bundle | the TS grader now reads the top-level field, matching `internal/perf/grade.go` |
| `make audit-perf` / `make audit-dataset-map` had no targets | the two new exit gates were not in `make test`, so they could not fail a build | both targets added and wired into `make test` |

### Defects found by driving the M5 course (round two)

Every one of these was found by running the app's own course against the
fixture it describes, not by reading code. The `plans` course is the first
course whose lessons state *numbers*, which is what made a whole class of stale
claim visible.

| Defect | What it did | Fix and evidence |
| --- | --- | --- |
| `perf.sql` was authored against an imagined dataset | the seed used `delivered`/`pending`/`refunded` while every authored lesson and result row said `shipped` (9000) / `completed` (400) / `cancelled` (300) / `pending` (250) / `refunded` (50): 11 of 16 authored result rows were false, and `LIKE 'Aurora%'` matched 25 rows where the lesson said 1 | the fixture was rewritten to the taught facts: five status values at exactly that distribution, customer skew 490/430/400/360/340/320/184/156/132/108 then 152 for customers 11–50 and 4 each for 51–300, `customer_id` and `status` seeded from two *independent* permutations so a customer's statuses are a real mix, `ordered_at` strictly increasing, exactly two lines per order, and the verbatim commerce catalogue for ids 1–20. Real captures now pin it: `TestPerfDatasetCarriesTheTaughtFacts`, and `make audit-perf-content` |
| the fixture was too narrow for its own lessons | at ~100 pages for 10 000 rows a 0.5% predicate is genuinely cheaper to read sequentially, so `ex.p4.1` ("the plan must go through `orders_status_idx`") asserted a decision the planner is *right* to refuse | five realistic columns (`carrier`, `tracking_code`, `shipping_address`, `billing_address`, a ~1.3 kB `notes`) make the tuple realistically wide: `pg_relation_size('dataset.orders')` = 20 480 000 bytes = 2500 pages, and `WHERE status = 'refunded'` now plans as `Index Scan on orders_status_idx` (measured in PGlite 18.6) |
| **both engines dropped every bitmap access** | PostgreSQL prints no `Relation Name` on a `Bitmap Index Scan`, the relation is on the `Bitmap Heap Scan` above it, so `summary.index_scans` and `summary.relations` silently lost it. `ex.p8.1`, whose plan performs two index accesses, was graded as having one: `PERF_NODE_MISSING` + `PERF_INDEX_NOT_USED` on a correct solution | `summarizePlan` in `web/src/api/planContract.ts` **and** `backend/internal/engine/explain.go` now reads a relationless node back against its parent's relation (identical walks, one comment each). Pinned three ways: `TestExplainCountsABitmapIndexScanAsAnIndexAccess`, a real captured fixture `contracts/fixtures/perf-bitmap-index-scan.json`, and a new re-derive clause in the conformance audit |
| **the two graders disagreed at the threshold** | `forbid_nodes.above_plan_rows` was exclusive (`>`) in Go and inclusive (`>=`) in TypeScript, so the browser failed a plan the server passed: the same bundle, two verdicts | `planContract.ts` now uses `>`, matching `backend/internal/perf/grade.go`'s explicit comment. The existing fixture `perf-forbid-at-threshold-is-legal.json` is the pin: the Go test passed it while the web grader failed it, which is how the divergence was found |
| five hand-authored fixtures carried summaries no walk could produce | their `plan` nodes never named a relation the stored `summary` claimed, so the fixtures pinned numbers the app never emits: a fixture that proves nothing | `scripts/perf-fixture-normalize.mjs` (`make audit-perf-fixtures`) repairs the *plan* only when the walk cannot reach the claimed relation, so a captured plan stays verbatim, then re-derives `summary`. The web conformance audit now also re-derives every fixture's summary from its own plan and re-grades it by the pinned verdict |
| `ex.p7.1` taught something untrue on this fixture | the lesson said the cast hides the index, but with `ORDER BY id LIMIT 5` the planner walked `orders_pkey` and stopped early: cast or no cast, the plan was `Limit → Index Scan on orders_pkey` and `orders_customer_id_idx` was never used | re-authored to `ORDER BY ordered_at`, which nothing indexes: the fix now genuinely needs `orders_customer_id_idx` (`Limit → Sort → Bitmap Heap Scan`), and the cast genuinely forces `Limit → Sort → Seq Scan`. The exercise discriminates instead of merely being unfailable |
| `ex.p8.1`'s contract over-specified the planner's choice | `require_nodes` demanded a literal `Index Scan` on the inner side of a join, but for two rows the planner legitimately answers with a bitmap on one engine and a plain index scan on another, so the exercise passed or failed by machine | the node requirement is gone; `require_indexes: ['orders_pkey','order_items_order_id_idx']` + `min_index_scans: 2` say the same thing in an engine-independent way (and now count bitmaps) |
| the perf-content audit could not see any of this | its timestamp comparison formatted a `Date` with `String(...)` (a locale string) against the authored `'YYYY-MM-DD HH:mm'`, so no authored timestamp row could ever match; and it read `bundle.dataset` from the bundle root, where the field does not exist: every exercise reported "planned against the small one" while `scale_factor` was 10000 | `wallClock()` formats a `Date` in the same local time the driver parsed it in, `jsValue` writes that form back, and the dataset check reads `bundle.environment.dataset`. `make audit-perf-content` is now green: 16/16 authored rows, 8 prose claims, 4/4 contracts, 4/4 dataset routings |
| **the content-truth rule existed twice and the copies had drifted** | `perf-content-audit` compared an authored timestamp to the engine's by the minute; the new `perf-seam-audit` compared `'2026-12-28 01:48'` to the engine's `'2026-12-28 01:48:00'` strictly and reported three *false* failures (`ex.p3.1`, `ex.p7.1`, `ex.p8.1`), the kind of red that teaches you to ignore the gate | one copy, `scripts/lib/rowtruth.mjs`, imported by both audits. The distinction it documents is the point: the **learner-facing** comparator (`compare.ts` / `compare.go`, design §6.1 rule 7, "no implicit text normalization") must stay strict and is never asked to make `10:12` equal `10:12:00`, because a learner is graded against a reference the *same* engine produced; the **content-truth** comparator asks whether the lesson's claim is true of the fixture, and the engine's text for an instant is dialect-shaped (PostgreSQL prints seconds, SQLite's ISO-8601 text does not, `docs/sqlite-dialect.md` §5). Evidence: `make audit-perf-content` and `make audit-perf-seam` both green on the same rows |

Four new gates came out of this pass, all inside `make test`:
`make audit-perf-content` (the course against the fixture it describes: every
authored row, every number the *prose* states, every perf contract against its
own solution), `make audit-perf-fixtures` (every fixture's summary is the walk of
its own plan), the re-derive clause inside `make audit-perf` (the same document,
both languages, one verdict), and `make audit-perf-seam` (the dataset seam,
driven through the shipping browser adapter: see step 9 below).

### The dataset-routing defect: fixed and verified (M5 step 9)

**Was:** `/explain` routed a perf exercise to the 10 000-row perf fixture, but
`/query` and `/answers/evaluate` always ran against **commerce** (80 orders
anonymously, the learner's own commerce database when signed in). So the `plans`
course was unpassable *in the product* while being provably correct in the
audit: a learner submitting any `plans` exercise was graded against rows from a
different fixture.

**Fixed, two tracks that met in the middle.** A request now carries a `dataset`,
and one resolver decides what it means.

* backend (`Bohr`, complete): `internal/httpapi/dataset.go` is the only reader
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
* frontend (mine, after the `Turing` subagent failed silently): `web/src/lib/dataset.ts`
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
worse than the symptom: had the other worker won the election, perf SQL would
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
| 5 | Per-learner SQLite isolation (D14) | backend | done | ADR `0006-sqlite-learner-isolation.md`: one file per account, reset by re-copy; file-per-account measured live (signed-in `CREATE TABLE` lands in the account file and a second account's identical query is `QUERY_RUNTIME_ERROR: no such table`; the anonymous shared file stays at 30 rows) |
| 6 | Browser SQLite adapter (D12) | frontend | done | ADR `0007-browser-sqlite-engine.md`; `@sqlite.org/sqlite-wasm` 3.53.4 in a Worker (`sqlite.ts` → `sqlite.worker.ts` → `sqliteEngine.ts`), the profile read from `contracts/dialects/sqlite.json`, driver framing stripped in `sqliteFraming.ts`; `make audit-sqlite-errors` green |
| 7 | Dialect badge + actionable refusal | frontend | done | `lib/dialectChoice.ts` (the learner's local override, keyed by module id) + the chapter badge (`.dialbadge` with `data-dialect`/`data-dialect-state`/`data-engine`) and the one-action switch in `SqlCell`; `Schema`/`TablesDrawer` take the dialect through to the seam |
| 8 | Per-dialect parity audit | shared | done | `--dialect postgresql|sqlite` in `scripts/adapter-parity-audit.mjs`; `make audit-parity` runs both and fails if either is red. PostgreSQL 119/119 match; **SQLite 49/49 match, 0 diverge**, 8 exclusions each printed with its reason and verified (rowid, file-layer pragmas, materialised dates). `docs/adapter-parity.md` §"The SQLite dialect" |
| 9 | M6 release | done (tag pending the owner) | `make test` green (21 audit targets, incl. `audit-courses`); `make verify` green, SPA + API + auth + both dialects on one binary, `verify: OK (embedded PostgreSQL :54340, SPA + API + auth on :18080)`; `make verify-mock` green, `OK (no database, honest degradation, auth still enforced)` |

### M6 evidence (measured)

**The browser SQLite engine, driven through the shipping seam** (`preview_evaluate`
against `npm run dev` on :5199, the real `web/src/api/sqlite.ts`):

| Probe | Measured |
| --- | --- |
| boot | 111 ms → `SQLite 3.53.4` |
| a grouped lesson query | 5 rows, columns `{status,TEXT}` and `{orders,expression}`: the declared type verbatim, the profile's own word for an expression |
| syntax errors | `incomplete input`, `near "SELEC": syntax error`, `unrecognized token: "'abc"` all → `QUERY_SYNTAX_ERROR` (this is what the frame-strip exists for: without it every one of them is a runtime error) |
| runtime errors | `no such table: nope`, `UNIQUE constraint failed: products.id` → `QUERY_RUNTIME_ERROR` |
| the deadline | `QUERY_TIMEOUT: interrupted` at the wall clock, and the connection **survives** it (80 rows the next statement) |
| a primary key column | labeled from `sqlite3_column_decltype`, `INTEGER`, not `expression` |

**The lesson loop in a real browser, as a user** (`#/course/sqlite-essentials/s3`):
the worked example ran on the wasm engine (`1 row · SQLite 3.53.4`, `MAX_ID 20 / MAX_ROWID 20 / ROWS 20`), a
correct submission to `s3.1` was **Passed** with the contract breakdown
(columns and order ✓, row count ✓, row values ✓, row order ✓), and a deliberately
wrong submission was **Not yet: row count** with `ROW_COUNT_MISMATCH` and the
recovery controls (hint, solution, accept, report). A fresh load of
`#/course/sqlite-essentials/s3` lands on chapter 3: the deep link is honoured
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
OpenAI-compatible endpoint: set `QC_AI_BASE_URL`, `QC_AI_MODEL`, `QC_AI_KEY_FILE`,
and `QC_GO` if `go` is not on PATH): HTTP 201 `status=active` in 38.2 s on
2026-10-15: the model's bundle passed `validate_artifact`, its 4 setup statements
applied to a scratch database, its reference returned 7 rows, the cross-check
agreed on 7, 3/3 adversarial fixtures were rejected, two repairs were requested,
and the activated artifact is graded by the real evaluator (reference passes,
wrong query does not). Provenance attributes the model, not the transport.

| Live run (`deepseek-v4.1-flash`) | Before | After |
| --- | --- | --- |
| outcome | `AI_REPAIR_EXHAUSTED`, twice, for two different reasons | `status=active`, first attempt, **0 repairs** |
| wall clock to a decision | 21.2 s / 42.0 s to a rejection | 6.7 s to activation |
| setup statements | `[""]`, then `CREATE TABLE customers`, then `DROP` | `[]`: the correct answer over the sealed dataset |
| what the model was told | "the commerce dataset", no columns | every table, every column and its type, from `dataset.Tables` |
| repeatability | 1 active, 2 rejected in the three runs that day | **4 consecutive runs all `active`** (6.7 s, 16.1 s, 8.5 s, 5.4 s; 3 of them with no repair at all) |

Both prompt defects are now gated from `backend/internal/ai/prompt_test.go`
(`go test ./internal/ai/...`): the brief must name every table and every column
the dataset package reports, so a column added to the seeder is required in the
prompt by the same commit, and it must say that a setup list may be empty and
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
   **no** `minItems`: an empty list is the correct answer over the sealed
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
the field named twice: the same doubled prefix would have reached the model on
any setup repair. It is trimmed once, in the caller, the way
`cross_check_solution` already did.

### M6 step 8 notes

**A verification hole found and closed while finishing M6.** `make audit-courses`
(the exercise-solution audit) POSTed `{sql}` and nothing else, so every statement
was run as PostgreSQL over the commerce dataset and the script never set an exit
code. Once SQLite chapters existed that meant 16 exercises reported `BROKEN` for
a reason that had nothing to do with their content, 5 more reported `ok` only
because their SQL was *also* valid PostgreSQL (a false pass), every `plans`
exercise was graded against the wrong fixture, and the whole thing exited 0: it
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
change, and it could not be done through the driver: modernc v1.38.2 has no DSN
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
**51 statements compared: 51 match, 0 diverge, 6 excluded**, the two remaining
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
| 7 | AI-off proof + exit-gate audit | shared | done | `make audit-ai`: all six gate clauses green; `make audit-ai-adapters` inside `make test` |
| 8 | M4 release | shared | done | `make verify` green; **live-provider audit green** (see "Live-provider run") |

## M3 step board

## Live-provider run (real model, end to end)

The M4 exit gate proves the pipeline with a deterministic fake. That is not
evidence the *transport* works, so `scripts/ai-live-audit.mjs` (`make audit-ai-live`)
drives the same pipeline with a hosted OpenAI-compatible model
(`amr-link.open-design.ai`, `deepseek-v4.1-flash`). It asserts the pipeline's
honesty, not a happy path.

**Run 1: failed, and it found three real defects.** The run stopped at
`request_bundle` after 20.0 s with `AI_UNAVAILABLE`, which the audit correctly
reported as an honest refusal. It was not: it was three bugs in the product.

| Defect | What it caused | Fix |
| --- | --- | --- |
| The pipeline's own run budget defaulted to 20 s (`ai.DefaultLimits`) while a single provider call was allowed 45–120 s | the run was cancelled mid-call, so a working model looked unreachable | `DefaultLimits.Timeout` → 120 s; `aiLimitsFromEnv()` derives the run budget from the call timeout × (1 + `MaxRepairs`) and reads `QC_AI_RUN_TIMEOUT_MS` |
| The server's `WriteTimeout` was 30 s | a generation that took longer had its connection closed before the pipeline could answer, turning a stage report into a transport error | `WriteTimeout` → 5 min with the reason recorded inline; read timeouts unchanged |
| `HTTPProvider.userContent` dropped `ProviderRequest.Previous` and `Failure` | the repair pass re-asked the identical question, so the model returned the identical bundle and the budget was exhausted | the repair context is now sent, bounded (16 000 chars for the previous reply, 2 000 for the failure) and clipped on a rune boundary; `TestHTTPProviderCarriesTheRepairContext` and `TestUserContentClipsTheRepairContext` pin it |

**Run 2: green, the model activated a challenge.** `HTTP 201 status=active`,
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
| `repair` | `attempt 2 requested after reference_contract failed`: a real repair, and it worked |
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

- `make test`: frontend typecheck + 9 audit suites + all Go packages, **green**
  (includes `audit-parity`: 61 compared / 61 match / 0 diverge / 1 volatile exclusion).
- `make verify`: build + `make test` + a cold-boot smoke test on an isolated embedded
  PostgreSQL: `/health` → PostgreSQL 18.3, `/query` 200, anonymous `/progress` 401,
  admin login 200, learner signup 201, scoped progress PUT/GET 200, revoked token 401,
  SPA served: **green**.
- `make verify-mock`: honest degraded mode (`/query` → 400 `ENVIRONMENT_UNAVAILABLE`), green.
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
  the query would have, so saturation reports `QUERY_TIMEOUT`: an existing code in the
  closed contract enum, rather than inventing one or queueing unboundedly.
- Two middleware layers (logging and metrics) would each hold their own status and error
  code; they were merged into one so the log line and the counter cannot disagree.

### Step 6 notes (exit-gate proof)

- **6.2 No path runs learner SQL on an administrative connection, proven by behaviour.**
  `backend/internal/httpapi/adminpath_test.go` boots its own embedded cluster, builds a
  real executor on the *learner* DSN, and drives `POST /query`: `current_user` and
  `session_user` both answer `querycraft_learner`; `app.progress`, `public.progress`,
  `pg_read_file`, `pg_ls_dir`, `COPY … TO PROGRAM`, `CREATE ROLE`, `CREATE DATABASE`,
  `ALTER SYSTEM`, `DROP TABLE dataset.orders`, `TRUNCATE`, `SET ROLE` and `pg_authid`
  are all refused with `QUERY_RUNTIME_ERROR` (HTTP 400, never 5xx); the dataset counts and
  the admin-side sentinel are unchanged afterwards. `RESET ROLE` is a documented no-op on
  a learner session (it resets the *current* role to the session user, which is already the
  learner) and the test asserts that instead of a fake error.
- **6.3 Setup works on a clean machine from the documented command, measured.**
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
  (a) The gateway emitted `numeric` as a JSON number, so `118.00` arrived as `118`: 
  the learner's own price lost its scale; `internal/engine/normalize.go` now returns
  PostgreSQL's text for it. (b) A naive `timestamp` was decoded into a time value and
  re-rendered, which moves the wall-clock time by the host's UTC offset (measured:
  `10:30` → `05:00` under `Asia/Calcutta`); the same text rule fixes it, and the browser
  adapter now asks PGlite for the same text via per-OID parsers instead of letting it
  materialise a JS `Date`. (c) The gateway called a missing relation a *syntax* error
  (`42P01`/`42703` were matched by the `42` class prefix) where design §4.2 files
  "missing relation" under `QUERY_RUNTIME_ERROR`, and the browser adapter already said
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
transactions were untouchable, and the curriculum contains *zero* DML lessons, which is
the symptom rather than the fix.

Settled in `docs/adr/0003-writable-learner-workspaces.md`:

- Learner SQL is writable. Signed-in learners get their **own environment database**,
  copied from the sealed template (ADR 0002) and granted `SELECT, INSERT, UPDATE, DELETE`
  plus `USAGE, CREATE` on `dataset`: no `TRUNCATE`, no ownership, no `REFERENCES`, no
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
| anonymous `GET /environment` | `{"ok":true,"environment":{"exists":false,"writable":false}}`: it does not invent a workspace |
| signup → `GET /environment` | lazily created `qc_env_acct_1ed246…`, `exists:true`, `writable:true`, `tables:5`; gateway log `challenge environment ready … created_in=7.0ms` |
| learner write | `UPDATE products SET stock = 0` → `succeeded`; read back `[[1,0],[2,0],[3,0]]` |
| reset | `POST /environment/reset` → `{ok:true, duration_ms:401}`; read back `[[1,40],[2,25],[3,15]]`: the sealed dataset |
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
| `make verify-mock` (honest degradation) | green, `/query` 400 `ENVIRONMENT_UNAVAILABLE`, auth still enforced |
| Adapter parity | 61 compared / 61 match / 0 diverge (`docs/adapter-parity.md`) |
| `make audit-load` | green: concurrency cap proven, pool cross-checked against `/metrics` |
| `make audit-metrics-live` | green: deadline stop → `QUERY_TIMEOUT`, row cap → `truncated` at exactly the cap, both counted |
| `make audit-adversarial-live` | green: ten §13.3 cases, dataset intact after the suite |
| `make audit-courses` (live gateway) | green: **every authored solution in all four courses** runs on the gateway and grades against itself |
| `make audit-load-live` | green: concurrency cap enforced on a running gateway, pool bounded, healthy afterwards |
| Owner UI pass on the built binary | gateway mode, real engine queries, grading, retake, drawer + reset, authoring |
| Version bump + tag `v0.3.0` | **done**: `web/package.json` `0.3.0`, tag created with the owner's go-ahead |

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
  for 4003 ms holding a connection: it now cancels at the cap and returns in 4 ms.
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

### Step 5 notes (D11: template environments)

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
  M4 hook. The learner role can also CONNECT to the metadata database (PUBLIC default): M4
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
  same built-in course: the "new" course showed the old course's blurb and 11% progress.
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
- Preserve the visual design and copy: this is a port, not a redesign.
- All frontend data access goes through `ExecutionService` (`web/src/api/`); never `fetch`
  from a screen, never fabricate query results in the UI.
- The gateway stays honest with no database: `ENVIRONMENT_UNAVAILABLE`, never invented rows.
- TypeScript `strict`; `web` must stay clean under `npx tsc --noEmit`.
- Go over Bun is settled (`docs/adr/0001-backend-runtime-go-not-bun.md`).
- Do not commit or tag without asking.

## Tooling pass: oxlint, anti-slop, React Doctor, and the Node floor (owner request)

The owner asked for five things in one request: lift the Node version, run React
Doctor on this codebase, write the docs to the `technical-writing` and `unslop`
skills plus the Vercel writing guidelines, and carry the learnings into the app's
own prompt. This section records what each one cost and what it found.

### The Node floor is 20.19, and the system Node is older

`react-doctor` requires `^20.19.0 || >=22.13.0`. The machine's system Node is
v20.17.0 at `/usr/local/bin/node`. It is owned by root and was installed by `n`,
so lifting it needs `sudo n 22.18.0`, which an agent cannot run here.

The interactive shell already resolves to v22.18.0 through nvm, because
`nvm alias default 22` is set in `.bashrc`. A login or non-interactive shell does
not, because `.bash_profile` exists and does not source `nvm.sh`. Measured:
`bash -lc 'node --version'` prints v20.17.0 and `bash -ic 'node --version'`
prints v22.18.0. Agent shell tools are non-interactive, so they see v20.17.0.

The repository declares the floor in three places: `.nvmrc` (`22`),
`web/package.json` `engines.node` (`>=20.19`), and `README.md`. The app itself
builds on older Node. The number covers the tooling.

### oxlint, with the anti-slop rules vendored

The frontend had no linter. `web/package.json` carried
`"lint": "echo \"(no linter configured)\""`.

The rules are Dillon Mulroy's `anti-slop`, vendored at
`web/tools/oxlint/anti-slop/` from upstream revision `13909c9e` (64 files, 388 KB,
MIT). Upstream asks to be vendored rather than installed, so a rule can be read
and changed here. `PROVENANCE.md` records the revision.

The plugin holds 23 rules: 18 generic and 5 Effect. At full strength they report
1799 findings. 1292 of those (72%) are `require-readable-spacing`, one formatting
rule that would rewrite the source, and the project has no formatter.

The severities are therefore triaged in `web/oxlint.config.ts`. Every rule that is
off says why in the file.

| Severity | Rules | Findings at full strength |
| --- | --- | --- |
| error | 8 anti-slop generic, 4 anti-slop Effect, 1 oxc | 1 |
| warn | `no-chained-type-assertions`, `no-unknown-returns`, `no-array-filter-map` | 18 |
| off | 7 anti-slop generic, `prefer-effect-match` | 1780 |

The one error was real and is fixed, so `npm run lint` exits 0 with 28 warnings.
`make test` now depends on `make lint`, so a regression fails the suite.

### The one anti-slop error, and what the 12 warnings point at

`src/api/execution.ts` built the capabilities object with
`...(dialects.length > 0 ? { dialects } : {})`. The conditional spread hides
property omission behind an empty object, and `EngineCapabilities.dialects` is
optional for a reason. The object is now built and the property is added when it
is present.

The 12 `no-chained-type-assertions` warnings are real, and they cluster in one
place. Every one is an `as unknown as T`:

- `src/api/ai.ts` lines 299, 400, 443, 445, 945, 954, 964
- `src/api/contract.ts` lines 47 and 50
- `src/api/planContract.ts` line 300
- `src/api/pglite.ts` line 888
- `src/api/sqliteEngine.ts` line 85

The rule's own message names the repair: keep the precise type, or parse
untrusted input at its boundary. `src/api/contract.ts` already parses with Effect
`Schema`, and `src/api/ai.ts` does not. It hand-rolls the decoding of an untrusted
model response with `typeof` and `as`, which is also what the 175
`no-runtime-typeof` sites are. The durable fix is to move those decoders to
`Schema`. Until then the three contract rules stay off, so the 18 actionable
warnings stay visible.

oxlint's own default rules found things the vendored rules do not cover: 7
`new Array(n).fill(null)` sites, 2 useless spread fallbacks, 1 useless spread, 1
unused expression, and 7 empty `catch` bindings. The 7 empty catch bindings, the
useless spread and the unused expression are fixed. The 10 remaining are warnings.

### React Doctor

`react-doctor@0.9.18` is a devDependency of `web` and runs as `npm run doctor`.
It is pinned exactly, and runs with `--no-score`, so the score API, the share URL
and crash reporting stay off.

It scans `src/` only. Passing `.` also scanned the 64 vendored rule files, which
moved the file count from 71 to 137 and added nothing useful. Measured: 71 files
in 3.3 s, 114 issues (4 errors, 110 warnings) across 5 categories.

The cost is size. `react-doctor` and its tree added 228 MB to
`web/node_modules` (358 MB to 586 MB), which is the `@sentry` (47 MB),
`@opentelemetry` (22 MB), `playwright-core` (14 MB) and `typescript` (23 MB)
trees it ships. It is a devDependency, so it does not reach the bundle.

`npm audit` reports 4 vulnerabilities in `vite`/`esbuild` and `mermaid`/`katex`.
Both are pre-existing and neither comes from this dependency.

The findings are hypotheses. The run above, "React Doctor evaluation", holds the
per-finding verdicts. The four errors stay un-suppressed in the config, so the
command exits 1 and a reader has to look at them.

### A defect the writing pass found in the app's own prompt

`src/api/review.go` sent `systemPrompt()` for a hint and for a critique.
`systemPrompt()` says "Reply with one JSON object and no prose". A hint is prose,
and `noteLines` strips the leading dash of each line of a critique, so the
contract for both is plain text.

The built-in provider answers by `Kind` and never reads the system message, so no
test could see the contradiction. A real provider was being told to wrap a hint
in JSON.

`feedbackSystemPrompt()` replaces it for both kinds. It carries the same reading
rules as the course prompt, because this text is also the learner's reading
material. Two tests hold it: `TestTheFeedbackPromptAsksForProseNotJSON` holds the
text, and `TestTutorAndCritiqueSendTheProseSystemPrompt` holds the wiring by
asserting on the request the service built.

### The reading rules reach generated course prose

`courseSystemPrompt()` now states the reading rules. Both course paths share it,
the whole-plan path directly and the one-chapter agent path through
`courseAgentSystemPrompt()`, so one sentence reaches every chapter of every
generated course. `TestTheCourseSystemPromptStatesTheReadingRules` holds it.

The rules are the ones this repository writes by: short sentences, active voice,
present tense, name the table and the column, no slogan that restates a heading.
A model left to itself writes chapter bodies in the register of a product page.

### The writing standard has three named sources

`AGENTS.md` `## Writing` now cites the three published standards it applies,
names the rules that decide most edits, and keeps the rule that a check is
reported from a real run. The sources are the `technical-writing` and `unslop`
skills in `cursor/plugins`, and `vercel-labs/writing-guidelines`.

## Accessibility batch: five fixes, measured (2026-10-16)

Owner request: finish the React Doctor accessibility list. The batch changed
markup and attributes, not the visual design, and every fix was checked in a real
browser rather than read off the source.

React Doctor 0.9.18, `--no-score --verbose src`:

| Category | Before | After |
| --- | --- | --- |
| Accessibility | 21 | 2 |
| Bugs | 34 (4 errors, 30 warnings) | 34 (4 errors, 30 warnings) |
| Performance | 15 | 15 |
| Security | 2 | 2 |
| Maintainability | 17 | 17 |
| Total | 89 | 70 |

The five fixes:

| Fix | Before | After |
| --- | --- | --- |
| `ChallengeLabScreen.tsx` request fields | four `<div className="field"><label>…</label><input>` with no `htmlFor` | the shared `Field` component, which gives the label and the control one `useId` |
| `CreateScreen.tsx` goal and optional fields | a placeholder-only textarea, one unassociated checkbox, two unassociated fields | `aria-labelledby` onto the existing `<h1>`, an explicit checkbox `id` and `htmlFor`, and `Field` for the two optional fields |
| `SearchBox.tsx` | `aria-expanded` on an `input[type=search]`, a role that does not support it | `aria-controls="search-results"` onto the results panel, which states the real relationship |
| Progress and Workbook chapter rows | `<div className="reportrow" onClick>`, unreachable from the keyboard | `<button className="reportrow action">`, plus the `.reportrow.action` reset in `querycraft.css` |
| `PromptDialog.tsx` | no focus trap, so Tab walked into the shell behind the dialog | a capture-phase Tab trap, the pattern `TablesDrawer.tsx` already uses |

The rows kept their shape, but not for free. The check read 32 computed
properties off the old `div` row and the new `button` row. Two differed:
`font-weight`, 500 against 400, and an 8px `border-radius`. Both came from the
bare `button` rule in `querycraft.css`, which `.reportrow` never overrode.
`.reportrow.action` resets both now. The rows then matched on 31 of 32
properties. The last, `text-align: start` against `left`, renders the same in
left-to-right text. The final row still drops its bottom border, because the
`:has` rule handles a second button in the card.

The labels are associated now. `web/src` holds 6 `<label>` elements and all 6
carry `htmlFor`; the `Field` component supplies the rest. The browser resolved
every changed field by its label.

Measured in Chrome 155 over CDP against `npm run dev`:

| Check | Value |
| --- | --- |
| Progress focusable stops | 15, nine of them the chapter rows |
| Workbook focusable stops | 22, nine of them the chapter rows |
| Enter on a Progress row | opens the lesson “Read the data model” |
| Space on a Workbook row | opens the lesson “Read the data model” |
| Prompt dialog Tab cycle | `input → Cancel → Close → input`, never leaving `.modal` |
| Escape and backdrop click | both close the dialog |

`docs/accessibility.md` sections 8 and 9 carry the evidence. React Doctor's
`prefer-html-dialog` finding cleared with the trap, because its stated reason was
the missing trap and not the absence of a `<dialog>` element. It is no longer a
deferral.

Two findings stay, both `no-static-element-interactions` on a modal backdrop
(`Modals.tsx:11` and `PromptDialog.tsx:77`). The click on a backdrop is a pointer
convenience; the Close button, Escape and the new trap are the keyboard path. A
`role` on the backdrop would name it a control it is not, so the finding stays
visible and recorded instead of silenced.

Verified on this tree: `npx tsc --noEmit` clean, `npm run lint` at 28 warnings
and 0 errors, `npm run build` clean, and `node scripts/shell-audit.mjs` reports
OK. The Go suites were not run: a second agent owned `backend/` in the same
checkout.

## UI pass: top-bar shell redesign, 2026-10-17

The learner-facing complaint that drove it: on a 1920px screen, the 244px global
sidebar plus the lesson's 272px chapter column consumed about 40% of the width
before reading began. The redesign replaces both with one fixed top bar.

- `Sidebar` and `Header` are one `TopBar` (`Shell.tsx`): brand, primary links
  with an active underline, a contextual segment on course/lesson screens
  (course, "Ch.1 of 9 · %", chapters drawer button, prev/next through
  `stepLesson` so end-of-course behavior has one owner), the global search, and
  engine/theme/account state. The 244px grid column is gone.
- The chapter list is `ChaptersDrawer.tsx`, a left slide-in drawer; the lesson
  reading column is a single centred 920px column (`.lessonmain`), and
  `.lessonlayout`/`.lessonside` are deleted.
- Below 760px the primary links move into `NavDrawer` behind a hamburger; the
  chapter buttons stay in the bar. Below 600px the search field leaves the bar.
- The collapse ladder (1500/1280/1120px) sheds the engine label, course name,
  and link labels one at a time; `shell-audit.mjs` now holds those rules and the
  icon-only links' accessible names instead of the retired 64px rail.
- Drawer focus mechanics moved to a shared `ui/useDrawerFocus.ts`
  (TablesDrawer's proven pattern: inert, capture-phase Tab trap with an
  `!inside` recapture branch, opener restore with a `document.contains` guard
  and a `#main` fallback). Escape closes one layer per press, topmost first
  (`App.tsx`); the search panel closes itself and stops the event
  (`SearchBox.tsx`, capture-phase `document` listener).
- `DESIGN.md` "Layout" and "Breakpoints" describe the new shell; `shell-audit`
  checks were rewritten for the drawer rules the old rail checks held.

Verified on this tree: `npm run typecheck` clean, `npm run lint` 0 errors,
`npm run build` clean, `make test` exit 0 (1187 checks, including the rewritten
shell audit), `make verify` exit 0. React Doctor still reports
`prefer-html-dialog` on the two new drawers; the verdict stands from the
PromptDialog pass: the capture-phase focus trap is the accepted answer, the
finding is deliberate.

Open items:
- The walkthrough video, its poster, and the highlight GIF still show the old
  shell; the gallery does not. Re-recording needs the scripted browser flow the
  first recording used.
- Focus is not moved to the chapter heading after a drawer-initiated chapter
  change; the drawer's close path restores focus to the opener instead.

## UI pass: README gallery re-shot on the new shell, 2026-10-17

All fifteen gallery screens in `docs/assets/screens/` were captured against the
built binary in gateway mode (`gateway · live` in the bar, the same provenance
the first gallery had), signed in as the seeded administrator, at the preview
capture's 1280×720. The graded-exercise shot (06) was produced by running the
chapter's authored solution through the real Run and Check answer controls; the
tables drawer (09) and the SQLite chapter (07) are the real screens, not
mockups.

Shooting them surfaced a real footgun, now documented: the local cluster's
default paths (`.data/pg/...`) resolve against the working directory the
gateway starts from, so `make serve` (repository root) and `go run ./cmd/api`
(`backend/`) use different clusters, and progress made under one is invisible
to the other. `backend/README.md` and the README's "Try it" now say this, and
`querycraft db status` prints the resolved paths. `make db-reset` now removes
both locations instead of one.

## Manage surface (admin), 2026-10-17

The admin's job is authoring, and the authoring records had no home: a course a
run did not finish was reachable only by remembering it existed, and
/diagnostics answered to nothing. The manage screen (`#/manage`, admin-only,
entered from the bar's Manage link) is the bounded answer: the gateway's own
health panel and the full course inventory, with the learner-analytics question
deliberately deferred until there are learners to measure.

- Backend: `requireAdmin` (403 ADMIN_REQUIRED; the role lives on the stored
  account, signups never receive it), `GET /admin/courses` over the unrouted
  `Depot.Courses()`, documented in the OpenAPI surface and backend/README.
  The detail routes stay owner-scoped (404), so an id still cannot be probed.
- Frontend: `ManageScreen` renders the health rows (build, engine readiness,
  limits, store, dataset and progress versions) and the inventory (status,
  owner, date; Open for active, Resume build for awaiting_approval/building
  through the store's run-breadcrumb resume path, Remove for own records).
  Both adapters implement the seam: the offline one answers null/unavailable
  honestly and the screen says what that means.
- Found live while shooting the screen, and fixed: stores created before a
  column was declared failed every Courses read with "column does not exist"
  (CREATE TABLE IF NOT EXISTS cannot widen a table). Both backends now apply
  additive column migrations on open (ADD COLUMN IF NOT EXISTS on PostgreSQL,
  table_info-compare + ALTER on SQLite), limited to columns that are nullable
  or carry a default; a NOT NULL column without a default is a decision, not a
  migration. Tests cover the legacy shape on both backends. The migration
  surfaced two generated courses from 2026-10-15 that had been invisible to
  GET /courses.

Verified on this tree: go build/vet/test all green (including the two
migration tests), `make test` exit 0 (1187 checks), `make verify` exit 0,
typecheck/lint/build clean, and the screen driven against the built binary
with real stored records.

## Runtime AI provider configuration (admin), 2026-10-17

The provider was boot-only: querycraft.json named it, and changing it meant a
restart. The manage surface now owns it at runtime.

- `GET /admin/ai` reports the effective configuration (provider, model,
  base_url, key_set, timeout_ms) and its source: `database` when an
  administrator saved one here, `boot` while the config file's provider is
  live. `PUT /admin/ai` validates, persists under the store's `ai` key, and
  swaps the running configuration. The key is write-only: it is stored, it is
  never logged and never returned, so every read answers `key_set` and an
  empty save keeps the stored key.
- The swap is a mutex-guarded `ai.Config` holder on the Server
  (`currentAI`/`SetAIConfig`), and every reader — the availability probe, the
  per-request service, the per-job service — goes through it. Requests and
  jobs submitted after the save use the new provider; a run already in flight
  keeps the provider it started with. The run budget follows the boot
  derivation from the saved call timeout (never shorter than one call).
- The boot path applies the persisted setting over the config file's provider
  (`AIConfigFromSetting`), so a save survives a restart; an unreadable row
  keeps the file's provider and says why in the boot log. The config file
  stays the boot document; the store is the runtime one.
- Manage screen: an AI provider panel — provider select (fake /
  openai-compatible / off), base URL, model, write-only key, call timeout —
  with the live source and current values shown, and the offline adapter
  refusing honestly (the panel reads gateway mode only).
- New `app.settings` key/value table (one row per key, value jsonb) across
  PostgreSQL, SQLite and the memory store; a key/value shape on purpose, so
  the next runtime setting is a row, not a schema change.

Verified end to end on the built binary: saving openai-compatible persisted
the row (key included in the store, never in a response), swapped the live
configuration, and a gateway restart applied it from the persisted setting
(boot log names the provider and model). `make test` exit 0 (1187 checks,
including the three new handler tests), `make verify` exit 0.

## UI drive: the whole product as a learner, 2026-10-17

Every learner-facing interaction was driven in the real app — the built binary
in gateway mode, a fresh learner account, desktop and a 375px phone viewport —
not read from code. The pass covered: auth (tabs, empty-submit validation,
wrong-password 401, signup, re-login, sign-out with token cleared), the top
bar (all five destinations with active underlines, Cmd/Ctrl+K search with 25
hits on "joins", theme persistence), the course screen's chapter index, the
lesson loop (Run on an example with a real result grid; a wrong-answer
execution error reported as QUERY_RUNTIME_ERROR with "not a wrong answer";
a wrong-result attempt graded with per-clause feedback, COLUMN_MISMATCH;
authored hints; Show solution's reference panel; Accept recorded as
accepted_by_user, not passed; the real pass with four clauses, Recorded and
the toast; Keep query), bookmarks, the note prompt, Mark chapter complete
with the progress readout moving, the chapters drawer (focus trap, jump,
Escape), prev/next in the bar, the tables drawer with Reset workspace, the
workbook (stats, notes & bookmarks, saved queries with Run and Copy,
Export/Import), the progress history (immutable attempts, per-exercise
versions, weekly bars), the challenge-lab surface, and the mobile shell
(hamburger drawer with chapter moves, chapters drawer, lesson rendering and a
real SQL run with no horizontal overflow).

One defect was found and fixed in the drive: keyboard focus was lost after a
drawer-initiated chapter jump — the drawer's close path restored focus to the
opener (the bar's button on the screen just left), so a keyboard learner
landed nowhere. The jump now moves focus to the lesson's main region, which
is also the skip link's target.

Two deliberate behaviors a driver could misread as bugs, recorded here so the
next pass does not re-litigate them: Cmd/Ctrl+K closes an open drawer before
focusing search (the drawers yield to search by design), and one Escape press
closes one layer, topmost first. A third, cosmetic and pre-existing: the SQL
cell's Ctrl+Enter chip is visible on touch screens where it is noise.

## Instance configuration: live AI provider, 2026-10-17

This deployment's AI provider is configured through the manage surface's own
runtime path: openai-compatible at the operator's model gateway, model
deepseek-v4.1-flash, key stored (write-only, never in a response body). Proof,
not intention: `/capabilities` answers available with that provider and model,
and a full challenge generation ran through the real pipeline with it —
validated, executed in a scratch environment, cross-checked, adversarial
fixtures, stored, and active ("Customer completed-order totals").

A second OpenAI-compatible credential (cline-pass) exists in the operator's
local opencode credential store and can be switched to from the manage panel
when its base URL is supplied; it is deliberately not recorded here in any
usable form, and the key is not in this repository.

## Wire layer adopted: pi/ai behind the provider seam, 2026-10-17

The spike the provider discussion called for is in. `internal/ai/provider/piai`
implements the two-method seam over github.com/sky-valley/pi/ai — pi's unified
multi-provider LLM API ported to Go. What the gateway no longer owns: the
Anthropic, OpenAI completions/responses and Gemini wire adapters, SSE parsing,
Retry-After-aware retries, the embedded model catalog and token accounting.
What stays ours: the pipeline, the repair rules, the schema enforcement, the
redaction, the key handling, and the deterministic fake provider for CI.

- Two modes. A catalog ref (`provider/model`, e.g. `anthropic/claude-sonnet-4-5`)
  resolves against the embedded catalog and serves any of its providers. A
  base_url targets any custom OpenAI-compatible server (a model gateway, vLLM,
  llama.cpp) with the model id that server knows. Both arrive through the
  manage surface's `pi-ai` provider kind, persisted and hot-swapped like the
  others.
- The mapping is tested offline: a scripted Api provider registered under a
  test-only name proves the request mapping (system prompt on the transcript,
  UserContent's repair context and schema instruction, the course agent's
  tool-call/tool-result turns), the response mapping (text, parsed tool calls,
  token counts) and the error path — no network.
- Found live, fixed: pi's catalog adapters register in init via a blank import
  of ai/providers, the documented contract for using ai without pi's coding
  package. The first live call answered "No API provider registered for api:
  openai-completions"; the blank import fixed it, and the next tutor call
  graded a real attempt through deepseek with actionable feedback.
- Cost, stated plainly: the backend Go floor is now 1.26 (the port declares
  go 1.26; the machine toolchain was upgraded and the READMEs updated), and a
  fast-moving third-party module sits in the grading path — contained behind
  the two-method seam, where a revert or replacement is one package.

Verified: go build/vet/test green under Go 1.26.9 (adapter tests + the full
suite), `make test` exit 0 (1187 checks), `make verify` exit 0, and a live
tutor completion through deepseek-v4.1-flash via the pi-ai provider.

## Provider selection made easy, 2026-10-17

The manage surface's AI panel carried the pi-ai kind, but its model field was
free text — and driving it as the admin caught a real bug before it bit: the
response parser coerced any provider it did not recognize to 'off', so a
stored pi-ai configuration rendered as off, and one Save away from writing
off. The parser now knows pi-ai, and the round-trip is verified: a stored
pi-ai configuration renders as pi-ai with the model and base URL prefilled and
the key write-only.

Selection itself is now catalog-backed: GET /admin/ai/models serves the
embedded pi catalog — 1,529 chat models across 41 providers, the same list the
provider resolver matches model refs against, sourced from models.dev — and
the model field offers it as a datalist picker when pi-ai is selected. A
customer who wants any provider, any model types two characters and picks.
