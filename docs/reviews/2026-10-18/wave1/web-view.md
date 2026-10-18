# web-view — slop audit (wave 1)

Scope: `web/src/components/**`, `web/src/lib/**`, `web/src/App.tsx`,
`web/src/main.tsx`. `web/src/state/store.tsx` and `web/src/api/**` were read only
to name consumers. No file was modified; the only write is this report.

Every "no consumer" claim is one `grep -rn '\bSYMBOL\b'` over `web/src` plus
`scripts/`, plus a `grep -rl` over `scripts/` for esbuild-bundled audit entry
points (the audit scripts are a live consumer, so they count as consumers).

## Verdict

The real slop is concentrated in three places and two of them are second
descriptions rather than spare parts. `BuildingScreen.tsx` holds the whole
generation lifecycle as one 537-line effect (`if (` 63 times, 23 `await`s, 15
`useState`, 6 `useRef`) that is a reducer typing itself out, and
`ChallengeLabScreen.tsx` writes the same checklist state machine and the same
abortable pause again. The focus trap and the HTML escaper are each written
three times, and the "exercises with a reference" filter is written twice on top
of `moduleExerciseBlocks`. The `lib/` layer is the honest part: almost every
export is consumed by a screen or by an audit script. The exceptions are a small
set of symbols (`builtCourses`, `moduleIndexOfExercise`, `hasMermaid`,
`themeLabel`, `THEME_IDS`, `emptyImportState`, `datasetLabel`) that nothing
reads at all. The remainder is surface that lies: control fields in `Modals.tsx`
that save nothing, two always-on switches in `AppearanceScreen.tsx`, and a
`Save challenge` toast with no writer. `datasetReason` and `dialectReason` look
dead from `src` alone but are loaded by `scripts/course-audit.mjs` and
`scripts/content-audit.mjs`; they are earned.

## Delete

`builtCourses()` — `web/src/lib/model.ts:28`. Returns `COURSES.filter((c) => c.built)`. `grep -rn '\bbuiltCourses\b' web/src scripts` returns only the definition. `HomeScreen.tsx:23` and `CourseScreen.tsx:28` open-code the same filter, so the helper is a third copy nobody calls. Remedy: delete it; if the filter is wanted once, route both screens through `activeModules`-style helper, not this.

`moduleIndexOfExercise(id)` — `web/src/lib/model.ts:65`. Wraps `findExercise(id)?.index`. `grep` finds only the definition; `grep -rl '\bmoduleIndexOfExercise\b' scripts` finds nothing. Remedy: delete.

`hasMermaid(md)` — `web/src/lib/markdown.ts:53`. `Markdown.tsx:45` finds `div.qc-mermaid` nodes in the rendered DOM instead of testing the source, so the regex helper has no caller. `grep -rn '\bhasMermaid\b' web/src scripts` returns the definition only. Remedy: delete, and delete `MERMAID_FENCE_RE` at `markdown.ts:23` with it.

`themeLabel(id)` — `web/src/lib/themes.ts:45`. `AppearanceScreen.tsx:141` prints `t.label` straight from the `THEMES` entry. `grep` finds no caller. Remedy: delete.

`THEME_IDS` — `web/src/lib/themes.ts:43`. `grep -rn '\bTHEME_IDS\b' web/src scripts` returns the definition only. `scripts/theme-contrast.mjs:24` reads `styles/themes.css` directly, so the audit does not use it either. Remedy: delete.

`emptyImportState()` — `web/src/lib/importBundle.ts:309`. Body is `return emptyProgress();`, the doc comment above it says "The workbook's empty-state text" though it returns a `Progress`, and no file calls it. Remedy: delete; callers already use `emptyProgress` from `lib/progress`.

`datasetLabel(dataset)` — `web/src/lib/dataset.ts:93`. The doc claims "this is where that sentence is written once", but `TablesDrawer.tsx:208-211` writes its own dataset sentence inline and no file calls `datasetLabel`. Remedy: delete, or move the drawer sentence here. Today the claim is false.

`DatasetRef.module` — `web/src/lib/dataset.ts:34-38`. The comment says it "contributes nothing to the answer" and is declared for symmetry. `datasetChoice` never reads `ref.module`, and no caller passes it (`grep 'datasetFor('` shows `TablesDrawer.tsx:47` `{ course }`, `SqlCell.tsx:47` `{ course, block }`, `pglite.ts:302,313`, `http.ts:388,414`, `store.tsx:853`). Remedy: delete the field.

