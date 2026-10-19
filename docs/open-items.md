# Open items

The work that is left, in the order the expert set. Direction lives in
`docs/reviews/2026-10-18/expert-advice.md`; when a decision is needed, read it
there first. Status: `[ ]` todo, `[~]` doing, `[x]` done.

## Done

1. `[x]` **Persist edits to a stored course.** `PUT /courses/{id}` in
   `backend/internal/httpapi/routes.go` (owner-scoped, four refusal cases in
   `backend/internal/httpapi/course_edit_test.go`), wired through
   `web/src/api/ai.ts` and `web/src/state/store.tsx`. `make test` and
   `make verify` green.
2. `[x]` **"Report a problem" and "Save challenge".** Already done by deletion:
   the affordances were removed in the web cleanup. The stale bullets were
   removed from `docs/features.md` section 10 on 2026-10-18.
3. `[x]` **Lab step 4 wording.** The authored draft is validated, not
   registered, and it has no version. Fix folded into item 7's sweep.

## Next

4a-i. `[x]` **Ask Tutor backend.** Extended `POST /tutor` into a conversation:
   the gateway runs the learner's SQL and the reference, assembles the bounded
   context (`backend/internal/httpapi/tutor.go`), adds `MaxTutorTurns` 8 /
   `TutorTimeout` 30 s / `MaxTutorContextBytes` 32 KiB, a per-account token
   bucket answering `AI_RATE_LIMITED` 429, and arms `guardSolutions` with the
   request's own reference. Policy recorded in ADR 0008, §11.2, §6.4,
   `features.md` and `contracts/ai.schema.json`. The leak guard is
   `TestTutorPromptCarriesTheExpectedResultNotTheReference`. `make test` and
   `make verify` green. Run: tutor-backend. Spec:
   `docs/reviews/2026-10-18/expert-tutor-drawer.md`.
4a-ii. `[x]` **Ask Tutor drawer (web).** `web/src/components/TutorDrawer.tsx` and
   `web/src/components/ui/useTutorChat.ts`; the `Ask tutor` button in
   `web/src/components/SqlCell.tsx`'s runrow, `tutorTarget`/`openTutor`/`closeTutor`
   in `web/src/state/store.tsx`, one `App.tsx` Escape branch, and the `.tutor*`
   block plus the 760px bottom sheet in `web/src/styles/querycraft.css`. Five
   states (empty, thinking, answering, error, rate-limited; `AI_UNAVAILABLE` is
   the error variant); session chat keyed by exercise id, context and tables from
   the gateway's response. `make test` and `make verify` green (run:
   tutor-drawer-web-2).

4. `[x]` **Split `course_agent.go`** into `course_agent_outline.go`,
   `course_build.go`, `course_prompt.go` and `schemas.go` (expert-advice 3a);
   the `Progress` shadow state now derives from `pipeline.build`. `setVerified`
   was already gone. `make test` and `make verify` green.
5. `[x]` **BuildingScreen reducer.** `useGenerationRun` in
   `web/src/components/screens/useGenerationRun.ts` holds the pure `RunState`
   reducer, the phase union and the one entry effect; `poll` and `settlePhase`
   live pure in `web/src/lib/generationPoll.ts`. The log is `LogLine[]` through
   `web/src/components/ui/GenerationLog.tsx`, so `BuildingScreen.tsx` and
   `ChallengeLabScreen.tsx` drop `esc` and `dangerouslySetInnerHTML`. `make test`
   and `make verify` green.
6. `[x]` **Course setup on course open.** `POST /environment/prepare` in
   `backend/internal/httpapi/routes.go`, handled by `handleEnvironmentPrepare`
   (`environment.go`); the once-per-generation mark is `envdb.Manager.PrepareCourse`
   and `envdb.SQLiteEnvironments.PrepareCourse`, cleared by reset. Web:
   `warmUpCourse` (`web/src/lib/courseWarmup.ts`) called from the course-change
   effect in `store.tsx`; a refusal is a toast and never blocks navigation.
   Tests: `TestEnvironmentPrepareAppliesCourseSetupOnce`,
   `TestEnvironmentPrepareNoWorkspaceAnswersUnavailable`, the warm-up checks in
   `web/scripts/course-store-audit.mjs`. `make test` and `make verify` green.
7. `[x]` **"Practise this now" routing.** `blockHome` in `web/src/lib/model.ts`
   maps a block id to `{courseId, moduleIndex}`; the lab's button
   (`ChallengeLabScreen.tsx`) opens the reference's own chapter through
   `s.openLessonIn` and toasts on a miss instead of `resumeCourse`. Step-4 copy
   no longer claims a registration or a version. `make test` and `make verify`
   green.
