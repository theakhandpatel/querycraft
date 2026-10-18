# Frontend review: `web/src` (React 18 + TypeScript strict + Effect v4)

Scope: the frontend track. Focus on `state/store.tsx`, `api/` (`contract.ts`, `ai.ts`,
`execution/`), `components/screens/`, and `lib/`. Measured against the
thermo-nuclear review bar and the code-simplification skill.

Checks run before writing (both from `web/`):

- `npm run typecheck`: clean.
- `npm run lint`: 0 errors, 29 warnings. Thirteen are `anti-slop` findings in
  the decoding seam under review: `api/ai.ts` 8 (7 `no-chained-type-assertions`
  plus 1 `no-unknown-returns`), `api/contract.ts` 2, `api/planContract.ts` 2,
  `api/pglite.ts` 2, `api/sqliteEngine.ts` 1.

## Verdict

The frontend is honest and well documented, but the user's hypothesis is
correct: the plumbing is over-built in three places at once, and two of the
three fixes delete code rather than move it. `state/store.tsx` is a single
2038-line provider with 111 exported members, 39 `useState`, 57 `useCallback`,
21 `useEffect`, and nine mirror refs; its context value is rebuilt on every
render, so one HTTP request or one toast re-renders all 32 consumers.
`BuildingScreen.tsx` encodes the entire generation lifecycle as one 537-line
effect holding four nested closures, a self-written poll loop, and a
module-level mutable `Map`, where a small reducer would make the resume,
approve, and interrupted-build paths one table instead of four interleaved
branches. The gateway decoding strategy is split in half: `contract.ts` and
`planContract.ts` use `effect/Schema`, while `api/ai.ts` hand-rolls 26
`asX(value: unknown)` decoders across ~800 lines, which is where every
`anti-slop` warning above lives. The seam itself is triplicated
(`ExecutionServiceShape`, `ExecutionApi`, and the 50-wrapper `createExecutionApi`
facade), and Effect earns its keep at exactly two points: `retry`/`timeout` in
`execution/http.ts` and the `Schema` decoders. The fast, dependency-free
code-judo move is to collapse the store's duplication and re-render cost, then
retire the hand-rolled AI decoders in favour of the `Schema` vocabulary the repo
already owns. No new package is needed for any of this.

## File sizes (measured with `wc -l`)

| File | Lines | Note |
| --- | --- | --- |
| `src/state/store.tsx` | 2038 | single provider, 111 members |
| `src/data/curriculum.ts` | 1315 | data table, acceptable |
| `src/api/ai.ts` | 1022 | ~800 lines of hand-rolled decoders |
| `src/api/pglite.ts` | 980 | engine adapter, cohesive |
| `src/components/screens/BuildingScreen.tsx` | 879 | one 537-line effect |
| `src/api/planContract.ts` | 707 | schema + grading, cohesive |

## Finding 1: the single store is the structural problem, and its fix is deletion

`state/store.tsx:394` opens `StoreProvider`. The file header at line 1 admits the
size and defers the split. That deferral costs measurably today, and the cost is
not merely aesthetic.

The context value is a plain object literal at `state/store.tsx:1868`, not
wrapped in `useMemo`. `useMemo` appears exactly once in the file, at line 490,
for the API object. So every provider render produces a new context identity and
every `useContext` consumer re-renders. `apiLog` and `apiCount` are set from
`onLog` (`store.tsx:478`) on every gateway round trip and inside the PGlite
adapter's `note`, and only `AppearanceScreen.tsx:190` reads `apiCount`. A single
query therefore re-renders all 32 `useStore()` call sites, including `SqlCell`
and every screen behind `App`. The same applies to `toast`, `progress`, and
`uiState`. This is the one place the app pays a real runtime cost for the
mega-provider.

The provider also carries nine refs that exist only to defeat stale closures:
`storedRef` (449), `sessionRef` (467), `hydratedRef` (476), `dirtyRef` (477),
`customThemesRef` (539), `progressRef` (644), `progressOwnerRef` (658),
`persistFailureNoticed` (760), `uiStateRef` (996). Each is a second copy of state
that a focused module would not need.