`Schema` prop `tables` — `web/src/components/Schema.tsx:7,26,29`. Only two callers exist: `ReviewScreen.tsx:155` and `TablesDrawer.tsx:216`, and neither passes `tables` (`grep -rn 'tables=' --include=*.tsx` returns nothing). The `tablesProp || null` initial state and the `if (tablesProp) return;` short-circuit serve a caller that does not exist. Remedy: drop the prop and read from `api.getTables` unconditionally.

`ResultGrid` prop `showFooter` — `web/src/components/ResultGrid.tsx:19,61`. The sole caller is `SqlCell.tsx:423` `<ResultGrid qr={qr} />`; nothing overrides the `= true` default. Remedy: delete the prop and the conditional.

`Ring` props `label`, `className` — `web/src/components/ui/Ring.tsx:2`. All three callers pass only `pct` (`CourseScreen.tsx:54`, `HomeScreen.tsx:63`, `WorkbookScreen.tsx:66`). Remedy: delete both props; keep `'complete'` and `'cring'` inline.

Icon symbols `i-sun`, `i-moon`, `i-chart` — `web/src/components/ui/Icon.tsx:28,29,39`. `grep -rn "['\"]sun['\"]\|['\"]moon['\"]\|['\"]chart['\"]"` over `web/src` finds no `name=` or `icon=` use. The theme toggle these were cut for is now `AppearanceScreen`. Remedy: delete the three entries from `SYMBOLS`.

`EditCourseModal` fields `level`, `context`, `dialect` — `web/src/components/screens/Modals.tsx:41-43,58-66,69`. The modal passes all four to `saveCourseEdits` (`Modals.tsx:69`), but `store.tsx:1637-1644` destructures `{ title }` and ignores `goal`, `context`, `dialect`. So three of the five inputs in this modal write nothing. Remedy: delete the three unused fields and the three values from the call; only `title` and the separate `setReviewGoal(goal)` line are live.

`AddLessonModal` "Placement" control — `web/src/components/screens/Modals.tsx:143,149-157,160`. `position` is threaded into `addLesson`, whose store signature `store.tsx:267` accepts it and whose body `store.tsx:1703-1705` destructures `{ title, goal }` only. The select can never change the outcome. Remedy: delete the state, the `Field`, and the argument.

`EditLessonModal` "Exercise prompt" control — `web/src/components/screens/Modals.tsx:80,86,96,99`. `prompt` is a local `useState` filled from the chapter and rendered, but `saveLessonEdits` (`Modals.tsx:99`) passes `{ title, summary: body }` and the store (`store.tsx:1647`) reads only `title`/`summary`. The textarea edits nothing. Remedy: delete the state and the `Field`.

`Head` prop `id?: string` — `web/src/components/screens/Modals.tsx:23`. The signature declares it; the component never reads it and no of the four call sites passes it. Remedy: delete from the type.

AppearanceScreen always-on switches and dead guard — `web/src/components/screens/AppearanceScreen.tsx:85,155,159`. Line 85 is `{(form.name || form.accent || true) && (`, a condition that is always true. Lines 153-160 render two `Switch`es hardcoded `on={true}` whose `onClick` only toasts ("Result grids already scroll horizontally", "Override confirmation would be a product setting"). A switch that cannot change state and reports a state it does not own is a fake control. Remedy: delete both toggles and the guard; if the settings are real, wire them to store state.

ChallengeLabScreen fabricated result text and dead buttons — `web/src/components/screens/ChallengeLabScreen.tsx:596,550,626`. Line 596 prints `{scenario}. Return the top plan per region each month, including a deterministic tie-break, using window functions.` as the challenge prompt in the result card, a hardcoded suffix the request never contained. Lines 550 and 626 render "Save challenge" buttons whose only action is `s.toast('Saved to your challenge set.')`; no challenge is stored. Remedy: render the request that was actually sent, or delete the card copy; delete the Save buttons or give them a writer.

SqlCell "Report a problem" — `web/src/components/SqlCell.tsx:259-261`. `reportProblem` toasts "This would open the on-demand critique flow: critic → verifier → fixer, with your approval before anything applies." The button is rendered on every failed check (`SqlCell.tsx:564`). The comment block above the tutor call (`SqlCell.tsx:223-233`) records that the earlier "in a live app…" toast was removed as a mock tell; this is the same shape. Remedy: delete the button or wire it to a real route.