8. `[x]` **Memoize `Markdown`.** One query result cost 6.8 ms and a search
   keystroke 13.2 ms of re-parsing
   (`web/src/components/ui/Markdown.tsx`). The boundary is a deliberate
   `memo` comparator over `source` and `className` only, the two string props
   both callers pass. `npm run typecheck`, `npm run lint`, `make test` and
   `make verify` green.

## Then

9. `[x]` **AI call-timeout one home.** The number lives once, as
   `ai.DefaultCallTimeoutMS` (`backend/internal/ai/api.go`). `config.Defaults()`
   reads it, and the admin save path fills it when a setting omits the timeout
   (`backend/internal/httpapi/admin_ai.go`), so the boot path and the manage
   surface cannot drift. Pinned by `TestAdminAISaveValidatesAndMasksTheKey`;
   `make test` and `make verify` green.
10. `[x]` **`piai.CatalogEntry` JSON tags.** Tags `provider`/`model`/`name`
    on the struct (`backend/internal/ai/provider/piai/pi_ai.go`), so
    `handleAdminAIModels` returns `piai.Catalog()` directly and drops its
    duplicate `entry`. The wire was already lowercase through that local struct,
    so the client (`web/src/api/ai.ts:912`) and the `AdminAIModelList` spec need
    no change. Pinned by `TestAdminAIModelsUseLowercaseWireKeys`; `make test` and
    `make verify` green.
11. `[x]` **Move the admin provider helpers** (`providerFromAISetting`,
    `AIConfigFromSetting`) out of `httpapi`. Now `ProviderFromAISetting`,
    `AIConfigFromSetting` and the persisted `Setting` shape in
    `backend/internal/aiconfig/aiconfig.go`; callers are `handleAdminAISave`
    (`admin_ai.go`) and `cmd/api/main.go`. No cycle: `aiconfig` imports `ai`,
    `ai/provider` and `piai` only, and none import `aiconfig`. Pinned by
    `TestAdminAISettingSurvivesBoot`; `make test` and `make verify` green.
    Run: provider-helper-move.
12. `[x]` **Move `login_throttle.go`** from `httpapi` to `internal/auth`
    (`auth.Throttle`/`NewThrottle`/`Allow`/`Fail`/`Reset`/`Size`/`LoginKey`/`ClientIP`
    and the three `DefaultLogin*` constants in
    `backend/internal/auth/login_throttle.go`); `config.Defaults()` reads them
    and drops its `httpapi` import, and `server.go` plus `handleLogin` call the
    auth API. `make test` and `make verify` green. Run: throttle-move.
13. `[x]` **Eleven perf fixtures** carried the removed `definition` key
    (`contracts/fixtures/perf-*.json`). Removed it from all eleven and from the
    `indexes[]` item in `contracts/plan.schema.json`.
    Evidence: `grep -rn '"definition"' contracts/` is empty; Go has no
    `PlanIndex.Definition` (`backend/internal/perf/plan.go:92-95`); no script or
    web consumer reads the value (`make audit-perf-fixtures`, `make audit-perf`
    (11 fixtures), `go test ./internal/perf/ -count=1`, `make test` and
    `make verify` all green). Run: fixture-definition.
12a. `[x]` **Parallel-gate flakes.** (run: flake-fix, finished by the orchestrator after two T3 restarts cancelled the executor) Four signals: the perf first-copy
    "conn closed", two flakes that passed on rerun, a leaked postmaster in
    `/tmp/qc-run-*`, and a smoke port collision. Evidence: `envdb` gives
    provisioning its own 2-minute bound apart from the statement deadline and
    retries a dead admin connection on a fresh one (`withAdmin`, pinned by
    `TestProvisioningRecoversFromAClosedAdminConnection` and
    `TestProvisioningGetsItsOwnDeadline` in `manager_recover_test.go`); the
    retry band-aid is gone from `dataset_test.go`; `gateway.mjs` kills registered
    children before removing a run root (a SIGKILLed audit survives without a
    postmaster, proven by killing a live `metrics-audit`) and restarts a gateway
    whose probed port was taken; the smoke target retries the next candidate
    port; `make test` exit 0 and `make verify` exit 0.