Progress writes repeat the same three-line ritual at 14 sites: `setProgress(next)`
then `persistProgress(next)` then `syncProgress(next, courseId)`. `syncProgress`
is called at lines 835, 846, 1088, 1113, 1130, 1155, 1182, 1204, 1226, 1276,
1291, 1306, 1357, 1367, 1392. `persistProgress(next)` appears alone 9 times
besides. That repetition is the signal the skill names: a missing helper, not
nine opportunities for a local edit. One `commitProgress(next, courseId?)` that
persists, syncs, and returns the snapshot would delete the clustering and make
every mutation a two-liner. `store.tsx:1082` (`saveProgress`) already reads
through `progress` while `store.tsx:1121` (`addStudyTime`) reads `progressRef`;
a single commit path ends that inconsistency too.

The owner-change and hydration logic is likewise duplicated. `initialProgress` is
called at line 411 (initializer) and line 664 (owner change), and the same
`hydratedRef.current = false; dirtyRef.current = false` pair is repeated in
`signup` (890), `login` (901), `logout` (915), and the owner effect (662). The
hydration effect at 801 to 854 re-implements the sample/missing/migrated
handling that `initialProgress` already does for the disk path.

Remedy, in deletions first: extract the progress domain (state, `commitProgress`,
the three refs it really needs) into `state/progressStore.tsx`; extract the
router and chrome (`screen`, `activeLesson`, drawers, modal, toast,
`catalogVersion`) into `state/uiStore.tsx`; extract accounts plus the
stored-course catalog into `state/catalogStore.tsx`; leave build/draft in its own
store. Each provider memoizes its value (`useMemo` over its own fields), so a
progress write no longer re-renders the shell and an API log line no longer
re-renders `SqlCell`. Then delete the 7 dead members listed in Finding 5 and
collapse the 14 sync sites to `commitProgress`.

## Finding 2: `BuildingScreen` holds the run lifecycle as one 537-line effect

`BuildingScreen.tsx:184` to `720` is a single `useEffect` whose async IIFE
contains four nested closures: `settle` (264), which itself handles the
awaiting-approval, failure, and activated branches and emits the stage log;
`runApproval` (377); `discardOutline` (425); and `watchJob` (450), a hand-written
`for (;;)` poll loop with its own deadline arithmetic and three-strike error
handling. The file has 63 `if (` statements and 14 `await`s, and two of its
states are refs passed to buttons (`approveRef` 166, `discardRef` 167) because
the closures own the run.

This is the spaghetti-growth the skill asks to flag: the run has real states
(fresh submit, pacing, blocked request fallback, watching a job, awaiting
approval, approving, discarding, resumed run, resumed stored course, failed), but
they are encoded as branch guards inside one function rather than as a typed
state. The resume and interrupted-build paths the task names are three extra
`const resuming = s.resumeRun`/`s.resumeBuild` reads (217, 221) plus an early
`if (resumingBuild) { await runApproval(...); return; }` (554). Adding a state
means another guard in an already-busy flow.

Two supporting smells. `resumedBuildJobs` at line 134 is a module-level mutable
`Map` that exists only to survive React StrictMode's double mount; global state
outside React to paper over a mount quirk is the "magic handling that hides
simple structure" the skill rejects. And the log renders `dangerouslySetInnerHTML`
(813) from strings built by a hand-rolled `esc` (16), so escaping is manual where
React would escape structured content for free.

Remedy: make the lifecycle a `useGenerationRun()` hook with a
`useReducer` over a discriminated union (`{ tag: 'idle' | 'pacing' | 'submitting'
| 'watching' | 'awaiting' | 'settling' | 'failed', ... }`). `settle` becomes a
`switch (outcome.status)` with one handler per status; `watchJob` becomes a pure
async function `poll(api, handle, { onTick, signal })` that the reducer drives,
and `Schedule.spaced`/`Effect.timeout` (already a dependency) can replace the
manual `pauseFor`/deadline code. The StrictMode `Map` disappears because the
submit step becomes idempotent under a `runId` in the reducer instead of in
module scope. The log becomes an array of `{ text, tone }` rendered as JSX, which
retires `esc` and the `innerHTML` path.

