# web-data — slop audit (wave 1)

Scope: `web/src/api/**` (6577 lines), `web/src/state/store.tsx` (2011 lines),
`web/src/types.ts`. Components were read only to find consumers. No file was
edited. Evidence is `file:line` plus the `rg`/`grep` that found it.

Method: every exported symbol was extracted per file with
`grep -oE '^export (async )?(function|const|class|interface|type|enum|let) ...'`
and each name was then counted in `web/src` (and `scripts/`, `web/scripts/`)
with its defining file subtracted. A name whose only occurrences are inside its
own file (or its own audit script) is called out. Store members were counted for
real, not merely by the name: `grep -rn "\.MEMBER\b"` and the `const { … } =
useStore()` sites.

## Verdict

The real slop in this domain is not the engine code, which is honest and
evidence-heavy; it is (a) export surface that nothing outside its own file
reaches, and (b) two adapters writing the same three facts by hand. The
`store.tsx` provider is the single largest problem: its context value is still a
plain object literal built on every render (`store.tsx:1848`), so one query or one
toast re-renders every consumer, and six exported members have no reader at all.
The second cluster is compatibility: `api/execution/index.ts` is a barrel kept
"so callers keep their import path" with exactly one live name, and
`api/index.ts` re-exports seven names of which two are used.
`sqliteDataset.ts` states the dataset version a second time for no reader, and
`ai.ts` writes the AI error-code set and the auth localStorage key a second time.
Where the code is earned, it is genuinely earned: `compare.ts`, the
`contracts/dialects/sqlite.json` reader, the `?raw` dataset imports, `idb.ts`,
and `aiDeadline.ts` all have live consumers and no second copy. The
`ExecutionServiceShape` seam and the two engine adapters are correctly separated;
they merely duplicate three small concrete helpers that should be one.

A note on the answer the task asks for: of the "three execution adapters",
`pglite.ts` (engine) and `sqlite.ts` (engine) both produce the one shared shape
`EngineResult` (`pglite.ts:44`, imported at `sqlite.ts:29`), and
`execution/pglite.ts` and `execution/http.ts` both satisfy the one shared shape
`ExecutionServiceShape` (`execution/shared.ts:42`); the duplicated logic is not
the shape but `stripComments`, the `draftCourse`/`draftChallenge` bodies, and the
literal `datasetVersion: 1`.

## Delete

- `StoreValue.MODULES` — `state/store.tsx:153` (interface), `:1851` (value). Why it
  exists: the provider mirrors the curriculum array. Evidence: `grep -rn "\.MODULES\b" web/src`
  finds no `s.MODULES`; every reader in `ReviewScreen.tsx:4,20,186` and
  `Modals.tsx:5,77` imports `MODULES` from `data/curriculum` directly. Remedy:
  drop the interface member and the value entry (the module-level import at
  `store.tsx:20` stays for internal use).
- `StoreValue.courseShort` — `state/store.tsx:231` (interface), `:1954` (value).
  Why it exists: pass-through of `lib/model.courseShort` through the context.
  Evidence: `grep -rn "\.courseShort\b" web/src | grep -v store.tsx` is empty;
  `Shell.tsx:5`, `HomeScreen.tsx:7`, `ProgressScreen.tsx:4`,
  `ChallengeLabScreen.tsx:6` all import it from `lib/model`. Remedy: drop the
  member; the import at `store.tsx:24` becomes unused only if nothing else in the
  file calls it (it does not).
- `StoreValue.cycleTheme` — `state/store.tsx:220`, defined `:1935`. Evidence:
  `grep -rn -w cycleTheme web/src` outside `store.tsx` is empty. Remedy: drop.
- `StoreValue.effectiveTheme` — `state/store.tsx:215`, defined `:1904`. Evidence:
  no `s.effectiveTheme` anywhere (`grep -rn "\.effectiveTheme\b"` empty). The
  internal `const effectiveTheme` at `:537` feeds the DOM effect at `:539`; the
  exported member is the dead half. Remedy: drop the member, keep the effect.