13a. `[x]` **Dead `definition` on the web plan path.** (claimed 2026-10-18 18:26, run: web-definition) `planContract.ts`
    declares and decodes it and `pglite.ts` writes it, but no consumer reads
    the value (`PlanView` renders `name`/`relation`). Drop the writer and the
    decoder fields.
    Evidence: `rg 'definition' web/src web/scripts scripts/` finds only prose;
    `PlanIndex` and `PlanEnvironmentSchema` drop it and the `pg_indexes` SELECT
    drops `indexdef`; `npm run typecheck`, `npm run lint`, `make audit-perf-seam`,
    `make audit-perf-content`, `make audit-perf`, `make test` and `make verify`
    all green. Run: web-definition.
14. `[x]` **Five test-only `dialect` exports** (`Names`, `ByName`,
    `AffinityClasses`, `Affinity`, `ErrorCodes`) plus the unused `EncodeValue`
    arms: deleted from `backend/internal/dialect/dialect.go`, `sqlite.go` and
    `dialect_test.go`; `docs/sqlite-dialect.md` updated; `make test` and
    `make verify` green.
15-i. `[x]` **Store split: the hash router.** `web/src/state/useHashRouter.ts` owns
    `initialRoute`, `applyHash`, `showScreen`, the `gated` role check, the
    state ⇄ fragment effects and the `hashchange`/`popstate` listener;
    `StoreProvider` keeps `screen`, `challengeId`, `activeLesson`,
    `pendingCourse`, `uiState`, the refs and `saveUiState` and passes them in,
    so `StoreValue` is unchanged. `npm run typecheck`, `npm run lint` (28
    warnings, unchanged), `make audit-shell`, `make test` and `make verify`
    green.
15-ii. `[x]` **Store split: progress.** `web/src/state/useProgressStore.ts` owns
    the load and migration, `commitProgress` and the owner-scoped persistence,
    the gateway sync and its failure notice, and the events, notes, saved
    queries, bookmarks, study time, chapter completion, sample data and
    export/import mutations. `StoreProvider` keeps `progress`, `progressRef`,
    `hydratedRef`, `dirtyRef`, `progressOwnerRef` and the shared `course`,
    `modules` and `activeLesson`, and passes them in, so `StoreValue` is
    unchanged. `npm run typecheck`, `npm run lint` (28 warnings, unchanged),
    `make audit-progress`, `make audit-course-store`, `make test` and
    `make verify` green. (run: store-split-2)
15-iii. `[x]` **Store split: catalog.** `web/src/state/useCatalog.ts` owns the
    stored-course list read and mirror (`materialise`, `applyStored`,
    `storedRef`), the fragment resolve, `deleteCourse`, `adoptStoredCourse` and
    the `catalogVersion`/`bumpCatalog` re-render trigger. `StoreProvider` passes
    `pendingCourse`, the session and the selected course in, and keeps
    `courseResolving`, `deleteCourse` and `adoptStoredCourse` in `StoreValue`.
    Evidence: the banner's old "catalog mutation (notes, bookmarks, saved
    queries, workbook)" line was a phantom — those mutations already live in
    `useProgressStore.ts` (`useProgressStore.ts:534-548` returns `addNote`,
    `toggleBookmark`, `saveQuery`, `removeSaved`, `removeWorkbookItem`,
    `isBookmarked`) — so the banner now maps the real catalog concern. The
    moved surface pins to `useCatalog.ts`: `catalogVersion` (`:88`),
    `storedRef` (`:93`), `bumpCatalog` (`:95`), `materialise` (`:109`),
    `applyStored` (`:123`), `deleteCourse` (`:206`) and `adoptStoredCourse`
    (`:243`). `npm run typecheck`, `npm run lint` (28 warnings,
    unchanged), `make audit-shell`, `make audit-progress`, `make
    audit-course-store`, `make audit-content`, `make test` and `make verify`
    green. (run: store-split-3)
15-iv. `[x]` **Store split: theme + motion.** `web/src/state/useTheme.ts` owns
    the device `theme`/`motion` preferences and their persistence, the
    user-defined theme list and its ref, the account preference read/write
    (`api.preferences`/`savePreferences`) and the three document effects
    (`injectUserThemes`, `data-theme`, `data-motion`). `StoreProvider` passes
    only `api`, `authReady` and `session` in and spreads the returned members,
    so `StoreValue` is unchanged; the value's old spurious `effectiveTheme`
    dependency is gone. Evidence: `make audit-themes` (18 themes, 252 pairs,
    OK); `npm run typecheck`, `npm run lint` (28 warnings, unchanged),
    `make test` and `make verify` green. (run: store-split-4)