## Finding 3: two decoder vocabularies, one of them hand-rolled

`api/contract.ts:1` and `api/planContract.ts:23` import `effect/Schema` and
decode the gateway bodies declaratively (`Schema.decodeUnknownEffect` at
`contract.ts:50`; `Schema.decodeUnknownSync` at `planContract.ts:300`).
`api/ai.ts` does the opposite: it hand-writes 26 `asX(value: unknown)` functions
starting at line 214 (`asAdminAIConfig`, `asStages`, `asAttempts`, `asEvidence`,
`asAiArtifact`, `asAiError`, `asAiOutcome`, `asCoursePlan`, `asCourseRecord`,
`asJob`, and so on) plus 9 envelope/outcome helpers, roughly 800 lines. The
sibling contract module proves the declarative form already works here.

The hand-rolled form is also where the lint warnings live. `ai.ts:332`, `433`,
`476`, `478`, `999`, `1008`, `1018` are `anti-slop(no-chained-type-assertions)`,
and `ai.ts:547` is `no-unknown-returns`. The rule's advice is literal: parse
untrusted input at its boundary. The clearest case is `asAiArtifact`
(`ai.ts:320`), which validates a few fields, then does
`...(value as unknown as AiArtifact)` at line 332, spreading an unvalidated object
and overriding two fields. That is exactly the assertion chain the rule rejects:
a half-parsed artifact can carry fields nothing checked.

Remedy: port the AI contract to `Schema` structs alongside the existing
`AiCoursePlan`/`AiOutcome` types, then replace the `asX` calls with
`Schema.decodeUnknownSync` (or the `Effect` variant where the seam is already
Effect-typed). This keeps one decoding vocabulary, makes "malformed gateway body
fails decoding" the uniform rule the `contract.ts` comment states, deletes the
26 helpers and 9 envelopes, and clears the 8 `api/ai.ts` lint warnings with no
behaviour change.
Because the outcome/refusal shapes are load-bearing, land it behind the existing
`audit-ai` and `audit-course-store` scripts, which already bundle `ai.ts`.

## Finding 4: the seam surface is triplicated; Effect earns its keep in two spots

The execution contract is written out three times: `ExecutionServiceShape` in
`api/execution/shared.ts:42` (~50 Effect-returning methods), `ExecutionApi` in
`types.ts:511` (~50 Promise methods), and the `createExecutionApi` facade in
`api/execution/service.ts:84` to `174`, where every method is a mechanical
`(args) => call((svc) => svc.method(args))`. Adding one operation means editing
four files (shape, promise type, facade, and the adapter that implements it).

The user asks whether Effect is ceremony at this seam. The honest split: it earns
its keep in `execution/http.ts`, where `Effect.retry(Schedule.recurs(...))` and
`Effect.timeout` at lines 108 to 109 are the single implementation of the
retry-on-read/timeout policy, and in the `Schema` decoders of Finding 3. It does
not earn its keep in two places. First, `execution/pglite.ts` uses `Effect.gen`
at 12 sites as an `async`/`await` with `Effect.sleep` for the simulated-latency
toggle; the same code is plainer as async functions returning `Promise`, and the
store consumes promises anyway. Second, the facade's 50 wrappers restate what the
shape already declares. The special cases are only `warmUp` (fire and forget,
`service.ts:107`), `saveDraft`/`getDraft` (IndexedDB, not the service,
`service.ts:117`), and the memoised `aiAvailability` (`service.ts:76`).

Remedy: keep Effect at the HTTP transport and in the decoders. Provide one typed
`toPromiseApi(service)` that maps the mechanical majority of `ExecutionServiceShape`
methods to their promise form, and let the three special cases above stay
explicit. That removes the third copy of the surface and the 50 hand-written
wrappers. This is optional relative to Findings 1 to 3; it is record-keeping
debt, not a correctness one.

## Finding 5: dead store members and duplicated derivations in screens