- `StoreValue.openChallenge` — `state/store.tsx:194`, defined `:922`. Evidence: no
  `s.openChallenge`; every caller uses `s.showScreen('challenge-lab')`
  (`Shell.tsx:68,193`, `HomeScreen.tsx:60`, `CourseScreen.tsx:110`). Remedy: drop.
- `StoreValue.storedCourses` — `state/store.tsx:299` (interface), `:1991` (value).
  Evidence: the state is write-only: declared `:440`, written `:1431`
  (`setStoredCourses(next)`), and read only at `:1991`. `storedRef.current` is the
  live copy. Remedy: drop the member, the `useState`, and the `setStoredCourses`
  call; keep `storedRef`.
- `idbDelete` — `api/idb.ts:210`. Evidence: `grep -rn -w idbDelete web/src scripts web/scripts`
  returns only the definition. Remedy: delete the function.
- `idbEntries` — `api/idb.ts:215`. Evidence: definition only. Remedy: delete.
- `ExerciseCheck` and `Block.checks` — `types.ts:37-43` and `types.ts:75`. The type
  comment itself says "Adapters grade by comparing results, so nothing runs this
  field at run time" (`types.ts:40-41`). Evidence: `grep -rn "\.checks\b" web/src`
  finds only `Evaluation.checks` reads (`SqlCell.tsx:158-163`, `ChallengeLabScreen.tsx:220`),
  never `block.checks`; `grep -rn -w ExerciseCheck web/src` returns only the two
  definitions. Remedy: delete the interface and the field; the `checks:` data in
  `data/curriculum.ts` (36+) and the synthesized exercises at `store.tsx:1691,1727,1753`
  are the file-side halves of the same dead fact (outside this domain).
- `TableColumnDef` — `types.ts:146-150`. Evidence: `grep -rn -w TableColumnDef web/src`
  returns only the definition. Remedy: delete.
- `Course.tracks` — `types.ts:118`. Evidence: `grep -rn -w tracks web/src` finds
  only the field plus unrelated prose in `data/content/s7.md` and a comment in
  `sqlite.ts:218`. Remedy: delete the field.
- `Course.createdAt` — `types.ts:138`. Evidence: the only writer is the store
  (`store.tsx:1409`, from `rec.created_at`); no reader (`grep -rn -w createdAt web/src`
  outside `aiContract`/`types` returns the store write and `contract.ts:64`, which
  is `UserSchema`). Remedy: delete the field, or delete only the store write if an
  owner wants it kept for a future display.
- `datasetVersion` in `api/sqliteDataset.ts:22` — a second extraction of
  `dataset_version` from a schema header (regex duplicated verbatim from
  `api/dataset.ts:15`). Evidence: `grep -rn "sqliteDataset" web/src` shows only
  `sqliteEngine.ts:36` importing `{ schemaSql, seedSql }`; nobody imports this
  constant. Remedy: delete the export and its comment; there is no SQLite consumer
  of a dataset version (the perf lab is PostgreSQL-only).
