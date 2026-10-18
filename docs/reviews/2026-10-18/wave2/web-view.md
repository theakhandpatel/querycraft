# web-view — wave 2 verification

Scope and method: every claim below was re-checked against the working tree on
2026-10-18. `rg` is not installed in this shell, so each search is the
equivalent `grep -rnw SYM web/src scripts` (plus a whole-repo `grep -rnw` where
a helper could live outside the two trees). No file was edited; this report is
the only write. The wave-1 report named `web/src/components/screens/Modals.tsx`;
the real path is `web/src/components/Modals.tsx`, and every "Modals.tsx"
reference here means that file.

## Verdict

Most of wave 1 survives. The delete list is sound: all eight dead symbols and
all four dead props still have no caller, and the three "looks dead but is live"
calls (`datasetReason`, `dialectReason`, `exportFilename`) are confirmed live
through the bundling scripts. Three items need correction: the Modals file path,
the BuildingScreen state-machine tags (the initial tag is `queued`, not `idle`,
and the machine has a mid-run adoption state and two poll-failure exits the
report omits), and the "five-slot checklist" claim (ChallengeLab's list has four
slots, not five, and its empty state is `idle`, not `queued`). Two remedies
overreach: `referenceBlock` searches all courses, so a per-course
`exercisesWithReference` cannot replace it one-for-one, and the fabricated
ChallengeLab prompt has no "request that was actually sent" to render. The final
list is one batch delete of dead symbols and props, one collapse batch for the
Focus/Escape/checklist duplicates, one corrected Move of `BuildingScreen`, and
one missed export-pruning item.

## Confirmed actions

Ordered by value (delete first, then the highest-traffic collapse).

- [ ] Delete the eight unreferenced lib symbols: `builtCourses`, `moduleIndexOfExercise` — `web/src/lib/model.ts:28,65`; `hasMermaid` + `MERMAID_FENCE_RE` — `web/src/lib/markdown.ts:23,53`; `themeLabel`, `THEME_IDS` — `web/src/lib/themes.ts:43,45`; `emptyImportState` — `web/src/lib/importBundle.ts:309`; `datasetLabel` — `web/src/lib/dataset.ts:93`. Evidence: `grep -rnw <sym> web/src scripts` returns the definition only for all seven, and a whole-repo `grep -rnw` over `--include=*.ts,*.tsx,*.mjs,*.go,*.md` (excluding `node_modules`, `.worktrees`, `docs/reviews`) also returns only the definition. Safe now: each is a pure function/const with no call site in `web/src` or the esbuild-bundled audits under `scripts/`.

- [ ] Delete the dead props and their short-circuits: `Schema` prop `tables` (`tablesProp`) — `web/src/components/Schema.tsx:7,12,26,29`; `ResultGrid` prop `showFooter` — `web/src/components/ResultGrid.tsx:19,61`; `Ring` props `label`/`className` — `web/src/components/ui/Ring.tsx:2`; `Head` prop `id` — `web/src/components/Modals.tsx:23`. Evidence: `grep -rn "tables=" web/src --include=*.tsx` is empty and `<Schema` appears only at `ReviewScreen.tsx:155` and `TablesDrawer.tsx:216`, neither passing `tables`; `<ResultGrid` appears only at `SqlCell.tsx:423` with no `showFooter`; `<Ring` appears at `CourseScreen.tsx:54`, `HomeScreen.tsx:63`, `WorkbookScreen.tsx:66`, all `pct`-only; `<Head` appears four times in Modals and none passes `id`. Safe now: no caller can reach the branch.

- [ ] Delete the three unused icon symbols `i-sun`, `i-moon`, `i-chart` — `web/src/components/ui/Icon.tsx:28,29,39`. Evidence: `grep -rn "['\"]sun['\"]\|['\"]moon['\"]\|['\"]chart['\"]" web/src scripts` is empty, every `name=`/`icon=` call site is literal or a two-way ternary (`Shell.tsx:27,119`, `HomeScreen.tsx:106`, `WorkbookScreen.tsx:124`, `PlanView.tsx:292`, `SqlCell.tsx:306`), and `grep -rn SYMBOLS web/src scripts` shows only the sprite. Safe now: removing a `<symbol>` no `use href` names.