15-v. `[x]` **Store split: auth + session.** `web/src/state/useAuth.ts` owns the
    stored-session restore (`api.me()` plus `readAuthToken`, the `pglite`
    `LOCAL_SESSION`), the `session` state and its `sessionRef`, `authReady`, the
    expired-session `authNotice`, the session-derived `role`, and
    signup/login/logout/onUnauthorized. `StoreProvider` keeps the cross-concern
    wiring and passes it in: `goHome` (the router's `setScreen`) and
    `resetHydration` (the progress guards `hydratedRef`/`dirtyRef`), so login,
    logout and the 401 land on Home and reset the guards exactly as before. The
    adapter is created first and takes a stable delegate
    (`authRef.current?.onUnauthorized()`), so its identity is unchanged and the
    hook can rebuild the handler. `useAuth` returns those members, `StoreValue`
    is unchanged and the store forwards them. The tracked store-split series
    (15-i … 15-v) is complete: the router, progress, catalog, theme + motion and
    auth + session each live in their own module. Evidence: `npm run typecheck`,
    `npm run lint` (28 warnings, unchanged), `make audit-shell`, `make
    audit-import`, `make test` and `make verify` green (the second `make verify`
    used `SMOKE_PG_PORT=54555 SMOKE_ADDR=127.0.0.1:18300` after a flaky
    `audit-perf-content` under the concurrent suite). (run: store-split-5)

16-i. `[x]` **`ai.ts` decoders: group 1.** Moved the model catalog
    (`GET /admin/ai/models`) and the tutor response (`asTutorResponse`,
    `asTutorContext`) to `effect/Schema` in `web/src/api/ai.ts`, following
    `web/src/api/contract.ts`; `AiServiceShape` and every exported signature are
    unchanged, and no `typeof` or assertion chain is left in those shapes.
    Evidence: `npm run typecheck`, `npm run lint` (27 warnings, down from 28),
    `make test` and `make verify` green; `make audit-ai-adapters` OK. Two further
    groups proposed in the run report. (run: decoders-1)
16-ii. `[x]` **`ai.ts` decoders: group 2.** Moved the outcome and artifact tree
    (`asStages`, `asAttempts`, `asEvidence`, `asAiArtifact`, `asAiError`,
    `asAiOutcome`, `asAiRecord`/`asAiRecords`, plus `asAiAvailability`,
    `availabilityFromCapabilities` and `asAdminAIConfig`) to `effect/Schema` in
    `web/src/api/ai.ts`. Every decoder in the inventory was present and still
    hand-written. New shared leaves: `AiResultContractSchema`,
    `AiSetupPlanSchema` and `AiCheckSchema`; the artifact reuses group 1's
    `AiProvenanceSchema`. A malformed artifact still reads as absent, never a
    spread-through half-object, and the readable-failure paths are unchanged.
    `AiServiceShape` and every exported signature are unchanged. Evidence:
    `npm run typecheck`, `npm run lint` (26 warnings, down from 27), `make test`
    and `make verify` green; `make audit-ai-adapters` OK. (run: decoders-2)
16-iii. `[x]` **`ai.ts` decoders: group 3.** The course/plan decoders, the job
    decoders and guards, `asCritiqueResponse` and `asDefectVerdict` moved to
    `effect/Schema` in `web/src/api/ai.ts`, reusing group 2's shared leaves
    (`AiResultContractSchema`, `AiSetupPlanSchema`, `AiCheckSchema`,
    `AiProvenanceSchema`) and the optional/`stringOr` helpers, now defined once
    above the M7 section. The envelope readers moved to `Schema` too, so no
    `typeof` is left in the file. `isJobStageState` folded into the
    `Schema.Literals` stage-state schema; `isTerminalJobStatus` reads the same
    literal schema through `Schema.is`. `AiServiceShape` and every exported
    signature are unchanged, and both AI audits pass unchanged. The four
    contract rules held off for this gap are re-enabled as warnings in
    `web/oxlint.config.ts`: the hand-rolled parse is gone, and what they still
    report in the file (43 `unknown` entry-point parameters, one JSON-object
    predicate) is the boundary's own shape, counted with the 231 sites of the
    same pattern outside `ai.ts`. Item 16's split (16-i, 16-ii, 16-iii) is
    complete. Evidence: `npm run typecheck`, `npm run lint` (296 warnings, 0
    errors), `make audit-ai-adapters` and `make audit-course-store` OK,
    `make verify` green, and `make test` green on the rerun (the first run failed
    once on the known `audit-perf` "conn closed" provisioning flake under load;
    `make audit-perf` alone and the full `make test` rerun both pass). (run:
    decoders-3)
16a-i. `[x]` **Warnings: the HTTP transport.** (run: warn-burn-1; finished 2026-10-19) `web/src/api/execution/http.ts` and `shared.ts`: the hand-rolled narrowing moved to `effect/Schema` decoders at the boundary (the paused work), the outgoing body is now a generic parameter so each caller's domain schema owns its shape (`object` is banned by the error-level `no-object-parameters`), and the two arbitrary-JSON preferences sites keep `Record<string, unknown>` with a reasoned inline disable. Evidence: `npm run typecheck` clean; `npm run lint` exit 0, 246 -> 236 warnings (http.ts 2 -> 0, shared.ts 3 -> 0, types.ts 3 -> 0 targeted; `ai.ts` 204 and 993 -> 0 as the generic body replaced its `unknown`/`object`); `make audit-ai-adapters`, `make audit-course-store`, `make audit-evaluate-wire` and `make audit-perf` OK; `make test` exit 0; `make verify` exit 0 (embedded PostgreSQL :54340, SPA + API + auth on 127.0.0.1:18080).
16a-ii. `[x]` **Warnings: the plan and grading cluster.** (finished 2026-10-19, run: warn-burn-2) `planContract.ts`, `compare.ts` and `contract.ts`: the wire bodies decode at the boundary (JSON typed `Schema.Json`, arrays made mutable so nothing is asserted back, row cells decoded to `RowValue`), the walk's `typeof` checks are `Schema.Json`-domain type guards, and the three irreducible `unknown` boundaries (a caught `SchemaError`, its issue tree, the engine's raw plan) carry reasoned inline disables. Evidence: `npm run typecheck` clean; `npm run lint` exit 0, the three files 45 -> 0 (planContract 20, compare 19, contract 6), whole run 246 -> 201 warnings and 0 errors; `make audit-perf`, `make audit-perf-content`, `make audit-perf-seam`, `make audit-fixtures` and `make audit-ai-adapters` OK; `make test` exit 0; `make verify` exit 0 (embedded PostgreSQL :54340, SPA + API + auth on 127.0.0.1:18080).
16a-iii. `[x]` **Warnings: the engine adapters.** (finished 2026-10-19, run: warn-burn-3) `pglite.ts`, `sqliteEngine.ts` and `sqlite.worker.ts`: the Postgres error and the engine failure decode with `effect/Schema` into the named `SqlState`/`EngineFailure` contracts, the plan value is `Schema.Json | null`, the result rows narrow through `Array.isArray` rather than a chained assertion, the wasm boot binds the call signature its shipped code has instead of casting twice, the worker's reply is a named union, and the two irreducible boundaries (a raw engine cell, a thrown engine value) keep `unknown` with a reasoned inline disable. Evidence: `npm run typecheck` clean; `npm run lint` exit 0, the three files 43 -> 0 (pglite 24, sqliteEngine 14, sqlite.worker 5), whole run 201 -> 158 warnings and 0 errors; `make audit-parity` (both dialects), `make audit-boot`, `make audit-sqlite-errors` and `make audit-perf` OK; `make test` exit 0; `make verify` exit 0 (embedded PostgreSQL :54340, SPA + API + auth on 127.0.0.1:18080).
16a-iv. `[x]` **Warnings: lib, screens, then the ledger.** (run: warn-burn-4, finished by the orchestrator after a T3 restart cancelled the executor) The executor fixed the 113 findings across the lib, state and screen files; the residual is 44 `no-unknown-parameters` in `src/api/ai.ts`, every one a decoder entry point that accepts `unknown` and returns a typed value (the rule's known false positive), with one real site fixed alongside (`outcomeFromError`'s `cause`). The ledger now records the final numbers in `web/oxlint.config.ts`, `docs/features.md` section 10 and L3: 296 warnings at re-enable, 44 today, 0 errors. Item 16a is complete. `make test` exit 0, `make verify` exit 0.
16b-i. `[x]` **Shared per-package PostgreSQL fixture.** (run: shared-pg-1) `pgtest.Main(m)` + `pgtest.Shared(t)` boot one cluster per package binary and hand each test a database copied with `CREATE DATABASE ... TEMPLATE`; a leftover-session terminate plus a bounded retry covers pgx probes that linger past a loaded boot. The orphan hardening is a detached watchdog that kills the postmaster and removes the cluster when the test binary dies: proven by SIGKILL (postmaster and root gone in 5 s). `internal/ai` and `internal/ai/jobs` migrated (`helpers_test.go`, `jobs_test.go`); the package fell from ~62 s of per-test boots to ~5 s. `make test` exit 0.
16b-ii. `[x]` **Migrate `internal/store`.** (run: shared-pg-1) Eight call sites in `challenges_test.go`, `courses_test.go`, `runs_test.go`, `migrate_test.go`, `sqlite_test.go`, `store_parity_test.go` and `schema_parity_test.go` moved to `pgtest.Shared`; the three `SurviveARestart` tests keep `pgtest.Start` because they stop and restart their server. `store_test.go` gained the package `TestMain`. `go test ./internal/store/` falls from ~30 s to ~5 s; `make test` exit 0.
16b-iii. `[x]` **Migrate the rest and settle the spike.** (run: shared-pg-1)
    Migrated to `pgtest.Shared`: `internal/engine`
    (`encoding_test.go`, `pgx_writable_test.go`), `internal/httpapi`
    (`environment_test.go`, `environment_setup_test.go`, `dataset_test.go`,
    `jobs_test.go`, `adminpath_test.go`), `internal/dataset`
    (`perf_test.go`, `scale_test.go`, `writable_test.go`), `internal/envdb`
    (`manager_test.go`, `manager_recover_test.go`, `perf_test.go`) and
    `cmd/api` (`reap_test.go`); each package gained a `TestMain` through
    `pgtest.Main`. Kept `pgtest.Start` where sharing genuinely breaks the test:
    the three `internal/store` `SurviveARestart` tests (they stop and restart
    their server) and `internal/envdb/manager_close_test.go` (`Close`
    deliberately abandons a half-built environment database, so that leak must
    die on a dedicated cluster). Retired the direct `localdb.Start` copies in
    `dataset/writable_test.go` and `httpapi/adminpath_test.go` (the latter lost
    its private free-port, binary-cache and quiet-logger helpers), and deleted
    the `TestTemplateEnvironmentSpike` measurement, whose finding is in
    `docs/adr/0002`; the two helpers `writable_test.go` uses stay in
    `dataset/template_test.go`. `backend/README.md` gains "The test fixture":
    `pgtest.Start` vs `pgtest.Shared` and the SIGKILL watchdog. Evidence:
    `gofmt`, `go build ./...`, `go vet ./...` and
    `go test ./internal/... ./cmd/... -count=1` all clean; `make test` exit 0 and
    `make verify` exit 0 (smoke on :54340). Item 16b (16b-i, 16b-ii, 16b-iii) is
    complete.