- Dead export modifiers (symbols used only inside their own file; the `const`/type
  stays, the `export` goes):
  - `ApiMode` — `api/index.ts:9` (used at `:11,41,85` internally only).
  - `PGLITE_DIALECT` — `api/pglite.ts:35` (internal only).
  - `TEXT_VALUE_OIDS` — `api/pglite.ts:133` (internal only; no script uses it).
  - `SQLITE_DIALECT` — `api/sqlite.ts:43`, `SQLITE_ENGINE_FALLBACK` — `:48`,
    `SQLITE_TIMEOUT_MS` — `:53` (all internal only; the mention at
    `scripts/adapter-parity-audit.mjs:491` is prose).
  - `IDB_NAME` — `api/idb.ts:15` (internal only; `IDB_VERSION` is used by
    `scripts/storage-audit.mjs:32` and stays exported).
  - `ColumnSchema`, `QueryErrorSchema`, `QueryResultSchema`, `CheckResultSchema`,
    `EvaluationSchema`, `UserSchema`, `AuthSessionSchema`, `AuthUserSchema` —
    `api/contract.ts:10-77` (no consumer outside the file; only the four
    `decode*` functions are imported, by `execution/http.ts:6`).
  - `PlanBundleSchema`, `normalizeContract`: see Collapse; and
    `PlanStatus`, `PlanEngine`, `PlanSeqScan`, `PlanIndexScan`, `PlanSummary`,
    `PlanRawNode`, `PlanWarningCode`, `PlanWarning`, `PlanErrorShape`,
    `PlanCaptureInput`, `PerfCode`, `PerfRequireNode`, `PerfForbidNode`,
    `PlanVerdict` — `api/planContract.ts` (only `PlanBundle`, `PlanCheck`,
    `PerfContract`, `ExplainRequest` and the functions are imported elsewhere).
  - `CODE_VALUE_MISMATCH`, `CODE_COLUMN`, `CODE_COLUMN_ORDER`, `CODE_ROW_COUNT`,
    `CODE_ROW_ORDER`, `ActualResult`, `NormalizedContract` — `api/compare.ts:8-30`
    (internal; the live exports are `compareResult`, `compareColumns`,
    `checksFromComparison`, `Comparison`, `ExpectedResult`).
  - `Sqlite3`, `SqliteDatabase`, `ExecOutcome` — `api/sqliteEngine.ts:38,39,107`
    (internal; `openWorkspace` and the caps are what `sqlite.ts`/`sqlite.worker.ts`
    import).
  - `AiStageName` — `api/aiContract.ts:87-96`. It is a vacuous union: its only use
    is `name: AiStageName | string` (`aiContract.ts:99`), which is just `string`.
    Evidence: `grep -rn -w AiStageName web/src` returns `:87` and `:99` only.
    Remedy: delete the type, write `name: string`.
- `execution/index.ts:7-18` — a compatibility barrel whose own comment says it
  exists "so callers keep their import path" (line 4-5). Evidence: the only
  importer of `'./execution'` is `api/index.ts:1`, which imports only
  `createExecutionApi`. Every other re-export (`AUTH_TOKEN_KEY`, `AUTH_USER_KEY`,
  `ExecutionService`, `GatewayError`, `readAuthToken`, `readAuthUser`,
  `writeAuthToken`, `writeAuthUser`, `ExecutionServiceShape`, `PGLITE_CONFIG`,
  `pgliteLayer`, `httpLayer`) has a consumer that imports from
  `./shared`/`./pglite`/`./http` directly, or none. Remedy: keep only
  `export { createExecutionApi } from './service';` and delete the rest.
- Dead names in the `api/index.ts` re-export block — `api/index.ts:96-105`.
  Evidence: only `main.tsx:6` (`resolveApiMode`) and `store.tsx:19` (`createApi`,
  `readAuthToken`, `readAuthUser`) import from `api/index`; `AUTH_TOKEN_KEY`,
  `ExecutionService`, `GatewayError`, `PGLITE_CONFIG`, `clearAuthToken`,
  `writeAuthToken` are never imported from here. Remedy: keep the two live
  re-exports, delete the rest.
- `localMethods.getContent` — `api/execution/pglite.ts:174-180`, plus
  `'getContent'` in the `LocalMethods` Pick at `:70`. It is spread at `:409` and
  immediately overridden by the real `getContent` at `:411-417` (the comment at
  `:410` says so). Evidence: object literal keys, last wins. Remedy: delete the
  duplicate property and the Pick member.

## Collapse

- **The provider value is not memoized** — `store.tsx:1848` builds a fresh object
  literal on every render; the file's only `useMemo` is for `api` at `:482`.
  Evidence: `grep -n 'useMemo' store.tsx` -> one hit (482). Every `useStore()`
  consumer (`grep -rn 'useStore()' web/src` -> 34 call sites across 25 files)
  re-renders on any provider state change, including each `apiLog`/`apiCount`
  write from `onLog` (`:470-473`,
  `:419-420`) and every progress write. Remedy: wrap `value` in `useMemo`, then
  split per the Move section.
- **`commitProgress` is missing; the write ritual is copied 14 times** —
  `setProgress(next)` + `persistProgress(next)` + `syncProgress(next, …)` repeats
  at `store.tsx:827, 838, 1093, 1110, 1135, 1162, 1184, 1206, 1256, 1271, 1286,
  1337, 1347, 1372`. Evidence: `grep -n 'syncProgress(' store.tsx` lists exactly
  those. Remedy: one `commitProgress(next, courseId?)` that sets, persists and
  syncs, returning the snapshot (this is the target-architecture plan, still
  unlanded).