- [ ] Delete the fake controls in `AppearanceScreen`: the always-true guard `(form.name || form.accent || true)` at `AppearanceScreen.tsx:85`, and the two hardcoded `on={true}` switches at `:153-160` ("Wide result grids", "Confirm before accepting an override") whose `onClick` only toasts. Evidence: read of `:85` and `:149-161`; the store exposes no writer for either label (`grep -n "Wide result grids\|Confirm before" web/src` returns only this screen). Safe now: a switch that cannot change state and reports a state it does not own is a fake affordance; the reduced-motion switch beside them (`:150-151`) is real and stays.

- [ ] Delete `reportProblem` and its button — `web/src/components/SqlCell.tsx:34` (type), `:259-261` (body), `:426` (ctx), `:451` (destructure), `:564` (render). Evidence: `grep -rnw reportProblem web/src scripts` is the definition plus its own call sites only; the body is a single `s.toast('This would open the on-demand critique flow: critic → verifier → fixer…')`, and the comment above the tutor call at `:223-233` records that the same "in a live app…" toast shape was already removed once. Safe now: no route or store call backs it.

- [ ] Drop the `export` keyword on the seven same-file-only exports: `parseRoute` — `web/src/lib/route.ts:100` (used at `:146` in `routeTarget`); `useEngineCapabilities` — `web/src/components/ui/DialectBadge.tsx:52` (used at `:117`); `contrastRatio` — `web/src/lib/userThemes.ts:119` (used at `:133,156,157,163`); `userThemeCSS` — `web/src/lib/userThemes.ts:179` (used at `:199`); `mergeProgress`, `mergeDrafts`, `isEmptyProgress` — `web/src/lib/importBundle.ts:195,268,136` (used at `:279,280,196`). Evidence: `grep -rnw <sym> web/src scripts` returns only the defining file; `scripts/import-audit.mjs` uses `im.applyImport` and `pr.PROGRESS_VERSION` only, and `scripts/theme-contrast.mjs` reads `styles/themes.css` directly rather than importing `userThemes`. Safe now: the function bodies stay and every caller is in the same file.

- [ ] Collapse the focus trap on one owner: `useDrawerFocus(open: boolean, ref: React.RefObject<HTMLElement | null>)` in `web/src/components/ui/useDrawerFocus.ts:25`. Point `TablesDrawer` at it (`TablesDrawer.tsx:54` `asideRef`, `:146-186` delete the inline `inert`+trap, `:13-27` delete `FOCUSABLE`/`focusable`) and delete the duplicate `FOCUSABLE`/`focusable`/trap in `PromptDialog.tsx:9-20,36-62` in favour of a shared primitive. Evidence: `grep -rnw useDrawerFocus web/src` shows only `ChaptersDrawer.tsx:5,19` and `Shell.tsx:7,137`; the inline copies are live at the cited lines. Safe now: the hook was "extracted from TablesDrawer.tsx's proven mechanics" per its own header, and its restore path at `:66-70` is a superset of TablesDrawer's `:181-185` (it also falls back to `#main`). Caveat carried into Amended: the hook focuses the first focusable, so do not point `PromptDialog` at it unchanged.

