# SQL Workbook: Technical Design

Version 0.3. Supersedes the v0.2 docx. Lives in the repo at `docs/technical-design.md`.

## Premise

**The problem.** Most SQL resources are either reading material with static examples, or quiz sites that match your answer against a string or a canned output. Neither trains the real skill: write a query, see actual output, get it wrong, and work out why.

**The idea.** A course where every example runs on a real PostgreSQL engine, and every exercise is graded by running the learner's query and comparing its result against an explicit contract. Lessons alternate a short explanation with an exercise, so practice happens while the idea is fresh. The end product is a course builder: the learner asks AI for lessons, datasets and exercises, and the system validates them by running them before they are used.

**Who it is for.** Developers who already program and want to get solid at SQL beyond basic SELECT. The learner is also the course author: they build their own course with AI as a helper, so there is no maintainer who ships fixes. Everything that judges answers or edits exercises is designed around that.

**Why it is built this way.**
- Real engine: results are trustworthy and error messages are authentic.
- Result-based grading: any correct query passes, not just the one the author wrote.
- Browser-first: nothing to install, so the course exists early and can be shared as a link.
- Server gateway later: this is where the backend engineering is (query isolation, limits, reproducible environments), and it unlocks performance lessons and AI-generated challenges.

**What success looks like.** A learner works through the course in order, writes their own queries, gets specific feedback, and can come back later to find their attempts, notes and progress intact.

**What it is not.** Not a SaaS product, not a chatbot, not a general database client.


## 0. Status and how to use this document

This is a working spec. Each section is either **Decided** or **Open**. Open items are collected in section 15 and must be settled before the milestone that needs them.

Changes from v0.2:

| Change | Reason |
| --- | --- |
| Browser-first (PGlite in a Web Worker, IndexedDB) before any Go service | The curriculum is the product. A learner needs lessons and a runner first, not a server. |
| Go gateway moved ahead of AI | AI keys must stay out of browser-accessible state, and the gateway is the main backend engineering work. |
| Nine stages replaced by five milestones | Fewer infrastructure-only phases. Every milestone ends in something usable. |
| Contracts defined as JSON Schema, not Go or JS interfaces | Two execution adapters and two evaluator implementations must agree on one source of truth. |
| Seven-state challenge lifecycle deferred | Fixed exercises do not need it. It arrives with AI-generated challenges. |
| SQLite metadata added as an opt-in backend | IndexedDB in browser mode; a separate Postgres database in server mode (the default). The `metadata` key set to `sqlite` in `querycraft.json` stores app metadata in one local file instead, with PostgreSQL still the learner's execution engine. |
| Premise and versioning policy added (Premise, 7.3) | v0.2 assumed the reader knew why, and applied versioning everywhere before it was needed. |
| Prototype findings added (Appendix A) | Measured behavior of PGlite replaces assumptions. |
| Inline editors inside lessons, interleaved with explanation | Settled after prototype review. Replaces the separate workspace panel. |
| Learner is the course author. On-demand review flow added (6.4, 6.5) | No maintainer ships fixes to learner-built courses, so the product must handle a bad exercise itself. |

Conventions: "must" is a requirement, "should" is a default that can be overridden with a reason. Numeric limits are starting values to tune by measurement.

## 1. Product and goals

### 1.1 Outcome

A learner works through SQL topics in order. Each lesson alternates short explanation with an exercise the learner solves by writing SQL against a real PostgreSQL-compatible engine. Results and errors come from the engine. Exercises are graded by comparing query results, not SQL text. Attempts, notes and progress persist. Later, an AI Challenge Lab generates targeted practice that passes the same validation as curated exercises.

### 1.2 Goals

- Real SQL execution. No simulated or hardcoded results.
- Progressive, interleaved teaching: explain, exercise, explain, exercise.
- Deterministic grading with an explicit result contract.
- Durable personal workbook: attempts, progress, notes, bookmarks, saved queries.
- Core learning works with AI disabled and with no server.
- A backend that is technically substantial on its own: a hardened SQL execution gateway, reproducible environments, measurable correctness (milestones 3 to 5).

### 1.3 Non-goals for the first four milestones

- Public multi-tenant hosting, accounts, collaboration, sharing.
- Multiple SQL dialects or dialect translation.
- Desktop packaging.
- Autonomous AI access to shell, files or network.
- Cloud sync.

### 1.4 Quality requirements

| Area | Requirement |
| --- | --- |
| Correctness | Results come from the configured engine. Evaluation is deterministic and covered by fixtures. |
| Durability | An acknowledged attempt or note survives a normal restart or reload. |
| Recoverability | Dataset reset never deletes attempts. Saved challenges are rebuilt from versioned artifacts. |
| Honesty | Limits the system cannot enforce on an adapter are reported as such (section 5.3), not implied. |
| Portability | Curriculum, evaluator and workbook do not depend on a specific engine adapter. |
| Usability | A learner can read, run, inspect schema, submit and continue without leaving the lesson. |

