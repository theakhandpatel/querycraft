# engine — slop audit (wave 1)

## Verdict

The execution layer is mostly earned: one `Execute` path supplies the row cap, byte cap, deadline and truncation flag to both dialects, `classify` owns one SQLSTATE table that `PgxExecutor.ClassifyError` also returns, and `sqlite_raw.go` reads each column by storage class, which is the measured reason the file exists. The real slop is concentrated in `sqlite.go`: `sqliteDSN` is a dead builder no production path calls, and the modernc error-framing layer (`sqliteUnframe`, `sqliteBusyMarker`, the `*sqlite.Error` branch, `sqliteError.Unwrap`) exists because "a driver or a wasm build" might hand one over, while the raw read driver already emits the engine's own sentence. Three accessors and one config knob are test-only: `PgxExecutor.Writable`, `SQLiteExecutor.ReadOnly`, `sqliteError.Code`, and `SQLiteConfig.ConnMaxLifetime`. The argument-binding path in `sqlite_raw.go` has no live caller either. The one cross-file two-truths is the error-code enum, declared in both `engine.go` and `dialect`. `execute.go`, `redact.go`, `normalize.go`, the limits algebra and the raw driver's read path are earned.

## Delete

`sqliteDSN` — backend/internal/engine/sqlite.go:176-193. Why it exists: it built the modernc DSN for the deleted `SQLiteConfig.DSN`/`parseSQLiteDSN` branch. Evidence it has no live consumer: `grep -rn sqliteDSN --include='*.go' backend | grep -v backend/internal/engine/` returns nothing; inside the package the only callers are `sqlite_test.go:39,651,1134-1160`. `sqliteConnectorFor` never calls it; it puts pragmas in `connector.pragmas` and `openSQLiteRawConn` applies them as statements. Remedy: delete the function and the three tests that call it. The old review (2026-10-17/03, Finding 4) said this builder "ships"; after the DSN branch landed, it does not.

The modernc error-framing cluster: `sqliteUnframe` (sqlite.go:342-353), `sqliteBusyMarker` (sqlite.go:314), the `*sqlite.Error` branch of `sqliteErrorOf` (sqlite.go:415-423), and `sqliteError.driver` plus `Unwrap` (sqlite.go:371-372,381-382). Why it exists: to strip modernc's `"<errstr>: <msg> (<rc>)"` rendering before the dialect profile sees it. Evidence it has no live consumer: the pooled connection is always `sqliteRawConn` (sqlite_raw.go:156-171,228), whose errors come from `engineError` as `*sqliteError` with the engine's sentence (sqlite_raw.go:652-657) or as `ctx.Err()`; `*sqlite.Error` is produced only by modernc's driver, and `grep -rn 'modernc' backend/internal/engine/*.go` shows the import used only at sqlite.go:415. The only other modernc user, the seed at envdb/sqlite.go:426, never calls `engine.ClassifyError`. The consumers are tests: sqlite_test.go:582-628 and :940 call `sqliteUnframe` by hand, and :651-673 opens a modernc connection to pin frames. Remedy: delete all four; reduce `sqliteErrorOf` to `return err` (or inline it at sqlite.go:436,577,590).

`PgxExecutor.Writable` — backend/internal/engine/pgx.go:98-101. Why it exists: the doc says a caller describing the environment can ask without assuming. Evidence: `grep -rn 'Writable()' --include='*.go' backend | grep -v backend/internal/engine/` returns only `envdb/plan.Writable` and `env.Writable`, different types; the engine callers are `pgx_writable_test.go:49,61`. Remedy: delete the method. Keep `PgxConfig.Writable` and the `writable` field, both read by `Query` (pgx.go:110,144).

`SQLiteExecutor.ReadOnly` and the `readOnly` field — backend/internal/engine/sqlite.go:256-259 and :204,253. Why it exists: symmetry with `PgxExecutor.Writable`. Evidence: the only reader is `sqlite_test.go:1125`; `grep -rn 'ReadOnly()' backend | grep -v /engine/` returns nothing. Remedy: delete the method and the field. `config.ReadOnly` stays, because the connector reads it (sqlite_raw.go:162,164).

