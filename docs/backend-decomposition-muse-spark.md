# Backend decomposition

This note judges the gateway structure. It keeps measured evidence. It drops size framing. You read each claim against the file it cites. You run the check it names before you change code.

## 1. Necessity

The byte cap marshals every row twice. You judge each piece below the same way. You name its problem, its reality, and its verdict.

### Per-row marshal

The byte cap marshals each row to count it. Execute calls json.Marshal at backend/internal/engine/execute.go:83, then reads len at :91 and :95, then drops the bytes. The cap itself is real. The contract fails oversized results with RESULT_LIMIT_EXCEEDED at :114. The response encoder marshals the same rows again. Keep the cap. Replace the counting marshal with a length estimator. You verify the boundary with a 600-row cap probe.

### Double decode

The evaluate body decodes twice. UnmarshalJSON fills eval.Request at backend/internal/httpapi/handlers.go:215, then fills routing at :222. The comment admits the collision at :199. One exercise key carries the evaluator spec and the perf marker. The collision is self-imposed. Two keys, or one shadow struct, hold both shapes. Delete the second decode. Decode once into a struct that holds both halves.

### Two deadlines

Two timers govern one statement. The handler arms context.WithTimeout at backend/internal/httpapi/handlers.go:115. Execute arms a second one at backend/internal/engine/execute.go:42. Both bounds matter. Admission needs a budget and execution needs a deadline. The nesting still double counts one timeout. Evaluations call Execute directly at backend/internal/eval/eval.go:172, with no handler above them. Keep the engine deadline as the execution owner. Shrink the handler timer to admission, then hand the request to Execute.

### Tables drawer

The drawer pays per-table round trips. Names run one Execute at backend/internal/httpapi/handlers.go:297. Columns run one PRAGMA per table at :315. Counts run one count per table at :339. Four tables pay nine round trips. The workspace description is real. The PRAGMA fan-out is inherent. SQLite exposes no multi-table describe. The counts are optional. The preview shows headers only at :285. Keep names and PRAGMA calls. Batch or drop the counts. You confirm the drawer with a four-table workspace.

### Per-request AI service

Each generation request rebuilds its service. aiServiceFor copies the config at backend/internal/httpapi/ai.go:60. The copy repeats limit wiring per request. The owner rule is real. The owner comes from the session and never from the body. The rebuild exists to stamp that owner. Keep the rule. Build the base service once in UseAI at :29. Stamp the owner per call, not per build.

### Owner stamp wrapper

The wrapper stamps the session owner on saves and lists. ownerScopedDepot covers six methods at backend/internal/httpapi/ai.go:78. SaveChallenge, SaveCourse, and SaveRun stamp the owner. The For methods force the store filter to the owner. The forgery rule is real. A caller must never name the user it acts for. Adding an owner parameter touches the whole Depot plus three backends. Six one-line stamps cost less. Keep the wrapper. You probe cross-account reads for 404s.

### Stage snapshot copies

Each mark copies the stage slice. snapshotStages copies at backend/internal/ai/pipeline.go:143. notify copies stages, attempts, and chapters at :152. A poller reads while the run writes. The race is real. Runs mark dozens of times. The lists stay short. The copies cost little and remove the race. Keep the copies. An atomic pointer adds machinery for no measured gain.

### Double redaction

Each transcript string passes two scrubbers. redactText calls engine.RedactSecrets at backend/internal/ai/transcript.go:174, then three local patterns. The engine scrubber covers DSN shapes at backend/internal/engine/redact.go:19. The local patterns cover bearer tokens, named secrets, and key prefixes at backend/internal/ai/transcript.go:156. A model can echo a key it received. A provider error can echo a request body. The sets cover disjoint shapes. Bounds cap the text at backend/internal/ai/transcript.go:45. Keep both passes.

### Silent zero count