Exports with no external consumer — `parseRoute` `web/src/lib/route.ts:100`, `useEngineCapabilities` `web/src/components/ui/DialectBadge.tsx:52`, `contrastRatio` `web/src/lib/userThemes.ts:119`, `userThemeCSS` `web/src/lib/userThemes.ts:179`, `mergeProgress`/`mergeDrafts`/`isEmptyProgress` `web/src/lib/importBundle.ts:195,268,136`. Each is referenced only inside its own file. Remedy: drop the `export` keyword; keep the function. `parseRoute` stays the private half of `routeTarget`.

## Collapse

Focus trap written three times — `web/src/components/ui/useDrawerFocus.ts:15-23` (FOCUSABLE + `focusable`), `web/src/components/TablesDrawer.tsx:13-27` and `:153-186`, `web/src/components/ui/PromptDialog.tsx:9-20` and `:36-62`. The hook's own header (`useDrawerFocus.ts:4-5`) says it was "extracted from TablesDrawer.tsx's proven mechanics", yet `TablesDrawer` still carries its full copy (`grep -rn 'useDrawerFocus'` shows only `ChaptersDrawer.tsx:19` and `Shell.tsx:137`). Remedy: make `TablesDrawer` call `useDrawerFocus(s.tablesOpen, asideRef)`, extend the hook with the restore-to-`#main` branch it already has (`useDrawerFocus.ts:70`), and have `PromptDialog` use the same trap.

Two HTML escapers — `web/src/components/screens/BuildingScreen.tsx:16-18` (`esc`) and `web/src/lib/markdown.ts:7-17` (`HTML_ESCAPES` + `escapeHtmlAttribute`). Both map the same five characters. Remedy: export one escaper from `lib/markdown.ts` and import it in `BuildingScreen`; the log should become structured `{ text, tone }` JSX and drop `dangerouslySetInnerHTML` (`BuildingScreen.tsx:813`) anyway.

Exercises-with-a-reference derived twice — `web/src/components/screens/BuildingScreen.tsx:101-105` (`referenceExercises`) and `web/src/components/screens/ChallengeLabScreen.tsx:19-29` (`referenceBlock`). Both flatten `course.modules` through `moduleExerciseBlocks` and keep `typeof solution === 'string' && solution.trim()`. Remedy: add `exercisesWithReference(course)` to `lib/model.ts` beside `moduleExerciseBlocks` and delete both locals. (The 2026-10-17 review, Finding 5, named this; it was not applied.)

`dialectLabel` redefined in a screen — `web/src/components/screens/ReviewScreen.tsx:222` (`dialect === 'sqlite' ? 'SQLite' : 'PostgreSQL'`) against `web/src/lib/dialect.ts:36`, which `TablesDrawer.tsx:206` and `SqlCell.tsx` import. Remedy: delete the local, import the lib one, and resolve `course.dialect` through `effectiveDialect(undefined, course)` (already used by `HomeScreen.tsx:131`).

`isDialectValue` redefined — `web/src/lib/dialectChoice.ts:38-40` repeats `isDialect` from `web/src/lib/dialect.ts:15`, in a module that already imports from `dialect.ts:1`. Remedy: import `isDialect`; delete the local.

Abortable pause written twice — `web/src/components/screens/BuildingScreen.tsx:109-123` (`pauseFor`) and `web/src/components/screens/ChallengeLabScreen.tsx:166-178` (`pause`). Same Promise, same `{ once: true }` abort listener, same `clearTimeout`. Remedy: one `sleep(ms, signal)` in `lib/`.

Checklist state machine written twice — `web/src/components/screens/BuildingScreen.tsx:28-54,78-90,142,191-197` (`PIPE_STEPS`, `applyStages`, `states`, `mark`) and `web/src/components/screens/ChallengeLabScreen.tsx:11-16,81,157-163,429-435` (`CL_STEPS`, `states`, `mark`). Same five-slot array, same `mark` reducer, same `done/failed/idle` rendering. Remedy: one `useChecklist(steps)` hook; the per-screen difference is the stage-name map, which belongs with the caller.

`enrolled && built` filter duplicated — `web/src/components/screens/HomeScreen.tsx:23` and `web/src/components/screens/CourseScreen.tsx:28`. `lib/model.ts` is where this belongs and `builtCourses` there is dead (above). Remedy: one `enrolledCourses()` in `lib/model.ts`, two call sites.