`SQLiteConfig.ConnMaxLifetime` — backend/internal/engine/sqlite.go:145 and the apply at :235-237. Why it exists: a pool knob beside the two that are used. Evidence: `grep -rn ConnMaxLifetime --include='*.go' backend` returns only these lines; `envdb/sqlite.go:193-199,310-315` sets Path, ReadOnly, Pragmas, MaxOpenConns and MaxIdleConns only. Remedy: delete the field and the `SetConnMaxLifetime` block.

`sqliteRawConn.ExecContext` and `sqliteRawResult` (with `LastInsertId`, `RowsAffected`) — backend/internal/engine/sqlite_raw.go:519-541. Why they exist: so database/sql could `Exec` a write. Evidence: `SQLiteExecutor` reaches the connector only through `QueryContext` (sqlite.go:434) and `PingContext`; the `.ExecContext(` hits in engine tests (`sqlite_test.go:774,777`) are on a modernc `*sql.Conn` opened at :763, not this driver. Remedy: delete both. database/sql falls back to `Prepare`, which already refuses by design (sqlite_raw.go:318).

The argument-binding path: `bindText` (sqlite_raw.go:610-617), `bindBlob` (:620-631), and the per-argument switch in `bind` (:576-604). Why it exists: `Executor.Query` takes `args ...any`. Evidence no live caller supplies them to a SQLite executor: `grep -rn '\.Query(ctx, [^)]*,' --include='*.go' backend | grep -v /engine/` returns only pgx/store/ai call sites; every `ex.Query` call in `sqlite_test.go` passes one argument; `bind` returns at :569-571 for the empty slice it always receives. Remedy: remove the binding path and let `stepScript` ignore arguments. Keep the variadic signature on the interface, which pgx uses (perf/explain.go:552, envdb/manager.go:726).

`sqliteError.Code` and the `code` field — backend/internal/engine/sqlite.go:364-379, with assignments at :420-421 and sqlite_raw.go:657. Why it exists: its own comment says "for the record and for tests". Evidence: no production read; `grep -rn '\.Code()' backend/internal/engine/*.go` returns the method body and the three test reads at sqlite_test.go:496,549,790. Classification is by sentence only (`ClassifyError`, sqlite.go:297-303). Remedy: delete the method and the field; `engineError` drops the `sqlite3_extended_errcode` call.

`ErrPoolTimeout`'s export — backend/internal/engine/engine.go:59. Why it is exported: a sentinel by convention. Evidence it has no importer: `grep -rn ErrPoolTimeout --include='*.go' backend | grep -v backend/internal/engine/` returns nothing. Remedy: rename to `errPoolTimeout`. The mechanism stays live inside the package (`connectErr` pgx.go:168, `classify` execute.go:210).

## Collapse

The error-code enum is written twice. engine.go:32-45 declares `CodeEmptyQuery` ... `CodeEvaluationError`; backend/internal/dialect/dialect.go:28-43 declares the same eight strings under `CodeSyntaxError`/`CodeRuntimeError`/`CodeResultLimitExceeded` plus `CodePersistenceFailed`. engine already imports dialect (sqlite.go:100). The contract `contracts/query-result.schema.json` is the third copy. Remedy: one Go declaration, aliased by engine; consumers do not change.

The pragma mechanism is described two ways. sqlite.go:29-41 says "Pragmas travel in the DSN, per connection", and `SQLiteConfig.Pragmas` (sqlite.go:133-137) says "applied to every connection through the DSN". The code applies them as statements: `applyPragmas` runs `PRAGMA <p>` on the raw handle (sqlite_raw.go:239-278). The DSN builder that matched the doc is the dead `sqliteDSN`. Remedy: describe the statement mechanism once and delete the DSN wording.

The seed's package is named wrong in three production comments. sqlite.go:27, sqlite.go:102-103 and sqlite_raw.go:121 attribute the one-time multi-statement seed to `internal/httpapi`. It is `envdb/sqlite.go:426`. Remedy: correct the package name.

The writable-path scope note is stale. sqlite.go:69-76 marks the writable per-account environment and its commit/rollback rule as future work ("M6 step 5's"). `envdb/sqlite.go:306-315` opens a writable per-account `SQLiteExecutor` today, and `SQLiteExecutor` still never issues `BEGIN`. Remedy: state that the writable path exists and runs under autocommit.

Two doc comments describe a smaller engine than the code. engine.go:4-5 says "PgxExecutor is the real ... implementation" with no mention of `SQLiteExecutor`; engine.go:61 says `Column.Type` is "the PostgreSQL type name" while SQLite reports the declared type (sqlite.go:524,539-547). Remedy: one sentence each.