- **`commitUiState` is missing** — the same
  `{ ...uiState, selectedCourseId|libraryFilter }` + `saveJSON(STATE_KEY, …)`
  ritual is written four ways: `openLessonIn` `:933-935`, `selectCourse`
  `:947-949`, `setLibraryFilter` `:1868-1872`, `applyHash` `:1027-1029`, and
  `deleteCourse` `:1586-1588`. A `saveUiState` helper already exists (`:628`) but
  is used at only two of the five sites. Remedy: route all through one helper.
- **`stripComments` is written twice, byte-identical** — `api/pglite.ts:512-518`
  and `api/sqlite.ts:255-261`. Evidence: `diff` of the two ranges is empty
  (normalized line numbers). Remedy: one shared `stripComments` in a small
  `api/sql.ts`.
- **`draftCourse`/`draftChallenge` bodies are written twice** —
  `execution/http.ts:505-512` and `execution/pglite.ts:158-171` return the same
  literal `{ title, chapters: (req.steps ?? []).length, exercises, datasetVersion: 1 }`
  and `{ ok: true }`; the only difference is the offline `note`/`sleep`. Remedy:
  one `draftMethods(config, note)` implemented once for both layers (it is offline
  behavior by design — `http.ts:499-504` says so).
- **`datasetVersion: 1` is a second truth** — the same two bodies hardcode `1`
  (`execution/pglite.ts:163`, `execution/http.ts:510`) while `datasetVersion` is
  parsed from the schema (`api/dataset.ts:15`) and imported by
  `execution/pglite.ts:29` for `saveProgress`. Remedy: import and use the constant.
- **`AUTH_USER_KEY` is written twice, and `readApprover` re-implements its own
  read guard** — the key is `execution/shared.ts:121`, but `api/ai.ts:986` writes
  the literal `'querycraft.user'` again. Evidence:
  `grep -rn "querycraft\.\(token\|user\)" web/src` -> `ai.ts:986`, `shared.ts:117,121`.
  `ai.ts` also bypasses `readAuthUser()` (`shared.ts:141`), which already carries
  the SSR/storage guards. Remedy: `readApprover` calls `readAuthUser() ?? 'learner'`.
- **The AI error-code set is written twice** — `api/ai.ts:197-208`
  (`AI_ERROR_CODES`) re-lists `api/aiContract.ts:15-25` (`AiErrorCode`). Remedy:
  derive both from one `as const` tuple, the way `QUERY_ERROR_CODES` already
  does (`types.ts:237`).
- **The job-status vocabulary is written twice, overlapping** —
  `api/ai.ts:608-615` (`JOB_TERMINAL_STATUSES = [active,rejected,unavailable,failed,awaiting_approval,discarded]`)
  and `api/ai.ts:695` (`SETTLED_JOB_STATES`, the same minus `discarded`). Plus
  `asJobStage`'s local `known` array at `:629` restates the `AiJobStage.state`
  union (`aiContract.ts:118-123`). Remedy: one tuple, and a shared
  `isTerminalJobStatus`.
- **The execution seam surface is written three times** — `ExecutionApi`
  (`types.ts:511`, 49 method signatures), `ExecutionServiceShape`
  (`execution/shared.ts:42`, ~50 Effects), and the `createExecutionApi` facade
  (`execution/service.ts:84-174`, ~50 one-line wrappers). Evidence: adding one
  operation touches all three plus the adapter. Remedy: generate the promise
  facade from the shape (`toPromiseApi`), keeping the three explicit special cases
  (`warmUp` `service.ts:107`, `saveDraft`/`getDraft` `:117`, `aiAvailability` `:76`).