`measured` re-derives `report.totals` — `web/src/components/screens/BuildingScreen.tsx:288-293` builds `{ ran: totals.verified, failed: totals.exercises - totals.verified, rows, ms }` immediately after `reportFromOutcome` already computed those totals (`lib/draft.ts:230-237`), and the authored path does it again (`BuildingScreen.tsx:675`). Remedy: render the right-hand card from `report.totals`; delete `measured`.

Stale-attempt derivation duplicated — `web/src/components/screens/ProgressScreen.tsx:93` and `:139` are the identical `isEarlierVersion(e, stampFor(findExercise(e.id)?.block, datasetVersion))`. Remedy: compute once per event row (or a small `staleFor(e)` helper).

`TopNavLink` and `NavButton` — `web/src/components/Shell.tsx:24-35` and `:116-124`. Same `{ icon, label, active, badge, onClick }` shape and the same `Icon` + label + badge body, differing only in the class name (`toplink` vs `nav`) and that one takes `React.ReactNode` for the badge. Remedy: one component with a `variant` class.

Generation-log markup duplicated — `web/src/components/screens/BuildingScreen.tsx:811-815` and `web/src/components/screens/ChallengeLabScreen.tsx:493-497` render the identical `.stream` / `.ln` / `.t` block over `log.map` with `dangerouslySetInnerHTML`. Remedy: one `GenerationLog` component taking `{ log, open }`.

Orphaned doc comment — `web/src/lib/progress.ts:102-107`. The block "True when a snapshot came from the sample-data generator …" describes `isSampleProgress` (`progress.ts:126`) but sits directly above `migrateProgress`, whose own doc starts at 107. Two JSDoc blocks stack on one function and the first is attached to the wrong one. Remedy: move the first block to `isSampleProgress`.

Doc promises with no implementation — `web/src/lib/pipeline.ts:11-13` ("the HTTP adapter replays the same cadence") is false: `grep -rn runPipeline` finds one caller, `BuildingScreen.tsx:232`; no adapter imports it. `web/src/lib/dataset.ts:83-85` says "no screen reads it yet"; the audit does read it. Remedy: correct both comments.

Repeated pill style objects — `web/src/components/screens/ChallengeLabScreen.tsx:506,588` and `ReviewScreen.tsx:40` repeat `{ background: 'var(--green-soft)', color: 'var(--green)' }` / amber / red variants inline. Remedy: the existing `.pill` classes should carry the tone (`pass`/`warn`), not an object literal at each site.

## Move / split

`BuildingScreen.tsx` (879) — the effect at `:184-720` is a reducer in waiting and should leave the file. Exact boundaries: (1) `lib/generationRun.ts` owns the run union and reducer, `useGenerationRun()` owns `runKey`/`states`/`log`/`awaiting`/`approving`/`discarding`/`failure`, the `resumeRun`/`resumeBuild` entry points, and the `settle`/`runApproval`/`discardOutline` transitions (today `:217-441`); (2) `lib/generationPoll.ts` owns a pure `poll(api, handle, { onTick, signal, deadline })` returning `AiOutcome | null` (today `watchJob`, `:450-549`) plus the shared `sleep`; (3) `components/GenerationLog.tsx` owns the `.stream` markup (`:811-815`); (4) `BuildingScreen.tsx` keeps only `PIPE_STEPS`, the stage map, and the JSX (`:722-879`). The module-level `resumedBuildJobs` Map (`:134`) and the `approveRef`/`discardRef` indirection (`:166-167`) disappear because the reducer owns the run id.

The exact state machine the effect encodes, as tags: `idle` → `pacing` (runPipeline, `:232`) → branch on `availability.available || resuming`: `submitting` (`submitGeneration`, `:584`) → `watching` (`watchJob`) → `settling`; or blocking `generating` (`generateCourse`, `:606`) → `settling`. `settling` (`:264`) splits by `outcome.status`: `awaiting_approval` → `awaiting` (`:303`), `active` → `adopting` (`adoptGeneratedCourse`, `:353`) → `finishing` (`:198`), anything else → `failed` (`:330`). Transitions out of `awaiting`: `approving` (`runApproval`, `:377`) → `watching` → `settling`, or `discarding` (`discardOutline`, `:425`). Separate entries: `resuming` (`resumeRun`, `:217`) and `resumingBuild` (`resumeBuild`, `:554`) enter `watching`/`approving` directly. The no-provider path is a distinct branch (`:622-702`): `pacing` → `verifying` (per-exercise `runQuery`, `:651`) → `drafting` (`draftCourse`, `:686`) → `finishing`. `failed` offers `failed → restart` via `runKey` (`:783`).