## 2. Decisions

| # | Decision | Status | Notes |
| --- | --- | --- | --- |
| D1 | Client is React with JavaScript, no TypeScript | Decided | Contract validation via JSON Schema at the boundary replaces type checking. |
| D2 | M1 to M2 run entirely in the browser: PGlite in a Web Worker | Decided | PostgreSQL syntax throughout. |
| D3 | Browser metadata lives in IndexedDB | Decided | Plus a versioned JSON export (section 8.3). |
| D4 | Contracts are JSON Schema in `contracts/` | Decided | Source of truth for both adapters, both evaluators and the API. |
| D5 | Go service with native PostgreSQL arrives in M3 | Decided | Second `ExecutionService` adapter, not a rewrite. |
| D6 | Server-mode metadata uses a separate Postgres database and role | Decided | PostgreSQL by default; an opt-in SQLite file backend (the `metadata` key set to `sqlite`) stores app metadata locally. Learner SQL still runs on PostgreSQL. |
| D7 | AI work starts only after M3 | Decided | Keys are held server-side. |
| D8 | Lesson authoring format is Markdown with front matter, compiled to JSON | Decided | Section 9. |
| D9 | Inline editors per example and exercise, plus a schema drawer | Decided | Section 10. |
| D10 | Where do attempts live once the Go service exists? | Open | Section 15. |
| D11 | One database per challenge versus schema per challenge | Open | Decide in the M3 spike. |
| D12 | Grading is a deterministic check first, then on-demand AI review, and the learner decides | Decided | Sections 6.4 and 6.5. AI never computes results or sets pass and fail. |

## 3. Architecture

```
React client (JavaScript)
  lessons, inline SQL cells, results grid, schema drawer, workbook
        |
        |  ExecutionService + Evaluator contracts (JSON Schema, section 4)
        v
  +-----------------------------+      +----------------------------------+
  | Adapter A (M1 to M2)        |      | Adapter B (M3 onward)            |
  | PGlite in a Web Worker      |      | Go service + native PostgreSQL   |
  | IndexedDB metadata          |      | Postgres metadata DB (separate)  |
  +-----------------------------+      +----------------------------------+
```

The client talks to an `ExecutionService` facade. Which adapter is behind it is a configuration choice. Curriculum, evaluator and workbook code must not import anything adapter-specific.

### 3.1 Components

| Component | Owns | Must not |
| --- | --- | --- |
| Course catalog | Versioned lessons, exercises, dataset manifests | Store learner history |
| Execution facade | Run, inspect schema, reset, capabilities | Expose connection details to the UI |
| Evaluator | Result contract comparison, feedback codes | Depend on SQL text or formatting |
| Workbook store | Attempts, progress, notes, bookmarks, saved queries | Execute SQL |
| Adapter A | Worker lifecycle, per-query transaction, kill and reseed | Claim isolation it does not provide |
| Adapter B | Roles, limits, environments, streaming, cancellation | Run learner SQL on an administrative connection |
| AI service (M4) | Provider calls, artifact validation pipeline | Bypass evaluator, limits or persistence |

## 4. Contracts

Schemas live in `contracts/*.schema.json`. Both JavaScript and Go validate against them in tests. Shapes below are illustrative; the schema files govern.

### 4.1 ExecutionService operations

| Operation | Input | Output |
| --- | --- | --- |
| `capabilities()` | none | `{ hard_timeout, explain, max_concurrent, isolation }` |
| `createEnvironment(spec)` | dataset id, schema and seed versions | environment ref |
| `execute(ref, sql, limits)` | SQL text, limits | `QueryResult` |
| `inspectSchema(ref)` | environment ref | `SchemaInfo` |
| `reset(ref)` | environment ref | void |
| `destroy(ref)` | environment ref | void |
| `health()` | none | status |

### 4.2 QueryResult

```json
{
  "status": "succeeded",
  "columns": [{ "name": "status", "type": "text" }, { "name": "order_count", "type": "int8" }],
  "rows": [["delivered", 36], ["pending", 13]],
  "row_count": 2,
  "truncated": false,
  "duration_ms": 8,
  "error": null
}
```

Errors use stable codes, never free text alone:

| Code | Meaning |
| --- | --- |
| `EMPTY_QUERY` | Blank SQL |
| `QUERY_SYNTAX_ERROR` | Engine rejected the statement. Message and position included when available. |
| `QUERY_RUNTIME_ERROR` | Engine error after parsing (type, constraint, missing relation) |
| `QUERY_TIMEOUT` | Deadline exceeded and the query was stopped |
| `RESULT_LIMIT_EXCEEDED` | Rows or bytes capped. `truncated` is set. |
| `ENVIRONMENT_UNAVAILABLE` | Engine starting, restarting or down |
| `PERSISTENCE_FAILED` | Attempt could not be saved. Never reported as success. |
| `EVALUATION_ERROR` | Evaluator failed. Distinct from a wrong answer. |