## Move / split

None warranted. `sqlite.go` has clear section comments and the deletes above remove roughly 130 lines from it; splitting the remainder would be churn. `sqlite_raw.go` is one coherent driver. `engine.go` mixes codes, limits and result constructors, but those are the package's small vocabulary and move cleanly only as part of the enum collapse above.

## Earned (checked, keep)

`Execute` — execute.go:22; callers eval/eval.go:177, perf/explain.go:182, ai/pipeline.go:646, httpapi/handlers.go:149. One cap/truncation path for both dialects.
`classify`, `pgCodeFor`, `engineWordsFor` — execute.go:206,262,289; `pgCodeFor` serves both `PgxExecutor.ClassifyError` (pgx.go:295) and `classify` (execute.go:227), pinned against the browser table by classify_test.go:69.
`ClassifyFor` and `IsBlankSQL` — execute.go:244,136; exported consumers perf/explain.go:122,244.
`RedactSecrets` — redact.go:21; used across ai/pipeline.go, ai/review.go, httpapi/logging_test.go:142 and more.
`Limits.Merge`, `Limits.Fill`, `Limits.Timeout` — engine.go:111,130,144; Merge at httpapi/server.go:254, Fill at eval/eval.go:175 and perf/explain.go:103, Timeout at httpapi/handlers.go:125. The ceiling-versus-floor split is documented and both are live.
`WithRequestID`, `RequestIDFromContext` — engine.go:225,230; httpapi/server.go:235 and eight other call sites.
`Failing`, `RefusingDialect`, `NewError`, `RoundMS`, `DefaultLimits` — engine.go:259,278,243,253,103; each has non-engine consumers (plan.go:77, dialect.go:121, metrics.go:296).
The `Executor` interface — engine.go:182; every method has a caller: Query/Version/Stats/Ready/Dialect/Diagnostics/Close in httpapi (handlers.go:20,30; metrics.go:213,221,243,271), Ping in cmd/api/main.go:405, EngineLabel and ClassifyError inside engine (execute.go:49,252).
`UnavailableExecutor` — unavailable.go:11; live in cmd/api/main.go:398-410 and httpapi/plan.go:211.
`PgxExecutor` internals: `pgResultFormats` (pgx.go:150) and `textOIDs` (normalize.go:105) are the parity mechanism, pinned by encoding_test.go:29; `sessionSettings` (pgx.go:173) supplies the statement timeout.
`normalizeValue` — normalize.go:20; TestNormalizeValue (engine_test.go:330) covers nil, int, float, bool, string, []byte, time.Time, pgtype.Numeric and [16]byte.
`PoolStats` — engine.go:167; every field is read by metrics.go:214-221.
`SQLiteReadOnlyPragmas`, `SQLiteDriverName` — sqlite.go:163,113; consumed by envdb/sqlite.go:196,312,459,426.
`sqlite_raw.go` read path — steps, values, `columnText`, `columnBlob`, `interruptOnDone`, `Close` all run on the live read path (sqlite.go:433-439). `sqliteRawDriver.Open` (:128), `sqliteRawConn.Begin` (:310) and `Prepare` (:318) are interface-required refusals.

## UNCERTAIN

`normalizeValue`'s `float64Valuer` branch (normalize.go:45-49): numeric OID 1700 is intercepted as text by `normalizeColumn` (normalize.go:125), and `Float8Codec`/`Float4Codec.DecodeValue` return native `float64`/`float32` (pgx v5.11.0 pgtype/float8.go:361). Only TestNormalizeValue (engine_test.go:350) drives it. Needs runtime proof that no non-text OID decodes to a `float64Valuer`.

`normalizeValue`'s `driver.Valuer` branch (normalize.go:50-61): likely reachable for a geometric column (`PointCodec.DecodeValue` returns `Point`, which implements `driver.Value`), but no test covers it. Needs one query to confirm.

The framing-cluster delete: if the owner wants insurance against a future driver swap, the current code keeps `sqliteUnframe` for that reason (sqlite.go:386-404, sqlite_test.go:905-913). Repo law says delete it; the owner may disagree.

Removing the SQLite argument-binding path (Delete item 7) has a behavioural edge if a future caller passes arguments, because unbound parameters become NULL. Flag for the owner.