`ChallengeLabScreen.tsx` (634) — two independent runs live in one file. Boundary: `lib/challengeEvidence.ts` owns `referenceBlock`, `adversarialFixtures`, the `Evidence`/`TryOutcome` types and the `run()` body (`:19-29,34-41,138-287`); the component keeps the form, the gateway path (`generateOnGateway`, `:292`), `tryIt` (`:329`), and JSX.

`web/src/components/ui/` — add `focusTrap.ts` (or widen `useDrawerFocus.ts`) as the one home for `FOCUSABLE` + `focusable` + the trap effect, consumed by `TablesDrawer`, `PromptDialog`, `ChaptersDrawer`, `Shell`. This is the boundary the hook's own comment already claims.

`web/src/lib/pipeline.ts` — `PipelineOptions` (`:3-9`) mixes required cadence fields with optional `steps`/`lines`/`startMs`, and its only caller uses `steps` for empty pacing ticks (see UNCERTAIN). If the steps channel stays, split the options type into `{ cadence }` and `{ steps, lines }` so the caller's intent is explicit; if it goes, `runPipeline` drops to lines-only.

## Earned (checked, keep)

`lib/scroll.ts` `scrollToNode` — one caller `LessonScreen.tsx:86`; the `behavior:'smooth'` fallback has a measured rationale in the header (`:7-20`), not speculation.

`lib/runResume.ts` `readRun`/`writeRun`/`clearRun` — consumers `store.tsx:1470,1502,1482`; the owner scoping and conditional clear are load-bearing.

`lib/offlineBundle.ts` `bundleFromProgress` — consumer `api/execution/pglite.ts:124`; `scripts/storage-audit.mjs:23` drives it.

`lib/exportBundle.ts` `collectDrafts`/`buildExportBundle`/`downloadExport` — consumers `store.tsx:1354,1369`; `exportFilename` is used by `scripts/export-audit.mjs:73`.

`lib/importBundle.ts` `validateBundle`/`readImport`/`applyImport`/`describeImport` — consumers `store.tsx:1362-1375` and `scripts/import-audit.mjs:25-28`; the merge rules at `:195-306` are the migration policy, not duplicate logic.

`lib/contentSeed.ts` `planContentSeed`/`splitSections`/`contentFingerprint`/`courseForModule`/`CONTENT_FINGERPRINT_KEY` — consumers `api/content.ts:3,21,38,46,61` and `scripts/content-audit.mjs:169-170`.

`lib/search.ts` `buildIndex`/`search`/`groupHits`/`SEARCH_KIND_LABEL` — consumers `SearchBox.tsx:6,28,32,111` and `scripts/search-audit.mjs:13`.

`lib/progress.ts` `accrueStudyTime`/`formatDuration`/`stampFor`/`isEarlierVersion`/`migrateProgress`/`isSampleProgress`/`seedProgress`/`rebuildProgressFromEvents`/`currentStreak`/`whenLabel`/`attemptsByDay`/`WEEKDAY_LABELS` — consumers in `LessonScreen.tsx:50`, `ProgressScreen.tsx:5`, `HomeScreen.tsx:67`, `store.tsx:47-53`, and `scripts/progress-audit.mjs:15`.

`lib/route.ts` `routeTarget`/`hashForState`/`isRouteHash`/`HOME_HASH`/`AUTHORING_SCREENS` — consumers `store.tsx:391,996,1008,1023` and `App.tsx:23,45`.

`lib/dialectChoice.ts` `chapterDialect`/`chooseDialect`/`servedDialects`/`switchTarget`/`engineLabelFor`/`subscribeDialectChoice`/`dialectChoiceVersion`/`storedDialectChoice`/`resetDialectChoice` — consumers `DialectBadge.tsx:6-12,73,74,119,123,202` and `ResultGrid.tsx:1,74`. `resetDialectChoice` is exported with no caller today; it is the documented undo for `chooseDialect`, marked UNCERTAIN below.

`lib/dataset.ts` `datasetFor`/`datasetChoice`/`isDataset`/`asDataset`/`DATASETS`/`datasetReason` — consumers `TablesDrawer.tsx:47`, `SqlCell.tsx:47`, `store.tsx:853`, `api/execution/pglite.ts:285,302,313`, `api/execution/http.ts:368,388,414`; `asDataset`/`datasetReason`/`DATASETS` are loaded by `scripts/content-audit.mjs:152,169-170` and `datasetReason` by `scripts/course-audit.mjs:49,113`.