A failed count reports zero. sqliteRowCount returns 0 on failure at backend/internal/httpapi/handlers.go:338. The drawer then shows an empty table for a table with rows. Tolerance for a missing count is reasonable. Reporting unknown as zero misleads. Keep the tolerance. Return an explicit unknown instead of zero. You check the drawer against a table with rows.

### Identifier quoting

Table names pass through one quoter. quoteSQLiteIdent wraps names at backend/internal/httpapi/handlers.go:356. A keyword table still resolves. The problem is real. No package replaces one line. Keep the quoter.

## 2. Entanglement

The store imports ai in six files. You read one line per edge. Each line ends with Keep or Remove.

- The store imports ai in store.go, courses.go, challenges.go, runs.go, sqlite_depot.go, and memory.go. Counts show 39 ai.Record, 38 ai.CourseRecord, 35 ai.Depot, 28 ai.StatusActive, and 25 ai.RunTranscript. Store embeds ai.Depot at backend/internal/store/store.go:83. Persistence speaks the generation dialect. Remove.
- The engine imports perf for plan types at backend/internal/engine/explain.go:12. Grading owns those types, and grading already imports the engine. The direction points the wrong way. Remove.
- The evaluator imports the engine at backend/internal/eval/eval.go:14. Evaluate runs learner SQL through Executor at :168 and compares QueryResult values. Grading needs execution. Keep.
- The environment manager imports the engine at backend/internal/envdb/scratch.go:28 and manager.go:36. Scratch hands out an Executor per account. Routing needs execution. Keep.
- The environment manager imports metrics at backend/internal/envdb/manager.go:37. The registry arrives as an optional field at :87. Each use checks nil at :774, :781, and :789. Observability only, no policy. Keep.
- The API imports auth. requireUser gates owner routes at backend/internal/httpapi/auth.go:48. Identity checks belong on the route. Keep.
- The API imports the store. Handlers read progress, events, and sessions through it. Persistence belongs behind the route. Keep.
- The API imports ai. Generation routes invoke the pipeline through the service. The call is real. Shrink the import to Service plus request types when the store edge moves. Keep.
- The API imports the engine. Statement routes call Execute with resolved limits at backend/internal/httpapi/handlers.go:137. Execution belongs on the route. Keep.
- The API imports eval. The grade route calls Evaluate at backend/internal/httpapi/handlers.go:189. Grading belongs on the route. Keep.
- The API imports envdb. statementExecutorFor routes anonymous callers to shared data and signed-in learners to workspaces. Isolation routing belongs on the route. Keep.
- The API imports dataset. Tables and resolveDataset serve the drawer and routing. Metadata belongs on the route. Keep.
- The API imports metrics. withObservability labels by route at backend/internal/httpapi/metrics.go:138. Recording belongs beside the mux. Keep.
- The API imports perf for plan bundle types. The same inversion as the engine edge forces it. Move the types down, then read them from the lower package. Remove.
- The API imports webui. The mux serves the SPA fallback at backend/internal/httpapi/server.go:224. One binary serves UI and API. Keep.

AccountLister at backend/internal/envdb/manager.go:678 carries a telling comment. The comment states the interface exists so envdb does not import the store, in a tree where the store already imports ai. The interface narrows the store to UserIDs for ReapUnreferenced at :688. Once the store edge disappears, the workaround loses its reason. Delete AccountLister. Accept the store seam directly. You keep a narrow test fake only if a test needs it, with a comment about tests, never about ai.

## 3. Hand-rolled versus a package

The spec has one writer and three test readers. You check each area against its package. You keep the hand version only when it buys something concrete.