Exhaustive grep of `web/src/components` and `App.tsx` shows 7 members of the 111
in `StoreValue` with zero consumers: `openLesson` (`store.tsx:226`),
`saveProgress` (235), `setGoalInput` (276), `setContextState` (282),
`setCourseSubtitle` (280), `catalogVersion` (265), and `moduleExerciseBlocks`
(337). `moduleExerciseBlocks` is telling: the store exposes it at line 2028 with
a cast, but both call sites (`BuildingScreen.tsx:103`, `ReviewScreen.tsx:189`)
import it from `lib/model.ts` directly. `setGoalInput` is never called by a
screen; `CreateScreen.tsx:17` seeds local state from `s.goalInput` and
`submitBuild` is the only writer, so the setter is pure surface. `catalogVersion`
is not read by any consumer; the re-render it triggers comes from the provider
re-rendering, so the value never needs to leave the store. Remove all seven.

Derivation logic is also leaking into screens. `lib/model.ts:46`
(`moduleExerciseBlocks`) already owns "the exercises of a module", yet two
screens re-derive a narrower set by hand. `BuildingScreen.tsx:101`
(`referenceExercises`) flattens `course.modules` then filters for a non-empty
`solution`; `ChallengeLabScreen.tsx:18` (`referenceBlock`) writes the same
triple-nested `for` loop over `COURSES`/`modules`/`blocks` and the same filter.
`LessonScreen.tsx:76` merges database docs with curriculum blocks and then calls
`chapterOutline`, so the "which prose wins" rule lives in a screen. These are the
"bespoke helpers where a canonical utility exists" case. Add
`exercisesWithReference(course)` and `chapterBlocks(module, docs)` to
`lib/model.ts` and delete the screen-local copies.

## Third-party verdict

No new package is warranted, and one already-present package is underused.

- JSON decoding: the repo already depends on `effect` and already uses
  `effect/Schema` in two modules. `ai.ts` should use it instead of hand-rolled
  parsers. Adding `zod` or `valibot` would introduce a second decoder vocabulary
  beside the existing one, which is the opposite of the fix.
- HTML escaping and the generation log: React escapes text children for free.
  Making the log structured data retires the hand-rolled `esc` and the
  `dangerouslySetInnerHTML` path; `DOMPurify` stays where it belongs, in
  `lib/markdown.ts`.
- State management: this is a decomposition problem, not a library problem.
  `useMemo` plus split contexts fixes the re-render cost with no dependency. A
  selector library such as `use-context-selector` would also work but is not
  needed if the stores are split.
- The generation poll loop: `effect` already provides `Schedule.spaced`,
  `Effect.timeout`, and interruption, which cover the hand-written `for (;;)` and
  `pauseFor`. Use them only if the loop moves out of the component, per
  Finding 2; otherwise the hand-rolled loop stays the simpler option.

## Remediation sequence

1. Collapse the progress writes in `store.tsx`: add `commitProgress(next,
   courseId?)`, route the 14 `syncProgress` sites and 9 `persistProgress` sites
   through it, and remove the 7 dead `StoreValue` members. Small, mechanical,
   covered by existing audits.
2. Split `store.tsx` by concern (progress, ui/router, accounts+catalog) with
   memoised values. Do it one provider at a time, keeping `useStore()` as a
   facade during the move so no screen changes in the same commit.
3. Add `exercisesWithReference` and `chapterBlocks` to `lib/model.ts` and delete
   the screen-local derivations.
4. Extract the generation lifecycle from `BuildingScreen.tsx` into a
   `useGenerationRun()` reducer plus a pure `poll` function; render the log as
   JSX and delete `esc` and the module-level `resumedBuildJobs` map.
5. Port the `api/ai.ts` decoders to `effect/Schema`, run `npm run audit-ai` and
   `npm run audit-course-store`, and confirm `npm run lint` drops the 8 warnings.
6. Optional: replace the 50 mechanical wrappers in `createExecutionApi` with a
   typed `toPromiseApi(service)` and leave `warmUp`, `saveDraft`/`getDraft`, and
   the `aiAvailability` memo explicit.

## What is unfinished

Steps 2 and 4 are the largest and riskiest; step 1 and step 3 are safe to land
first and independently. Nothing here was applied: this is a review only, and no
file under the repository was modified. The 29 lint warnings are the pre-existing
baseline, reported and not introduced by this review.