### 4.3 ExerciseSpec and ResultContract

```json
{
  "id": "where.2",
  "version": 1,
  "lesson_id": "where",
  "type": "solve",
  "prompt": "Return product_name, category and price of Electronics or Home products under 40.",
  "setup": { "dataset": "ecommerce", "schema_version": 1, "seed_version": 1 },
  "result_contract": {
    "columns": ["product_name", "category", "price"],
    "column_order": "exact",
    "row_order": "ignored",
    "duplicates": "preserved",
    "numeric_tolerance": 0
  },
  "reference_solution": "solutions/where.2.sql",
  "hints": ["hints/where.2.1.md", "hints/where.2.2.md"],
  "difficulty": "beginner"
}
```

Exercise `type` is one of `complete`, `write`, `modify`, `predict`, `fix`, `combine`, `solve`.

### 4.4 Attempt and Evaluation

An attempt is immutable: `id`, `exercise_id` or `challenge_id`, `sql`, `execution_status`, bounded result preview, `duration_ms`, `created_at`, `client_request_id`, `exercise_version`, `dataset_version`. An evaluation is a separate record: `attempt_id`, `status` (`passed`, `failed`, `error`), `feedback_codes`, and `evaluator_version` once two evaluators exist (M3). Editing and rerunning creates a new attempt.

## 5. Execution design

### 5.1 Common rules

- One statement per submission in M1 to M2. Multi-statement is a later decision with explicit transaction semantics.
- Every learner query runs inside a transaction that is rolled back, so the shared dataset cannot be altered by ordinary experimentation.
- Result caps apply before data crosses a boundary: rows, serialized bytes, and a per-value size cap for long text.
- The editor text must survive any failure.
- Run and Submit are separate actions. Run shows results. Submit invokes the evaluator.

### 5.2 Adapter A: PGlite in a Web Worker

- The engine and dataset live in a dedicated Worker so the UI thread stays responsive.
- Startup seeds the dataset from the versioned seed artifact. Seeding must be deterministic and fast enough to repeat.
- Deadline enforcement is done by the host, not the engine: if a query exceeds the limit (5 s initially), terminate the Worker, start a new one, reseed, and return `QUERY_TIMEOUT`. Prototype testing showed `statement_timeout` has no effect in this engine (Appendix A).
- Reset is a reseed.
- If Worker creation is blocked by the host page, fall back to the main thread and surface that in the UI and in `capabilities()` (`hard_timeout: false`).
- Version pinned. Behavior must be re-measured when the PGlite version changes.

### 5.3 What each adapter does and does not protect

| Concern | Adapter A (browser) | Adapter B (native Postgres) |
| --- | --- | --- |
| Learner damages shared data | Rolled-back transaction, reseed on reset | Rolled-back or read-only transaction, restricted role, template rebuild |
| Runaway CPU | Host kills the Worker after the deadline | `statement_timeout`, cancellation, process-level limits |
| Memory exhaustion | Not controlled. The tab can crash. | `work_mem`, `temp_file_limit`, streaming with caps |
| Learner reads other users' data | Not applicable. Single user, local data only. | Separate databases and roles |
| Privileged operations | Not applicable. WebAssembly sandbox only. | Role without superuser, no extension install, no server file access, no `COPY ... PROGRAM` |
| Untrusted remote users | Not a goal | Needs container-level isolation (open decision) |

A Worker is a responsiveness tool, not a security boundary.

### 5.4 Adapter B: Go and native PostgreSQL

- Learner SQL runs on a dedicated restricted role. The administrative connection is used only to create and drop environments.
- Defaults to a read-only transaction for curated exercises. Writing exercises opt in explicitly.
- Session settings per query: `statement_timeout`, `lock_timeout`, `idle_in_transaction_session_timeout`, `temp_file_limit`, `work_mem`.
- Results stream with pgx. Stop reading at the row and byte caps and cancel the query.
- Context cancellation maps to server-side cancel when the client disconnects.
- Global and per-environment concurrency limits through a semaphore. Bounded connection pools per role.
- Environments are created from a template database for speed. D11 decides between a database or a schema per challenge.
- `EXPLAIN ANALYZE` is not exposed until the performance milestone, because it executes the statement.
- A string blocklist of SQL keywords is never relied on. Enforcement is by database permissions.

## 6. Evaluation

### 6.1 Comparison rules