- [ ] Collapse the two HTML escapers: export one five-character escaper from `web/src/lib/markdown.ts:7-17` and import it in `web/src/components/screens/BuildingScreen.tsx:16-18` (`esc`). Evidence: both maps are `& < > " '` exactly; `grep -rn "escapeHtmlAttribute\|escapeHtml\b" web/src` shows `escapeHtmlAttribute` is private to markdown.ts and `esc` is private to BuildingScreen. Safe now: same character set, same output. (The report's structured-JSX follow-on is a larger change and is not required for the collapse.)

- [ ] Delete the local `dialectLabel` in `web/src/components/screens/ReviewScreen.tsx:222-224`, import `dialectLabel` from `web/src/lib/dialect.ts:36`, and resolve the argument with `effectiveDialect(undefined, course)`. Evidence: call sites are `:45` and `:90`, both `dialectLabel(course.dialect)`; the lib function is already imported by `TablesDrawer.tsx:8,206` and `SqlCell.tsx:9,374`; `effectiveDialect` is the resolver `HomeScreen.tsx:131`/`LibraryScreen.tsx:80` use. Safe now: the local body is `dialect === 'sqlite' ? 'SQLite' : 'PostgreSQL'`, identical to the lib body; `effectiveDialect` supplies the type the lib signature needs.

- [ ] Delete `isDialectValue` in `web/src/lib/dialectChoice.ts:38-40`, import `isDialect` from `./dialect`, and use it at `:55` and `:147`. Evidence: `dialectChoice.ts:1` already imports from `./dialect`; `isDialect` at `dialect.ts:15` is the same two-value check. Safe now: one predicate, one truth.

- [ ] Add one abortable pause and delete both locals: a `sleep(ms, signal)` helper in `web/src/lib/`, replacing `pauseFor` — `BuildingScreen.tsx:109-123` (used at `:547`) and `pause` — `ChallengeLabScreen.tsx:166-178`. Evidence: both bodies are the same `Promise`, `{ once: true }` abort listener, `clearTimeout`, and early resolve when already aborted; `grep -rn "function pause\|pauseFor" web/src` shows no shared helper exists. Safe now: behaviour-identical extraction.

- [ ] Add `enrolledCourses()` to `web/src/lib/model.ts` and call it from `HomeScreen.tsx:23` and `CourseScreen.tsx:28`. Evidence: both are `COURSES.filter((c) => c.enrolled && c.built)`; `builtCourses` at `model.ts:28` is the wrong filter and is deleted above. Safe now: identical predicate, one definition.

- [ ] Delete the `measured` re-derivation and render the right card from `report.totals`: remove `web/src/components/screens/BuildingScreen.tsx:92,145,288-293,675` and derive `ran`/`failed`/`rows`/`ms` from `report.totals` at `:838-847`. Evidence: `reportFromOutcome` already computes `totals.verified/rows/ms` at `lib/draft.ts:230-237` and `authoredReport` computes them at `:283-290`; the card reads `measured.ran/rows/ms/failed`. Safe now: `totals.verified` equals `measured.ran` on both paths, and `failed` is `totals.exercises - totals.verified` (keep the existing `Math.max(0, …)` guard because `report.totals` has no `failed` field).

- [ ] Compute the stale-attempt flag once: replace the two identical expressions at `ProgressScreen.tsx:93` and `:139` with one per-row value (or a `staleFor(e)` helper beside `stampFor`). Evidence: both are `isEarlierVersion(e, stampFor(findExercise(e.id)?.block, datasetVersion))`; `stampFor`/`isEarlierVersion`/`datasetVersion` are already imported at `ProgressScreen.tsx:5,6`. Safe now: same inputs, one evaluation.

- [ ] Collapse `TopNavLink` and `NavButton` — `web/src/components/Shell.tsx:24-35` and `:116-124` — into one component with a class `variant`. Evidence: both render `<Icon name={icon} />`, a label span, and `{badge != null && <span className="navbadge">{badge}</span>}`; they differ only in class (`toplink`/`toplink-label` vs `nav`/`navtext`), `aria-current`, and the badge type (`string` vs `React.ReactNode`). Safe now: mechanical merge; the `aria-current` on the top nav can live on the `variant` branch.

- [ ] Extract one `GenerationLog` component for the duplicated `.stream` markup — `BuildingScreen.tsx:811-815` and `ChallengeLabScreen.tsx:493-497`. Evidence: both render `log.map((l, i) => <div className="ln" key={i}><span className="t">{l.t}</span><span dangerouslySetInnerHTML={{ __html: l.html }} /></div>)` over the same `{ t: string; html: string }[]` shape. Safe now: identical markup and data type.

- [ ] Move the orphaned doc block at `web/src/lib/progress.ts:102-106` ("True when a snapshot came from the sample-data generator…") down to `isSampleProgress` at `:126`. Evidence: read of `:100-127`; `migrateProgress`'s own JSDoc starts immediately at `:107`, so the first block documents the wrong function. Safe now: comment-only move.

- [ ] Correct the false doc at `web/src/lib/pipeline.ts:11-13` ("the HTTP adapter replays the same cadence"). Evidence: `grep -rnw runPipeline web/src scripts` shows one caller, `BuildingScreen.tsx:232`, in a screen that runs under both adapters; no adapter imports `pipeline.ts`. Safe now: comment-only.

- [ ] Carry the tone on the `.pill` classes instead of inline objects — `ChallengeLabScreen.tsx:506-508,587-589` and `ReviewScreen.tsx:40` repeat `{ background: 'var(--green-soft)', color: 'var(--green)' }` and the amber/red variants. Evidence: read of the three sites; `.pill`/`.pill.subtle` already exist in `styles/querycraft.css`. Safe now: style-only.

## Amended

- [ ] Modals fake fields — right direction, exact remedy widened. Files: `web/src/components/Modals.tsx` (path corrected from `components/screens/Modals.tsx`). Delete `level`/`context`/`dialect` state and `Field`s at `:41-43,58-66` and pass only `{ title }` at `:69`; delete `position` at `:143,149-157` and pass only `{ title, goal }` at `:160`; delete the `prompt` state and `Field` at `:80,86,96`. Evidence: `store.tsx:1637-1644` destructures `{ title }` only, `:1703-1705` destructures `{ title, goal }` only, `:1647-1653` reads `title`/`summary`; `grep -rn "saveCourseEdits\|addLesson(" web/src --include=*.tsx` gives exactly `Modals.tsx:69,160`. What changed: also narrow the declared seam so the phantom params stop existing — `store.tsx:265` `saveCourseEdits` to `{ title?: string }`, `store.tsx:268` `addLesson` to `{ title: string; goal?: string }`. The report only touched the caller, which would leave the store advertising fields no body reads. `goal` for `saveCourseEdits` is dropped from the call but its state stays, because `s.setReviewGoal(goal)` at `:69` is a live writer.

- [ ] ChallengeLab fabricated prompt and Save buttons — direction right, the prompt remedy corrected. Delete the two "Save challenge" buttons at `ChallengeLabScreen.tsx:550` and `:626` (`s.toast('Saved to your challenge set.')`; no store writer exists and both adapters' `draftChallenge` are no-ops: `pglite.ts:165-169`, `http.ts:512`). For the prompt at `:596`, replace the hardcoded sentence with the authored reference's own prompt rather than "the request that was actually sent" — `run()` sends no request to a generator; it validates `referenceBlock()` (`:19-29`, `ex.5.1`), so the honest text is `block.prompt`. Evidence: read of `run()` `:138-287` (only `checkAnswer`/`draftChallenge` calls) and the `referenceBlock` body.

- [ ] Checklist state machine — right direction, the shared shape corrected. One `useChecklist(steps)` is fine, but the two lists are not the same: `PIPE_STEPS` has five slots and states `'queued' | 'running' | 'done'` (`BuildingScreen.tsx:28-54,142,191-197`), while `CL_STEPS` has four slots and states `'idle' | 'running' | 'done'` (`ChallengeLabScreen.tsx:11-16,81,157-163`). Neither has a `'failed'` tag (failures are reported as text/`failure`, not as a checklist state). The hook must take the steps and the empty-state word (or normalise `queued`/`idle` to one name) and keep `applyStages` with BuildingScreen, since it maps gateway stage names and has no counterpart in the Lab.

- [ ] `exercisesWithReference(course)` — right idea, wrong arity. `referenceExercises` (`BuildingScreen.tsx:101-105`) is course-scoped, but `referenceBlock` (`ChallengeLabScreen.tsx:19-29`) scans every `COURSES` entry and then picks `ex.5.1` (or the first). A per-course helper can serve both only as `COURSES.flatMap((c) => exercisesWithReference(c))` at the Lab call site. `moduleExerciseBlocks` is at `model.ts:46` and both locals already apply the same `typeof solution === 'string' && solution.trim()` filter.

- [ ] Focus trap — the hook already has the `#main` fallback the report says to "extend it with" (`useDrawerFocus.ts:70`); switching TablesDrawer to the hook gains it, so no hook change is needed for that. What is needed: do not point `PromptDialog` at `useDrawerFocus` unchanged. The hook focuses `root.querySelector(FOCUSABLE)` (the first focusable, here the Close button at `PromptDialog.tsx:97`), while the dialog deliberately focuses and selects the input (`:27-34`), and the dialog unmounts when closed so the hook's `inert` effect (`useDrawerFocus.ts:28-33`) is idle. Either add an `initialFocus` option to the hook or share only `FOCUSABLE`/`focusable` and the Tab-cycling function.

- [ ] BuildingScreen extraction — direction right, tag list corrected. Proposed boundaries are sound, but the machine the effect encodes is: initial `queued` (not `idle`, which is the Lab's word) → `pacing` only on a fresh run (`:227-250`; a resume skips it) → branch `availability.available || resuming` `:261` → `submitting` (`:584`) → `watching` (`:450-549`) → `settling` (`:264`), or blocking `generating` (`:606`) → `settling`; `settling` splits `awaiting_approval` → `awaiting` (`:303`) | `active && plan` → `adopting` (`:353`) → `finishing` (`:198`) | else `failed` (`:330`, and also `active` with no plan fails at `:330`). From `awaiting`: `approving` (`:377`) → `watching` → `settling`, or `discarding` (`:425`). Entries: `resuming` (`:217,580`) enters `watching`; `resumingBuild` (`:554`) enters `approving`. The no-provider path (`:622-702`) is `pacing` done → `verifying` (`:651`) → `drafting` (`:686`) → `finishing`. Corrections to the report: tag 0 is `queued`; `settling`/`finishing` are the local functions `settle`/`finish`, not stored states; the run also has a mid-run `adopting` from `watchJob` (`setBuilt(s.adoptStoredCourse(record))` `:515,531`) plus `recordStatus`; and `watchJob` has two failure exits (`misses >= 3` `:467`, deadline `:538`) that are not the `settling` failure. `failed → restart` is the `setRunKey((k) => k + 1)` button at `:783`.

- [ ] Doc half dropped, half kept. `pipeline.ts:11-13` is false and is corrected above. `dataset.ts:83-85` ("no screen reads it yet") is accurate: `datasetReason`/`dialectReason` are read by `scripts/course-audit.mjs:48-49,112-113` and `scripts/content-audit.mjs:170`, and an audit is not a screen. Drop that half; if anything, the comment could add "the audits do", but it is not a false claim.

## Dropped

- `dataset.ts:83-85` "the audit does read it, so the claim is false" — counter-evidence: the comment says "no screen reads it yet". The named readers are `scripts/*.mjs`, which are not screens. No edit required.

## New

- The "drop the export keyword" sweep is incomplete. The same `grep -rnw SYM web/src scripts` test wave 1 used finds more exported-but-only-internally-used symbols in the in-scope trees: `storedDialectChoice` (`lib/dialectChoice.ts:83`, used only by `chapterDialect` at `:112`), `dayKey` (`lib/progress.ts:233`, used at `:241,246`), `contentRows` / `ContentRow` / `StoredContent` (`lib/contentSeed.ts:103,21,32`, used only by `planContentSeed`), `OutlineGroup` (`lib/model.ts:11`), `PipelineOptions` (`lib/pipeline.ts:3`), `ThemeDef` (`lib/themes.ts:12`), `SearchHit` (`lib/search.ts:33`), `Route`/`RouteState` (`lib/route.ts`), `DraftChapterReport`/`DraftFailure` (`lib/draft.ts:19,31`), `RunBreadcrumb` (`lib/runResume.ts:19`), `ContentVersions` (`lib/progress.ts:9`), `MergeResult` and the `Import*` result types (`lib/importBundle.ts`), and the `offlineBundle.ts` record types. Note that wave 1's Earned list credits `storedDialectChoice` to `DialectBadge.tsx:6-12`, but `DialectBadge` imports only `chapterDialect/chooseDialect/dialectChoiceVersion/servedDialects/subscribeDialectChoice/switchTarget` and does not import `storedDialectChoice`. Enumerate with a loop over each `web/src/lib/*.ts` export and count references outside its own file; drop `export` on the ones that reach zero and add nothing. Evidence: `grep -rnw storedDialectChoice web/src scripts` returns `:83` and `:112` only.

## Still uncertain

- `PipelineOptions.steps` and the `step`/`all-done` events (`lib/pipeline.ts:3,61-72`; `types.ts` event union). The one caller (`BuildingScreen.tsx:232-246`) passes `steps: beats.map(() => '')` and ignores everything but `ev.type === 'log'`. Deleting `steps` shortens the awaited pacing from `max(3*420, 3*380)=1260ms` to `3*380=1140ms`, so the intended cadence needs a runtime read.

- `resetDialectChoice` (`lib/dialectChoice.ts:99`) still has no caller; whether the UI should offer the undo is an owner decision.

- `BuildingScreen` local `report`/`built` (`:148-149,287,353`) versus `s.draftReport`/`s.draftCourse`: resolving this needs the reducer split first.

- `ChallengeLabScreen`'s two generate buttons (`:386` authored evidence `run`, `:387` gateway `generateOnGateway`) are both wired; deleting one is a product call.

- `useEngineCapabilities` module-level `pendingProbe` (`DialectBadge.tsx:35-48`) shares one in-flight promise across mounts and adapter changes; a runtime check is needed to call it safe or not.

- `AppearanceScreen` network-latency switch (`:187`) — resolved as live, not uncertain: `store.tsx:421,484` writes `api.config.latency`, and `pglite.ts:52-54` reads it in `delayMs`. Keep it.