17. `[x]` **apidocs uncertain set.** (claimed 2026-10-18 21:58, run: apidocs-final-2, resumed after a T3 restart cancelled apidocs-final; its partial spec edits are preserved) Six items in
    `docs/reviews/2026-10-18/wave2/apidocs.md`; needs the `operationId` call.
    Evidence: `TestOperationIDsFollowTheRouteTable` derives all 43 ids as
    `lower(method)_<route operation>`; the `Exercise` schema names only the three
    fields the evaluator reads; `patchDecision`/`AIPatchDecision` require only
    `approved` (pinned by `TestPatchDecisionIgnoresBodyIdentity` and
    `TestPatchApprovalLoadsThePatchFromThePath`); `AdminAIUpdate` lists the four
    accepted aliases; `exercise_count` 0/41 is pinned by
    `TestNormalizeRequestClampsAndDefaults`; the inert `steps` field is gone.
    `make test` and `make verify` green.
17a. `[x]` **HTTP-mode grading sends the wrong contract key.** (claimed 2026-10-18 22:24, run: evaluate-wire) Item 17 proved the evaluator reads `result_contract`; the http adapter still sent `exercise.contract` at `web/src/api/execution/http.ts:425`, so the released single binary graded every submission against a zero contract (column order, row order, duplicates and tolerance all ignored). Fixed: `checkAnswer` sends `result_contract`, and the page engine compares in-process, so it needed no change. `make audit-evaluate-wire` (`scripts/evaluate-wire-audit.mjs`) reads both shipping sources and fails unless the web body has `result_contract:` and `reference_solution:`, the adapter has no bare `contract:` key, and `eval.Exercise.Contract` is tagged `json:"result_contract"`. Evidence: `make test` (evaluate-wire-audit: OK) and `make verify` green.
18. `[x]` **`/runs` screen.** (run: runs-screen, finished after a T3 restart cancelled the executor) `web/src/components/screens/RunsScreen.tsx` is the owner-scoped reader: the list and the inline transcript load through `listRuns`/`getRun` in the AI seam, decoded with `effect/Schema` against `contracts/run.schema.json`; `#/runs` in `web/src/lib/route.ts`, entry in `ManageScreen.tsx`. `web/scripts/ai-adapters-audit.mjs` pins the run decoders and the offline adapter's honest empty answer. `make test` and `make verify` exit 0.
19. `[x]` **Session cookie.** (claimed 2026-10-19 03:05, run: cookie-session) Items 15 and 16 have landed, so the expert's revisit point (2e) is here. Split into two gated halves. Both halves are done.
19-i. `[x]` **Backend: cookie session and CSRF check.** (claimed 2026-10-19 03:05, run: cookie-session) Set `qc_session` (`HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` over TLS) on login and signup and clear it on logout; resolve the session from the cookie when no `Authorization: Bearer` is present; refuse a cookie-authenticated mutating request whose `Origin`/`Sec-Fetch-Site` says cross-site (a request with neither header is a non-browser client and stays allowed); keep the token in the login response for API clients. Tests plus the OpenAPI security scheme. Evidence: `setSessionCookie`/`clearSessionCookie`/`sessionToken`/`sameOriginRequest` in `backend/internal/httpapi/auth.go` (`sessionCookieName = "qc_session"`, 403 `CSRF_REFUSED`), tests `TestSignInSetsSessionCookie`, `TestSessionCookieIsSecureOverTLS`, `TestCookieAloneAuthorizesGet`, `TestLogoutClearsSessionCookie`, `TestCookieMutatingRequestSameOriginCheck`, `TestBearerIgnoresSameOriginCheck`, `TestInvalidBearerDoesNotFallBackToCookie` in `auth_cookie_test.go`, and the `cookieAuth` scheme plus `CSRF_REFUSED` code in `backend/internal/apidocs/spec.json`. `go build`/`go vet`/`go test ./internal/httpapi/ ./internal/auth/ ./internal/apidocs/` and `make test`/`make verify` green.
19-ii. `[x]` **Web and smoke: drop the stored token.** (claimed 2026-10-19 03:35, run: cookie-session) `web/src/api/authStorage.ts` deletes `AUTH_TOKEN_KEY`/`readAuthToken`/`writeAuthToken` and keeps only the non-secret account id (`AUTH_USER_KEY`/`readAuthUser`/`writeAuthUser`/`clearAuthUser`); `web/src/api/execution/http.ts` attaches no `Authorization` header, sends `credentials: 'same-origin'`, writes only the account id on signup/login, and asks `/auth/me` unconditionally (the 401 path and the writable capability key off the account id); `web/src/state/useAuth.ts` restores by `me()`; `Shell.tsx`/`useTheme.ts` key the signed-in UI off the session, not a token; the Makefile `verify` drives signup, the scoped PUT/GET and logout with a cookie jar (`curl -c/-b`) and proves the jar is 401 after logout, while keeping one bearer `/auth/me` check; `docs/features.md` drops the localStorage bullet, `backend/README.md` and `docs/self-hosting.md` describe the cookie/Bearer split, and ADR `docs/adr/0009-session-cookie.md` records the decision and the CSRF rule. Evidence: `cd web && npm run typecheck && npm run lint`, `make audit-ai-adapters`, `make audit-course-store`, `make audit-evaluate-wire`, `make audit-import`, `make audit-export`, `make test`, `make verify` and `make verify-mock` green.
20. `[x]` **`.pill` tones and `resetDialectChoice`.** (claimed 2026-10-19 04:00, run: pill-and-undo) Tone classes `.pill.pass`, `.pill.warn`, `.pill.fail` and `.pill.accent` carry the exact colours the inline objects used (`--green-soft`/`--green`, the amber, red and accent pairs), and the five call sites now use the class: `ChallengeLabScreen.tsx:359,494,574` (accent, green, green/red ternary) and `ReviewScreen.tsx:41,296` (amber). The undo is deleted: a stored choice is only named while `DialectRefusal` is on screen, and that refusal disappears the moment the engine serves the chosen dialect, so no existing surface could carry a reset. Adding one is new learner-facing UI the owner did not ask for, which the autonomy rule says to file rather than improvise. A changed engine makes the refusal return, so nothing is stranded.
    Evidence: `grep -rn 'className="pill" style' web/src` is empty; `grep -rnw resetDialectChoice web/src web/scripts scripts` is empty; `npm run typecheck`, `npm run lint` (0 errors), `make audit-themes` (18 themes, 252 pairs, OK), `make audit-shell` (OK), `make test` exit 0, `make verify` exit 0.