1. Execution errors are execution failures, not wrong answers.
2. Column names are checked against the contract. Column order matters only when the contract says so.
3. Row order is ignored unless the contract requires an order.
4. Duplicates are preserved unless the contract says otherwise. Never compare as sets by accident.
5. NULL is distinct from empty string and from zero.
6. Numeric comparison uses the contract's tolerance.
7. No implicit text normalization.
8. An empty result is a valid result and is compared like any other.
9. If the evaluator itself fails, return `EVALUATION_ERROR`, not a learner failure.

### 6.2 Feedback layers

Correctness first. If wrong, a specific cause: column mismatch, row count mismatch, value mismatch, wrong order. Then a hint on request. Then the reference solution on request, with an explanation. Hint and solution views are recorded as events.

### 6.3 Two implementations, one fixture suite

The evaluator exists in JavaScript (M1) and Go (M3). Both must pass the same language-neutral fixture suite in `contracts/fixtures/`: JSON files of `{ contract, actual_result, expected_outcome }`. A new rule is added as fixtures first.

### 6.4 Statuses, triage and overrides

Because the learner owns the course, a failed check can mean the learner is wrong or the exercise is wrong. The evaluator reports facts. It does not guess which.

| Status | Meaning |
| --- | --- |
| `passed` | The result satisfies the contract on the current exercise version |
| `failed` | The result does not satisfy the contract. Feedback codes say how. |
| `error` | The query or the evaluator failed. Not a wrong answer. |
| `accepted_by_user` | The learner overrode a failure. Recorded separately and never counted as `passed`. |

Two actions follow a failure, and they are separate because the intent differs:
- **Ask the tutor.** A conversation over the prompt, the dataset schema, the learner's query and the gateway's own run of it. Optional. It never changes status. The tutor may see the expected result after the learner has attempted the exercise, but never the reference query (ADR 0008).
- **Report a problem with this exercise.** Starts the review flow in 6.5.

**Accept my answer** needs no AI and is always available, so a wrong "failed" cannot block progress.

After several failed attempts the UI may show a nudge, "Think the exercise is wrong? Report it." It never starts the review on its own.

### 6.5 On-demand critique, verification and repair

The review flow runs only when the learner reports a problem. Most failures are learner mistakes, so running it on every mismatch would waste cost and add noise. Free deterministic checks (the reference runs, satisfies its own contract, returns a non-empty result, dataset invariants hold) run automatically when an exercise is created or edited, with no model involved.

Three roles, each in a separate context:

1. **Critic.** Produces a falsifiable claim, not prose: a type (`reference_wrong`, `contract_too_strict`, `prompt_ambiguous`, `dataset_defect`, `learner_error`), the evidence, and the SQL probes that would confirm it.
2. **Verifier.** Sees only the claim and the exercise files, not the critic's reasoning. Runs the probes on the engine through a read-only role and accepts or rejects the claim on execution evidence. A different prompt or model is preferred.
3. **Fixer.** Runs only for a confirmed defect. Proposes a patch to the exercise bundle (reference, contract, prompt or a seed row). Deterministic code applies it in a sandbox and reruns the exercise's fixtures plus every exercise on the same dataset. A patch that fails any check is discarded.

The learner sees the diff and chooses **Apply fix** or **Dismiss**. Applying bumps the exercise `version` and adds a regression check. Earlier attempts stay recorded against the version they were graded on.

Rules:
- Agents never compute results. The engine does. A claim that cannot be expressed as a probe is reported as `prompt_ambiguous` for the learner to decide.
- Agent tools are limited to run-probe, inspect-schema, read-bundle and propose-patch. They cannot write.
- SQL, comments and data values are untrusted input.
- Rounds and tokens are capped. A verified or rejected claim is cached by exercise version plus claim, and a repeat report returns the stored verdict.
- Reports per exercise per day are capped.
- Execution evidence reduces shared blind spots but cannot remove them, so applying a fix always requires the learner's approval.
- Execution cannot settle teaching quality. That stays a human judgment.

## 7. Dataset and exercises

### 7.1 E-commerce dataset

Tables: `customers`, `products`, `orders`, `order_items`, with primary and foreign keys. Row counts: about 30 customers, 20 products, 80 orders, about 160 order items. The seed is deterministic and generated by SQL, so it can be rebuilt identically. Checksum verification arrives in M3, when a second engine must prove it built the same data.

Required edge cases, verified by a smoke test:

- Customers with no orders. Products never ordered.
- Orders with several items. Customers with several orders.
- Repeated cities and categories.
- NULL emails and NULL cities.
- A product with zero stock.
- Several order statuses across several months.

Schema and seed share one `dataset_version`. See 7.3 for what is versioned and when.

**Physical layout.** In server mode the dataset lives in a PostgreSQL schema named
`dataset` (`dataset.customers`, `dataset.orders`, …), while gateway metadata
(`progress`, `events`) lives in a schema named `app`. Each environment is a
separate database on one server. Every learner statement runs with
`search_path = dataset, public`, so unqualified table names resolve to the
dataset and `app.*` stays out of reach. PGlite mirrors the same split in the
browser. The full layout, cluster → database → schema, why the dataset is
isolated (a restricted role can be granted SELECT on `dataset` only), and how to
point the gateway at a container: is in
[database-layout.md](database-layout.md).