| Area | Package and licence | What it replaces | Verdict | What the hand version buys |
| --- | --- | --- | --- | --- |
| OpenAPI document | kin-openapi, MIT | Hand text in openapi.go plus swaggerUIDocs | Replace | Nothing lasting. A generator over the mux plus contracts removes the drift the tests pin. |
| Metrics exposition | prometheus/client_golang, Apache-2.0 | Registry, histogram buckets, +Inf handling, escapeHelp, escapeLabel | Keep | Dependency-free scrape, bounded route labels, declared-before-observed names, no transitive modules. |
| Provider wire and retry | Vendor SDK plus cenkalti/backoff, MIT, and hashicorp/go-retryablehttp, MPL-2.0 | HTTPProvider client plus pipeline repair budgets | Keep | Visible timeout, byte cap, key redaction, single-attempt semantics, repair owned by Limits. |
| Raw SQLite driver | modernc.org/sqlite, BSD-style module licence | Raw driver over modernc.org/sqlite/lib | Keep | Verbatim TEXT for DATE-declared columns, browser parity, no time.Time conversion. |
| SQL classification | pganalyze/pg_query_go, MIT | isBlankSQL plus dialect profiles | Keep | Single-statement scope, profile-as-data with a mirror test, no parser version lag against pinned PostgreSQL. |
| Store SQL | jmoiron/sqlx, MIT | Hand SQL per backend | Keep the SQL, remove the duplication around it | Explicit dialect split that a helper cannot hide. |
| Sessions | gorilla/sessions, BSD-3-Clause | Opaque tokens plus bcrypt via x/crypto | Keep | Revocation by DELETE, no signing key, empty-password admin preserved. |

You verify the spec claim yourself. The mux defines routes at backend/internal/httpapi/server.go:183. The drift test lists every method plus path pair at backend/internal/httpapi/openapi_test.go:10. The security test pins session routes and 401s in the same file. The contract test pins QueryError and ErrorEnvelope codes against the canonical schemas at backend/internal/httpapi/contract_test.go:1023. The SPA test pins JSON output and the Swagger UI page at backend/internal/httpapi/spa_test.go:38. No file under web/src reads the spec. No external consumer exists. The only readers are the tests plus the docs page served at backend/internal/httpapi/openapi.go:1884. Replace the hand text with a generator. Read routes from the mux. Read schemas from contracts. Fail the build when a route lacks docs.

You read the raw driver header before you judge it. modernc converts TEXT columns declared DATE, DATETIME, or TIMESTAMP into time.Time before database/sql sees them. The header records the version and the defect at backend/internal/engine/sqlite_raw.go:3. The browser adapter reads through sqlite3_column_* and reports stored text. The dialect contract demands stored text at contracts/dialects/sqlite.json. The driver connection type stays unexported with no result-time hook. The write-side time format cannot fix reads. The raw driver reads storage class first at backend/internal/engine/sqlite_raw.go:838, then picks the matching accessor. Declared types supply labels only at :755. This is the one hand-rolled piece that earns its keep. Keep it. You review its unsafe pointer code separately from Execute.

Prometheus stays hand-rolled for structure, not habit. DefaultBuckets straddle the gateway deadline at backend/internal/metrics/metrics.go:31. WritePrometheus renders HELP, TYPE, cumulative buckets, +Inf, sum, and count at :228. escapeHelp and escapeLabel handle the two escaping rules at :311 and :315. routeOfMux bounds cardinality by route at backend/internal/httpapi/metrics.go:382. The client library adds modules and a label model you must then audit. The gateway needs none of that.

The provider client stays hand-rolled. Complete makes one attempt at backend/internal/ai/http_provider.go:143. It enforces a timeout, a byte cap with one-byte overread, and redaction on every failure path. The pipeline owns repair through MaxRepairs. Retry libraries move that policy into defaults the audit must then rediscover. The header comment states the small-surface reason at :14. Keep the client. Keep repair in Limits.

Classification stays hand-rolled. isBlankSQL strips comments and whitespace at backend/internal/engine/execute.go:133. The SQLite profile matches engine sentences at backend/internal/dialect/sqlite.go:66. The mirror test pins the embedded profile to the contract at backend/internal/dialect/dialect_test.go:20. A full parser binds native code, tracks server versions on its own schedule, and invites multi-statement support the design forbids.