- **`Course` hides five real fields behind an index signature, so screens restate
  them** — `types.ts:143` is `[key: string]: unknown`; the store sets `source`,
  `blurb`, `domain`, `minutes`, `authored` (`store.tsx:1803-1812`) which the
  interface never declares. Evidence: three screens re-declare them by
  intersection — `LibraryScreen.tsx:9`, `CourseScreen.tsx:24`, `HomeScreen.tsx:11`
  (`Course & { blurb?: string; domain?: string; minutes?: number; source?: string }`).
  Remedy: declare the fields on `Course`, delete the intersection types and the
  index signature.
- **Two decoder vocabularies for the gateway body** — `api/ai.ts` hand-rolls the
  `asX(value: unknown)` family (26 functions, `:214-1021`) while
  `api/contract.ts:10` and `api/planContract.ts:280` declare `effect/Schema`
  structs. This is the target-architecture item (line 62-63) and remains unlanded;
  listed here so the collapse list is complete, not re-litigated.
- **`sqliteCapabilities`/`engineCapabilities` are two parallel capability
  builders** — `api/sqliteProfile.ts:205` and `api/pglite.ts:259`, each returning
  an `EngineCapabilities`. This is arguably two dialects with two honest answers;
  flagged UNCERTAIN rather than a delete.

## Move / split

- `state/store.tsx` (2011 lines) should become memoized stores behind the existing
  `useStore()` facade. Exact boundaries, matching the target architecture
  (`docs/target-architecture.md:56-58`):
  - `state/progressStore.tsx`: `progress`/`setProgress`, `progressRef`,
    `progressOwnerRef`, `hydratedRef`, `dirtyRef`, `initialProgress`,
    `persistProgress`, `commitProgress`, `syncProgress`, `syncEvent`,
    `recordEvent`, `addNote`, `toggleBookmark`, `saveQuery`, `removeSaved`,
    `removeWorkbookItem`, `addStudyTime`, `clearProgress`, `loadSampleProgress`,
    `exportData`, `importData`, `markLessonDone`.
  - `state/uiStore.tsx`: `screen`, `challengeId`, `reviewTab`, `tablesOpen`,
    `chaptersOpen`, `navOpen`, `modal`, `toastMsg`, `promptRequest`, `theme`,
    `customThemes`, `motion`, `uiState` (with `commitUiState`), the router effect
    (`:1006-1070`).
  - `state/catalogStore.tsx`: accounts (`session`, `authReady`, `authNotice`,
    `role`, `signup`, `login`, `logout`), `storedCourses`/`storedRef`,
    `materialise`, `applyStored`, `deleteCourse`, `adoptStoredCourse`, the
    course-resolution effect (`:1523-1561`).
  - `state/buildStore.tsx`: `draftCourse`, `draftReport`, `buildContext`,
    `submitBuild`, `useAuthoredDraft`, `adoptGeneratedCourse`, `resumeRun`,
    `resumeBuild`, `runStarted`, `runSettled`, `resumeStoredCourse`,
    `buildSettled`, and the authoring mutations (`moveLesson`, `saveCourseEdits`,
    `saveLessonEdits`, `applyExtension`, `addLesson`, `addSection`).
  Each provider memoizes its own value; `useStore()` composes the four and stays
  the only import screens touch.
- Two files are named `api/pglite.ts` (the engine, 980 lines) and
  `api/execution/pglite.ts` (the transport layer over both engines, 431 lines).
  The shared basename makes call sites read as if they hit the same file. Rename
  the layer to `api/execution/browser.ts` (or the engine to `api/pgliteEngine.ts`,
  matching `sqliteEngine.ts`).
- `api/contract.ts` (85 lines) is a schema file whose schema consts have no
  consumer; once the dead exports go it is only `decodeQueryResult`,
  `decodeEvaluation`, `decodeAuthSession`, `decodeUser`. It still earns its
  separate file as the mirror of `contracts/*.schema.json`.

## Earned (checked, keep)

- `api/compare.ts` — the §6.1 comparator; live in `execution/pglite.ts:4,349-354`
  and bundled by `scripts/fixtures-audit.mjs:22`.
- `api/sqliteProfile.ts` — reads `contracts/dialects/sqlite.json` directly (one
  reader for the profile, no mirror); `classifySqliteMessage`/`encodeSqliteValue`/
  `sqliteColumnTypeLabel` live in `sqlite.ts:33-37`; `sqliteErrorRules`/`affinityFor`
  are pinned by `scripts/sqlite-error-codes.mjs:422,503`.