### 7.2 Exercise rules

- Every reference solution runs and passes its own contract in CI.
- Where several formulations are valid, at least one alternative is a fixture that must pass.
- Common wrong queries are fixtures that must fail, with the expected feedback code.
- Answers must not depend on insertion order or on ties. Exercises that sort must specify a tie-break.

### 7.3 Versioning policy

**Why version at all.** Two things must not silently change under a learner:
1. Saved history. An attempt should still make sense after you fix a prompt typo, change a seed row or tighten a contract.
2. Grading. An answer that passed last month should not flip to failed because the dataset changed underneath it.

**What we do in M1 (the minimum).**
- `dataset_version`: an integer, bumped on any change to the schema or seed.
- Exercise `version`: an integer, bumped on any change to the prompt, contract or reference solution.
- Every attempt stores both. If either differs from current, the UI shows "solved on an earlier version" instead of discarding progress.

**What we defer, and why.**

| Deferred | Until | Because |
| --- | --- | --- |
| Seed checksums and dataset manifests | M3 | Needed only to prove a second engine built identical data |
| `evaluator_version` | M3 | There is one evaluator until the Go port exists |
| Full artifact bundles with replay metadata | M4 | Only generated challenges must be rebuilt from stored artifacts |

Rule of thumb: version a thing when something stored refers to it.

In a learner-authored course, the main source of exercise version bumps is an applied fix from the review flow (6.5). That is why old attempts must keep the version they were graded on.

## 8. Persistence

### 8.1 Browser mode (IndexedDB)

| Store | Contents |
| --- | --- |
| `attempts` | Immutable attempts and bounded result previews |
| `evaluations` | Evaluation records |
| `progress` | Per exercise and lesson state |
| `notes`, `bookmarks`, `saved_queries` | Workbook items |
| `drafts` | Editor text per cell |
| `meta` | Content versions, export version |

Writes use transactions. If a write fails, the UI says so and keeps the draft. Browsers can evict storage, so export is a first-class feature, not an afterthought.

### 8.2 Server mode (M3 onward)

A separate Postgres database holds the same logical entities: profile, challenge, environment_manifest, attempt, attempt_result, evaluation, hint_event, note, bookmark, saved_query, audit_event. The learner role has no access to it. The idempotency key is the client request id, unique per profile, so a retried request does not create a duplicate attempt.

### 8.3 Export and import

A single JSON document with a `format_version`, content versions, and all workbook entities. It never contains credentials. Import validates against the schema before writing and refuses unknown future versions. The export format is also the migration path from browser mode to server mode (D10).

## 9. Course content

### 9.1 Lesson format

One Markdown file per lesson. Front matter holds `id`, `title`, `summary`, `objectives`, `prerequisites`. The body is a sequence of blocks:

- Prose.
- ` ```sql syntax ` for a non-runnable syntax block.
- ` ```sql example ` for a runnable example, followed by a one or two line note.
- ` ```exercise ` for an exercise, which references an `ExerciseSpec` by id.

A build step compiles lessons to JSON and validates references. Content is data. No lesson text lives in application code.

### 9.2 Teaching pattern

Explain a small idea, show a runnable example, then an exercise. Repeat. A lesson reveals content in order and holds at each exercise until it is solved or skipped. Later exercises reuse earlier concepts. Each lesson ends with at least one exercise that combines the new idea with an earlier one.

### 9.3 Curriculum order

1. Tables, SELECT, DISTINCT, aliases.
2. WHERE, comparison, AND, OR, NOT, precedence.
3. ORDER BY, LIMIT, OFFSET.
4. NULL handling.
5. IN, BETWEEN, LIKE, CASE.
6. Aggregation: COUNT, SUM, AVG, MIN, MAX, GROUP BY, HAVING.
7. Joins: INNER, LEFT, multi-table, duplicates from joins.
8. Subqueries, EXISTS, UNION.
9. CTEs, recursive CTEs.
10. Window functions.
11. Advanced and debugging, then performance (M5).

A topic is published only when its prerequisites are published, its exercises pass the fixture suite, and it has at least one example.

## 10. User interface

- A lesson is a single reading column with inline editors. Each example and each exercise has its own editor, Run button and result area. Results and feedback stay with the editor that produced them.
- A **Tables** control is always reachable. It opens a drawer (not a modal) with tables, columns, types, keys and 5-row previews.
- Exercises hold the lesson at that point until solved or skipped.
- Distinct actions: Run, Check answer, Hint, Show solution, Reset.
- Result grid handles zero rows, NULL, long text, wide results and truncation.
- Editor keeps its content across errors, navigation and reload.
- Keyboard: run with Ctrl or Cmd plus Enter. Visible focus. Labels on every control.
- Design follows Impeccable's guidance on avoiding generic AI interface patterns: no side-stripe accents, no nested cards, no decorative gradients, restrained palette, readable line length, text at least 14 px, no repeated labels.
- Light and dark themes through tokens.

## 11. AI Challenge Lab (M4)

The model is an untrusted content generator. The Go service enforces policy.

### 11.1 Pipeline

1. Validate a structured request: topics, difficulty, scenario, constraints.
2. Request a candidate bundle under a constrained output schema: prompt, schema, seed plan, reference solution, result contract, hints, objectives.
3. Validate size limits, table and row caps, and allowed SQL constructs.
4. Create a throwaway environment and run setup. Check constraints and expected dataset properties.
5. Run the reference solution and confirm it satisfies its own contract.
6. Cross-check: request a second, independently produced solution and require the same result on the generated data. This catches some contract errors. It is not proof of correctness, because both solutions can share a mistake.
7. Run the evaluator against adversarial fixtures where feasible.
8. Repair within a small retry budget. Then fail clearly.
9. Save the artifacts and exact versions, then mark the challenge active.

Nothing becomes visible to the learner before step 9.

### 11.2 Rules

- AI is off until a provider is configured. Core features work without it.
- Provider keys are held by the Go service and never sent to or stored in the browser.
- The first verdict always comes from the deterministic evaluator. AI never computes results or sets pass and fail. Only the learner can override a failure (`accepted_by_user`) or approve a fix.
- Tutoring uses the learner query, the gateway's own run of it, the objective, the dataset schema, the result contract and, after an attempt, the expected result. It never receives or states the reference query (ADR 0008); the reference arms the no-solution guard only.
- Retries, tokens, output size and concurrency are bounded.
- Tool calls route through normal services: schema inspection, query execution, evaluation, reset, save draft. No shell or filesystem tools.
- The SDK is chosen after a spike against a provider-neutral interface, not by preference.

## 12. Security and privacy

| Threat | Control | Residual risk |
| --- | --- | --- |
| Runaway query | Host-enforced deadline (A), statement limits and cancellation (B) | Memory exhaustion in A |
| Learner reaches metadata | Separate database and role (B) | Misconfiguration |
| Privileged SQL | Restricted role, no superuser (B) | Engine vulnerabilities |
| Malicious generated setup | Validation, throwaway environment, restricted role, bounded retries | Resource abuse within limits |
| Local API reachable from LAN | Loopback binding by default | User override |
| Provider key leak | Server-side storage, redacted logs | Compromised host |
| Prompt injection through SQL comments or data | Treat all content as untrusted. Tools enforce policy independently. | Misleading explanations |
| Storage loss in browser | Export, clear warnings | User clears data without exporting |

Public hosting that accepts SQL from untrusted users is out of scope until per-query container isolation exists.

## 13. Observability and testing

### 13.1 Observability (Adapter B)

Structured logs with request id, operation, duration, status and error code. Metrics for query duration, timeouts, caps hit, pool usage, environment create and reset time, evaluator errors and persistence errors. No secrets or connection strings in logs. A diagnostics view for engine readiness, versions and migration state.

### 13.2 Test matrix

| Layer | Checks |
| --- | --- |
| Contract | Every fixture validates against its schema in JS and Go |
| Evaluator | Shared fixture suite passes in both languages |
| Exercise | Reference solutions pass. Valid alternatives pass. Known wrong queries fail with the right code. |
| Dataset | Deterministic seed, expected counts, edge-case smoke test |
| Adapter A | Worker timeout, kill and reseed, reset, large result cap, rollback isolation |
| Adapter B | Role permissions, timeouts, caps, cancellation, concurrency, reset |
| Adversarial suite (B) | See 13.3 |
| Persistence | Reload and restart keep data. Failed write is surfaced. Import and export round trip. |
| Review flow | Scripted fake provider: unsupported claims are rejected by the verifier, patches that fail fixtures are discarded, nothing applies without approval |
| End to end | Lesson, run, submit, reload, resume |
| Load (B) | Query endpoint under concurrent load with timeouts and pool metrics recorded |

### 13.3 Adversarial suite

Each case must end in a bounded, reported outcome and leave the service healthy:

- `pg_sleep` beyond the deadline.
- Very large `generate_series`.
- Infinite recursive CTE.
- Cross join explosion.
- `DO` block with a loop.
- Attempts to read the metadata database or other environments.
- `COPY ... PROGRAM`, server file access, `CREATE EXTENSION`, role creation.
- Very large single value in a result.
- Connection exhaustion by concurrent requests.
- Reset while a query is running.

The published results of this suite, including what is not protected, are part of the project documentation.

## 14. Milestones

Each milestone ends in something usable. Estimates are omitted until team size and hours are known.

### M1: Usable course (browser)

Scope: React shell, PGlite in a Worker, e-commerce dataset, inline SQL cells, results grid, Tables drawer, evaluator with result contracts, hints and solutions, an accept-my-answer override, IndexedDB attempts and progress, about 10 lessons with exercises, JSON export.

Exit gate:
- A learner can complete the first lessons and keep progress across reloads.
- Reference solutions pass, and wrong-query fixtures fail correctly.
- A runaway query is stopped by the host, with the dataset restored.
- Contracts exist as JSON Schema and the evaluator passes the fixture suite.

### M2: Complete workbook (browser)

Scope: full curriculum through window functions, notes, bookmarks, saved queries, search, progress views, import, accessibility pass.

Exit gate:
- Every published lesson has objectives, an example and an exercise or a reading-only reason.
- Export and import round trip without loss.
- Keyboard-only use of a full lesson works.

### M3: Execution gateway (Go and native PostgreSQL)

Status: **shipped**, tagged `v0.3.0`. Plan of record and the release checklist:
`docs/m3-implementation-plan.md`; live tracker: `progress.md`. The adapter-parity
result and its exclusions: `docs/adapter-parity.md`.

Scope: Adapter B behind the same contracts, restricted role, limits, streaming caps, cancellation, template environments, Postgres metadata database, Go evaluator, adversarial suite, load test, metrics, `docker compose up` setup.

Exit gate:
- The Go evaluator passes the shared fixture suite.
- The adversarial suite passes with bounded outcomes and a healthy service afterwards.
- No path runs learner SQL on an administrative connection.
- The same lesson content runs against both adapters unchanged.
- Setup works on a clean machine from the documented command.

### M4: AI Challenge Lab

Plan of record: [`docs/m4-implementation-plan.md`](m4-implementation-plan.md); live tracker: `progress.md`. The provider decision: ADR [`0005`](adr/0005-ai-provider-and-sdk.md).

Scope: provider interface and fake provider, challenge request and artifact schemas, generation pipeline (11.1), server-side keys, tutoring, on-demand critique, verification and repair (6.5), challenge lifecycle states added as needed.

Exit gate:
- An invalid generated setup never becomes active.
- Reference solution and cross-check pass before activation.
- Retries are bounded and failures are understandable.
- Generated challenges survive restart and can be reset.
- A reported defect is verified by execution before any patch is proposed, and no patch is applied without the learner's approval.
- Disabling AI leaves every other feature working.

### M5: SQL performance lab

Plan of record: [`docs/m5-implementation-plan.md`](m5-implementation-plan.md).

Scope: performance exercise type, `EXPLAIN` viewing, controlled `EXPLAIN ANALYZE`, larger controlled datasets, plan-based evaluation before timing, environment metadata in results.

Exit gate:
- Plans are reproducible on pinned fixtures.
- Results report engine version, scale and indexes, and do not overstate timing precision.

### M6: Second dialect, SQLite

Plan of record: [`docs/m6-implementation-plan.md`](m6-implementation-plan.md).

Scope: SQLite as a **second dialect**, alongside PostgreSQL, not a replacement for it
(ADR 0004 explains why the app is PostgreSQL-first). The learner can take a course that
teaches SQLite, run it on a real SQLite engine in both adapters, and be graded the same
way. This is the milestone that turns "SQL" from one engine into a dialect dimension.

Concretely:

- A **dialect** concept in the contracts: a course/chapter declares its dialect, and
  `QueryResult.engine` names the engine that produced a result.
- Adapter A: SQLite compiled to WebAssembly in a Worker, mirroring the PGlite adapter
  (same deadline kill, same row/byte caps, same honest capability reporting).
- Adapter B: SQLite behind the same `POST /query` contract, so the gateway serves both.
- A second dataset authored for SQLite (schema + seed), with a checked mapping to the
  commerce dataset so the two dialects answer the same questions about the same shape.
- **Dialect-aware grading**: SQLite has no SQLSTATE, dynamic typing, `LIKE` is
  case-insensitive for ASCII, and dates are not types. Error classification and value
  encoding must therefore be defined per dialect, not assumed from PostgreSQL.
- Courses that teach what is *different*: SQLite vs PostgreSQL types and affinity,
  `AUTOINCREMENT` vs `GENERATED`/`IDENTITY`, `PRAGMA` vs `SET`, `rowid`, when SQLite is
  the right choice, and what it cannot do (no roles, no `EXPLAIN ANALYZE`, no schemas).

Exit gate:
- A learner cannot accidentally run a SQLite chapter against PostgreSQL, or the reverse,
  without the UI saying which dialect they are on, and the refusal is actionable.
- Both dialects run the same *shape* of question (SELECT, JOIN, GROUP BY, window, CTE,
  recursive CTE) and pass the same result contracts.
- Error classification is dialect-specific and documented, with a test per code.
- The adapter-parity audit runs **once per dialect** (PostgreSQL: PGlite vs gateway;
  SQLite: wasm vs gateway), and both are green.
- The single binary stays cgo-free and Docker-free.

## 15. Open decisions

| Decision | Default | Settle by |
| --- | --- | --- |
| D10: Where attempts live once the Go service exists | Server owns metadata in server mode. Browser mode stays available. Export and import is the bridge. | Start of M3 |
| D11: Database or schema per challenge | Database from a template | M3 spike |
| Multi-statement submissions | One statement only | Before the first writing exercise |
| Writing exercises (INSERT, UPDATE, DDL) | Not in M1 to M2 | Before M3 limits are finalized |
| Public demo | Not hosted. `docker compose up` plus a recorded demo. | Before any hosted deployment, with container isolation |
| Challenge delete semantics | Delete challenge data after confirmation. Attempts exported first by default. | Before the delete UI |
| AI provider and SDK | Provider-neutral interface + OpenAI-compatible HTTP transport; deterministic fake provider default | M4 spike: settled by ADR `0005` |
| Desktop packaging | Not planned | After real install friction evidence |
| D12: SQLite engine in the browser | `@sqlite.org/sqlite-wasm` 3.53.4 in a Worker, declared-type labels from `sqlite3_column_decltype`, deadline from `sqlite3_progress_handler` | Settled: ADR `docs/adr/0007-browser-sqlite-engine.md` (the only candidate that can label a column, so the only one the parity audit can compare) |
| D13: SQLite engine on the gateway | `modernc.org/sqlite` (pure Go, no cgo) | Settled in M6 step 4: `backend/internal/engine/sqlite.go`, proved cgo-free by `make check-cgo` |
| D14: How SQLite isolates a learner's writes | Per-learner database file, reset by copy | Settled: ADR `docs/adr/0006-sqlite-learner-isolation.md` (measured: copy-create ≈2 ms, reset ≈2 ms) |
| D15: One dialect per chapter, or dialect-neutral chapters with per-dialect solutions | One dialect per chapter | Before the first SQLite course |

## 16. First backlog

| ID | Issue | Acceptance |
| --- | --- | --- |
| C-01 | Define JSON Schemas for QueryResult, ExerciseSpec, ResultContract, Attempt, Evaluation | Schemas validate sample files |
| C-02 | Evaluator fixture suite format and first 20 cases | Cases cover order, duplicates, NULL, empty, tolerance |
| E-01 | JavaScript evaluator | Passes all fixtures |
| R-01 | Accept-my-answer override and `accepted_by_user` status | Progress shows accepted answers separately from passed |
| D-01 | Dataset seed, `dataset_version` and smoke test | Edge cases verified, expected row counts match |
| A-01 | Adapter A Worker with deadline kill and reseed | Runaway query test passes |
| A-02 | Result caps before postMessage | Large result and wide value tests pass |
| U-01 | Inline SQL cell component and results grid | Zero rows, NULL, long text render |
| U-02 | Tables drawer | Matches actual schema |
| L-01 | Lesson compiler (Markdown to JSON) with reference validation | Broken reference fails the build |
| L-02 | First 5 lessons with exercises | Reference solutions pass in CI |
| P-01 | IndexedDB stores, drafts, JSON export | Reload keeps data, export validates |
| S-01 | Spike: Go, pgx, restricted role, template database, adversarial cases | Starts M3 and informs D11 |

## 17. Principles

1. Ship the learning loop before infrastructure.
2. One set of contracts. Adapters conform to it.
3. Report limits honestly instead of implying guarantees.
4. Attempts are history. Never delete them as a side effect of reset.
5. Deterministic evaluation decides correctness. AI explains and authors.
6. Version a thing when something stored refers to it, and not before.
7. Measure behavior of the engine before relying on it.
8. The learner owns the course. AI proposes, execution verifies, the learner approves.

## Appendix A: Prototype findings

From the PGlite prototype (version 0.2.17):

- `statement_timeout` is ignored. A 20-million-row `generate_series` ran about 10 seconds despite a 300 ms limit. Hard termination needs the host to kill the Worker.
- Rolling back a transaction after each learner query leaves the seed intact, including after a failed `DELETE`. Rollback works as the isolation mechanism for curated exercises.
- The seed and all prototype solutions ran correctly against the real engine in Node.
- In a hosted preview environment, creating a Worker from a Blob URL was blocked by the host. The engine's WebAssembly download from a CDN was unverified there. Both depend on the host page's security policy, which is why Adapter A needs a main-thread fallback and honest capability reporting.
- Each lesson's reference solutions and example queries returned non-empty results on the seed data, which should be kept as a fixture check.