Store SQL stays hand-written. Placeholders differ by driver. Schema prefixes differ by backend. Clocks differ by engine. A query helper hides none of that. Deduplicate the shapes around the SQL, never the SQL in section 5.

Sessions stay hand-rolled. NewToken mints 256-bit tokens at backend/internal/auth/auth.go:92. StartSession stores them at :186. BearerToken parses the header at :200. Hash and Verify use bcrypt. The package comment states the JWT rejection at :7. Revocation costs one DELETE. Stateless tokens buy nothing here.

## 4. Abstraction that does not earn itself

Eight seams have one method each. You list each seam with its production implementors and its kind. Kinds use four words only. Adapter means a foreign type. Capability means an optional engine answer. Double means a test stand-in. Wrapper means one caller.

- Dialecter at backend/internal/engine/engine.go:93. One production implementor, SQLiteExecutor at backend/internal/engine/sqlite.go:276. PostgreSQL uses the default. Kind: capability. Verdict: keep.
- LabeledExecutor at backend/internal/engine/engine.go:113. One production implementor, SQLiteExecutor at backend/internal/engine/sqlite.go:283. Kind: capability. Verdict: keep.
- ErrorClassifier at backend/internal/engine/engine.go:138. Two production implementors, PgxExecutor at backend/internal/engine/pgx.go:284 and SQLiteExecutor at backend/internal/engine/sqlite.go:308. Kind: capability. Verdict: keep.
- Diagnostician at backend/internal/engine/engine.go:237. Two production implementors, PgxExecutor at backend/internal/engine/pgx.go:233 and the unavailable executor at backend/internal/engine/unavailable.go:44. One test fake. One caller at backend/internal/httpapi/metrics.go:348. Kind: capability. Verdict: keep.
- Runner at backend/internal/ai/api.go:462. Two production implementors, envdb Manager at backend/internal/envdb/scratch.go:28 and ProvidedRunner at backend/internal/ai/api.go:472. One test double. Callers live in the pipeline at backend/internal/ai/pipeline.go:497. Kind: double plus environment seam. Verdict: keep the interface, move ProvidedRunner behind tests.
- AccountLister at backend/internal/envdb/manager.go:678. Three production implementors through UserIDs on every store backend. One caller at :688. Kind: wrapper. Verdict: delete with the store edge, per section 2.
- secretRedactor at backend/internal/ai/transcript.go:185. One production implementor, HTTPProvider at backend/internal/ai/http_provider.go:329. One caller at backend/internal/ai/transcript.go:189. Kind: capability. Verdict: keep.
- float64Valuer at backend/internal/engine/normalize.go:13. Zero in-repo implementors. Foreign numeric types implement it. One caller at :45. Kind: adapter. Verdict: keep.

You find the fake-compat reason in the Dialecter comment at backend/internal/engine/engine.go:91. You treat compat as test convenience, never as design. The capability split still stands on its own. SQLite differs on dialect, label, and error vocabulary. The default-dialect fallback is real policy. PostgreSQL leaves the options unimplemented by design. Rewrite the comment to cite optionality. Cite the fake nowhere.

Store carries 15 members and Depot carries 17. Store holds 14 methods plus the embedded Depot at backend/internal/store/store.go:47. Depot holds 17 methods at backend/internal/ai/api.go:416. Store callers form three groups. Auth needs accounts and sessions. Learner history needs progress and events. The reaper needs UserIDs. Split Store by caller into three narrow seams. Depot callers need the whole trail together. The service, the jobs registry, and the generation routes read challenges, courses, patches, and runs under one owner rule. Three backends implement all of it today. Splitting Depot into four thin seams trades one honest seam for four. Keep Depot whole.

## 5. DRY

Each route appears in five places. You name each place once. You keep one source.

- The mux defines each route at backend/internal/httpapi/server.go:183.
- The operations map repeats each route for logs at backend/internal/httpapi/metrics.go:185.
- The error mapper groups routes by code at backend/internal/httpapi/server.go:292 and :324.
- The spec repeats paths, methods, and security in backend/internal/httpapi/openapi.go.
- The observability layer re-derives the route label at backend/internal/httpapi/metrics.go:382 and :393.