## Last (not the current concern)

L1. `[x]` **Accessibility runner in the build.** (run: last-items-a11y, finished by the orchestrator after a T3 restart) `scripts/a11y-audit.mjs` builds and serves `web/dist`, drives Chrome through `playwright-core` and runs axe-core (WCAG 2.x A/AA) on six real routes, failing on any critical or serious violation; `make audit-a11y` sits in `test-all`. It found and closed two real defects (a dangling `aria-controls`, the Workbook nav name). Evidence: `make audit-a11y` OK (6 routes, 0 violations, empty allowlist), `docs/accessibility.md` section 10; `make test` exit 0 and `make verify` exit 0.
L2. `[x]` **React Doctor's findings.** (claimed 2026-10-19 15:05, run: last-items-doctor) 83 findings (5 errors, 78 warnings) to 42 (2 errors, 40 warnings), each remaining finding with a verdict in `progress.md` "React Doctor re-run (2026-10-19)". Fixed: the three `no-ref-current-in-render` writes moved to `useLayoutEffect` (timing preserved), the capability retry timer now has a `clearTimeout`, three empty-`<label>` grid spacers became spans, six `Set`/`Map` lookups, the `useChecklist` updater, the `PromptDialog` prop reset, five `exhaustive-deps`, two non-component exports moved to `state/screens.ts`, the duplicated heading is `ui/PageHead.tsx`, one `Promise.all`, 16 array-index keys. Kept with reasons: two backdrops, five `prefer-html-dialog`, 13 complexity and 4 giant components (sizes recorded, split tracked), the JSON clone, the router effect, three deliberate loops, nine fixed-list index keys. Evidence: `npm run typecheck` clean, `npm run lint` 0/0, `make audit-shell`/`audit-themes`/`audit-a11y` OK, `make test` exit 0, `make verify` exit 0.
L3. `[x]` **Lint warnings: zero, none errors.** (claimed 2026-10-19 14:10, run: last-items-lint; finished 2026-10-19) `AiPerformResult.body` is now `Schema.Json | null`, matching `WireResult`, and all 44 `no-unknown-parameters` in `web/src/api/ai.ts` became `Schema.Json` parameters (`Schema.Json | null` for the whole-body readers). Decoders keep their tolerant semantics: a malformed value still reads as none, a wrong-typed row is still dropped. `outcomeFromError` now takes the transport's own `GatewayError`. Evidence: `npm run typecheck` clean; `npm run lint` 44 -> 0 warnings, 0 errors; `make audit-ai-adapters`, `make audit-course-store`, `make audit-evaluate-wire`, `make audit-ai` and `make audit-perf` OK; `make test` exit 0; `make verify` exit 0.
L4. `[x]` **Node v20 as root.** (run: last-items) Resolved in the environment: `/usr/local/bin/node`, `/usr/bin/node` and `/bin/node` all report v22.18.0, and `npm run doctor` starts in a non-interactive shell (exit 1 from findings, not from a version refusal).
L5. `[x]` **Resource-exhaustion fuzzing.** (claimed 2026-10-19 15:40, run: last-items-fuzz) Add a deterministic, seedable fuzz phase to the adversarial audit: generated SQL and raw request shapes that probe parser depth, statement length, row and byte caps, deadlines and the connection budget, each ending in a bounded contract outcome with the service healthy; publish the result in `docs/adversarial-suite.md`.
    Evidence: `scripts/adversarial-audit.mjs` adds a mulberry32 phase with `FUZZ_SEED`/`FUZZ_CASES` covering 11 SQL generators (parenthesis and subquery nesting, long `IN`, many `UNION ALL`, multi-MB literal, token soup, huge `LIMIT`/`OFFSET`, `generate_series` cross join, streaming and aggregate recursive CTEs, multi-statement script), 11 raw shapes (malformed and wrong-type JSON, negative/huge limits, deep JSON, unknown fields, 100 KB `dataset`/`dialect`, duplicate keys, 9 MiB body) and a `max_concurrent + 2` burst; seed 20261019 with 40 cases and `FUZZ_SEED=12345 FUZZ_CASES=60` both passed with 0 failures (1.6 s and 2.9 s), no 5xx, healthy after each batch; `node --check`, `make audit-adversarial`, both seed runs, `make test` and `make verify` green.

## Rules (from the expert)

1. One item per run. Never start the next before the current gate is green.
2. Split an item when it cannot clear one gate cycle or touches two of
   backend, web and contracts.
3. Done means: change in tree, `make test` and `make verify` green, an evidence
   line here, and no new lint findings.
4. Stop and report on two gate failures from one cause, an unanswered owner
   call, or a change that no file proves.
5. Claim an item by marking it `[~]` with the run id; skip claims younger than
   40 minutes; reclaim older ones with a note. A nudge to a stalled run renews
   the claim: wait one full tick after a nudge before starting a replacement,
   or two writers will race the same files.