`lib/dialect.ts` `dialectFor`/`effectiveDialect`/`dialectLabel`/`dialectSupported`/`asDialect`/`isDialect`/`DIALECTS`/`dialectReason`/`dialectChoice` — consumers `HomeScreen.tsx:131`, `LibraryScreen.tsx:80`, `DialectBadge.tsx:4`, `api/execution/pglite.ts:286,314`, `api/execution/http.ts:368,418`; `dialectReason`/`asDialect`/`isDialect`/`DIALECTS` are read by `scripts/course-audit.mjs:48,112` and `scripts/content-audit.mjs`.

`lib/draft.ts` `courseFromPlan`/`reportFromOutcome`/`authoredReport` — consumers `BuildingScreen.tsx:8,285,315,697` and `store.tsx:45,1406,1830,1835`.

`components/ui/Field.tsx` — the `useId` rationale (`:4-13`) is real: four modals mount at once. Consumers `Modals.tsx:56-66,94-96,128-130,147-157`, `CreateScreen.tsx:69-74`, `ChallengeLabScreen.tsx:373-384`.

`components/ui/Icon.tsx` sprite (`SYMBOLS` + `IconSprite` + `Icon`) — one hidden `<svg>` of `<symbol>`s, used by every screen and `App.tsx:71`. Keep, minus the three dead symbols.

`components/ui/Markdown.tsx` — theme-aware Mermaid re-render (`:25-35`) and font-measure wait (`:71-79`) are earned; one consumer `LessonScreen.tsx:190`.

`components/PlanView.tsx` — the timing-precision rule (`:13-29`) is enforced in the render, and `estimateOff` (`:47-54`) recomputes the flag rather than trusting the engine's annotation. One consumer `SqlCell.tsx:415`.

`components/SqlCell.tsx` — 572 lines, but every state is reachable and the `liveStatus` region (`:279-300`) and dialect refusal (`:130,147`) are real. Keep minus `reportProblem`.

`components/TablesDrawer.tsx` reset flow (`:120-141`, two-press confirm, `inert` handling) — earned. Keep the behaviour, collapse the trap into the hook.

`App.tsx` / `main.tsx` — thin; `SCREENS` map, the solo Escape layer order (`:47-62`), and the auth/course-resolve gate (`:68`) are each single-purpose. Keep.

`lib/userThemes.ts` color math — the derivation (`:139-170`) is duplicated from `scripts/theme-contrast.mjs` on purpose, so the audit does not import the code it audits. Keep the duplication.

## UNCERTAIN

`PipelineOptions.steps` and the `step`/`all-done` events — `web/src/lib/pipeline.ts:3,61-72`, `types.ts:427-430`. The only caller (`BuildingScreen.tsx:232-246`) passes `steps: beats.map(() => '')` and filters the callback to `ev.type === 'log'`, so no step payload is read; the `steps` chain still stretches the awaited duration (`stepMs: 420` vs `lineMs: 380`). Deleting `steps` shortens the legibility pause, so the deletion needs a runtime check of the intended cadence. This is the one lib seam I could not settle statically.

`resetDialectChoice` — `web/src/lib/dialectChoice.ts:99`. Exported, no caller in `web/src` or `scripts`. It is the only way to undo a stored chapter choice; whether the UI should offer it is an owner decision, not a static one.

`BuildingScreen` local `report`/`built` vs store — `BuildingScreen.tsx:148-149,287,353,515`. The local copies exist so the card paints before `s.draftReport`/`s.draftCourse` update; whether the store can be made synchronous enough to delete them needs the reducer split first.

ChallengeLabScreen's two generate buttons — `ChallengeLabScreen.tsx:386` (`run`, the authored evidence path) and `:387` (`generateOnGateway`). Both are wired and reachable. Whether the authored path is still wanted beside the gateway path is a product call; `run()` is 150 lines of real work, so I did not mark it dead.

`AppearanceScreen` network-latency switch — `AppearanceScreen.tsx:187` toggles `s.latency`, which is a store flag the adapters read. Confirm the adapters still honour it before treating it as live.

`useEngineCapabilities` module-level `pendingProbe` — `DialectBadge.tsx:35-48`. A module singleton inside a component module, with a comment claiming it "caches nothing, so it cannot pin a stale answer". Needs a runtime check that two mounts do not share a resolved promise across an adapter change.