The operation name decoupling is intentional. The comment states it at backend/internal/httpapi/metrics.go:181. Renaming a path must not rewrite past logs. Keep the mapping. Generate its keys from the mux. Table-drive the error mapper from route patterns. Generate the spec from the mux plus contracts. You keep policy in tables. You delete copied route strings.

Store SQL repeats across backends. The depot statements exist for PostgreSQL and again for SQLite. The in-memory backend repeats the same semantics in Go maps at backend/internal/store/memory.go. Some repetition must stay. Placeholders differ, with $1 in PostgreSQL and ? in SQLite. Schema prefixes differ, with app.* in PostgreSQL and bare names in SQLite. Clocks differ, with now() in PostgreSQL and the timestamp helper in SQLite at backend/internal/store/sqlite.go:163. You check the exact difference in session expiry. PostgreSQL compares expires_at against now() in SQL at backend/internal/store/postgres.go:339. SQLite compares against a formatted timestamp string at backend/internal/store/sqlite.go:324. Memory compares time values in Go at backend/internal/store/memory.go:528. The SQLite format stays fixed-width microsecond UTC, so lexical order matches time order. That difference is load-bearing. Keep all three spellings.

Real duplication sits around the SQL. Owner filtering repeats in ChallengesFor, CoursesFor, and RunsFor on every backend. Upsert shapes repeat with ON CONFLICT DO UPDATE. List ordering repeats on created timestamps plus ids. The in-memory filters at backend/internal/store/memory.go:107, :172, and :361 must mirror the SQL WHERE clauses exactly. Extract the shared shapes into one helper or one stated pattern. Pin memory parity with a test. You keep dialect SQL handwritten. You state shared shapes once.

The SQLite profile repeats across a boundary. contracts/dialects/sqlite.json is the source. backend/internal/dialect/sqlite.json mirrors it byte for byte. The tool forces this duplication. go:embed cannot reach outside the package at backend/internal/dialect/sqlite.go:20. The test fails on drift at backend/internal/dialect/dialect_test.go:20. Keep the test. Copy the contract over the mirror, never edit one alone.

## 6. YAGNI

Every exported name has a caller. You scan all non-test files for exported functions and types. You search every file including tests for callers. Zero exported functions lack a caller. Zero exported types lack a caller. Two exported methods look dead but are not. LastInsertId and RowsAffected at backend/internal/engine/sqlite_raw.go:599 live on sqliteRawResult. database/sql calls them through driver.Result. The scan counts 335 exported functions and 140 exported types on this tree. The dead-code axis is clean. You save nothing by deleting exports.

The scan cannot see two knobs. Name each one here.

- ProvidedRunner at backend/internal/ai/api.go:467 ships in non-test code. Only tests construct it, at backend/internal/ai/helpers_test.go:348 and backend/internal/ai/jobs_test.go:81. Production wires envdb Manager at backend/cmd/api/main.go:271. Move ProvidedRunner into test files. Production keeps one Runner.
- Open on the raw driver at backend/internal/engine/sqlite_raw.go:129 always returns an error. The comment marks it unreachable through SQLiteExecutor. The connector opens connections directly. database/sql demands the method on driver.Driver. Neighboring Begin at :369 and Prepare at :377 refuse the same way. Keep the refusals. Mark them as interface ballast so no reader mistakes them for live paths.

## 7. What you could not determine

Three questions need production data. You record them here.

- Traffic shape is unknown. No production scrape was available. Semaphore size, pool ceilings, and job bounds rest on lab numbers. You set them from a week of metrics.
- No measurement covers provider cost or latency. The gateway ships with no key by design. The retry posture stays conservative until you measure a hosted model.
- Filesystem behavior beyond local disk is unknown. Rename atomicity and lock semantics on other mounts were not probed. The embedded PostgreSQL assumes local disk.