- `api/sqliteFraming.ts` — the one driver-frame strip; live in `sqlite.ts:63-64`
  and `scripts/sqlite-error-codes.mjs:49`.
- `api/dataset.ts` and `api/sqliteDataset.ts` (schema/seed halves) — the dataset
  ships once in the Go module and is compiled in with `?raw`; live in
  `pglite.ts:2`, `sqliteEngine.ts:36`.
- `api/content.ts` — `BUNDLED_CONTENT` live in `execution/http.ts:480` and
  `execution/pglite.ts:179`; `seedContentDocs` live in `pglite.ts:3,393`.
- `api/idb.ts` core (`openDatabase`, `idbGet`, `idbPut`, `idbPutMany`,
  `OFFLINE_STORES`) — live in `execution/pglite.ts:23,131,149` and pinned by
  `scripts/storage-audit.mjs:35`.
- `api/aiDeadline.ts` — `advertisedRunBudgetMs`/`providerDeadlineMs` live in
  `execution/http.ts:145,194`; `AI_DEADLINE_FALLBACK_MS` pinned by
  `web/scripts/ai-adapters-audit.mjs:252`.
- `api/planContract.ts` builder/grading core — `buildPlanBundle`,
  `failedPlanBundle`, `emptyEnvironment`, `gradePlan`, `validatePlanBundle` live
  in `pglite.ts:7-12`, `execution/http.ts:7`, `SqlCell.tsx:189`.
- `api/errors.ts` — `GatewayError`/`asGatewayError`/`authFailure` live across
  `ai.ts`, `http.ts`, `execution/pglite.ts`.
- `api/sqliteEngine.ts` — the custom `@sqlite.org/sqlite-wasm` driver reached via
  `decltype`/progress-handler; deliberately kept per
  `docs/target-architecture.md:83-85`.
- `api/*.worker.ts` (both) and the `Transport` interface (`sqlite.ts:71`) — the
  one abstraction with two implementations (worker and main thread), so no rule
  can live in only one; live in `sqlite.ts:160-201`.
- `execution/shared.ts` bearer-token helpers (`readAuthToken`, `writeAuthToken`,
  `writeAuthUser`, `readAuthUser`, `clearAuthToken`) — live in `http.ts:27-31`,
  `execution/pglite.ts:210`, `store.tsx:403,650`.
- `store.tsx` `jobIsRunning` import (`:42`) — live at `:1477`; the AI job union
  is genuinely used by the resume effect.

## UNCERTAIN

- `api/pglite.ts:442` `getDatabase` is exported and used only by
  `scripts/boot-audit.mjs:157,195` (and internally); if the boot audit is the
  intended consumer it is earned, otherwise the export is dead. Needs an owner
  call.
- `MAX_ROWS = 200` (`api/pglite.ts:38`) vs `SQLITE_MAX_ROWS = 500`
  (`api/sqliteEngine.ts:176`) — the comments (`sqliteEngine.ts:170-175`) claim the
  difference is deliberate (DOM guard vs contract cap), but no parity case crosses
  either. Prove with a fixture or collapse to one number.
- `sqliteEngineCapabilities` (`api/sqlite.ts:228`) hardcodes `hardTimeout: true`
  while the main-thread transport cannot stop a statement (`sqlite.ts:128-137`);
  the value is right for the engine but the field means "this host". Needs runtime
  proof of which answer the UI relies on.
- `store.tsx:422` `const [, setCatalogVersion] = useState(0)` — a write-only state
  used purely as `forceUpdate` (`bumpCatalog` `:1381`, called at nine sites). It
  works because it re-renders the provider and rebuilds the un-memoized value;
  once the value is memoized this mechanism stops being a re-render trigger and
  needs replacing. Do not remove before the memoization lands.
- `Course.authored` / `Course.source` are set (`store.tsx:1812,1803`) and read
  only through screen-local intersection types; if the `Course` fields are
  declared, confirm no consumer relies on the index signature for other keys.
