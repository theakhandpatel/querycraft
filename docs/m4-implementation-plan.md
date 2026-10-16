# M4 implementation plan: "AI Challenge Lab"

Design source: `docs/technical-design.md` §14 (M4), §11.1 (the generation
pipeline), §11.2 (rules), §6.5 (on-demand critique, verification and repair),
§12 (security and privacy), §13.2 (test matrix). The provider decision is settled
by ADR [`docs/adr/0005-ai-provider-and-sdk.md`](adr/0005-ai-provider-and-sdk.md):
a provider-neutral interface with a deterministic fake default, not a vendor SDK.

Steps are ordered so the seam lands first (done), the generation policy second,
and the HTTP and UI surfaces only once the pipeline can refuse an artifact
honestly. The model is an untrusted content generator and the Go service is the
policy (§11); every step below is written so a failure is reported with a stage
name rather than papered over.

## What M4 is

> Scope: provider interface and fake provider, challenge request and artifact
> schemas, generation pipeline (11.1), server-side keys, tutoring, on-demand
> critique, verification and repair (6.5), challenge lifecycle states added as
> needed.
>
> Exit gate:
> - An invalid generated setup never becomes active.
> - Reference solution and cross-check pass before activation.
> - Retries are bounded and failures are understandable.
> - Generated challenges survive restart and can be reset.
> - A reported defect is verified by execution before any patch is proposed, and
>   no patch is applied without the learner's approval.
> - Disabling AI leaves every other feature working.

### The gate, operationalized

| Gate clause | What proves it | Step |
| --- | --- | --- |
| Invalid generated setup never becomes active | `validate_artifact` refuses a wrong-shaped or over-cap bundle before `scratch_environment`; a test asserts the outcome is `rejected`/`failed` and that nothing was written to the depot | 3, 5 |
| Reference solution and cross-check pass before activation | `reference_contract` and `cross_check` are stages ahead of `activate`; activation is unreachable unless both are `done`; a test drives each failure with the fake provider | 3 |
| Retries are bounded and failures are understandable | `Limits.MaxRepairs` caps the `repair` stage; exhaustion fails as `AI_REPAIR_EXHAUSTED` and names the stage; `attempts[]` and `evidence.repairs` carry what happened | 3, 4 |
| Generated challenges survive restart and can be reset | `Depot` over `app.challenges` plus `GET /challenges`, `GET /challenges/{id}`, `POST /challenges/{id}/reset`; a test writes, rebuilds the service over the same store and reads the challenge back | 5 |
| A defect is verified by execution before a patch is proposed; no patch without approval | `reportDefect` executes the reproduction before `verified` can be true; `applyPatch` refuses without `approved`; a scripted-fake test covers `AI_PATCH_UNVERIFIED` and `AI_PATCH_UNAPPROVED` | 3, 5 |
| Disabling AI leaves every other feature working | the default build has no provider; `make verify-mock` and every existing `audit-*` stay green, and `make audit-ai` asserts `ai.available == false` with a reason and that the lab UI is hidden | 7 |

## Where we are today (measured, not assumed)

Measured against the last **landed** step, commit `7c951de11` (*M4 step 1: the AI
generation seam*), on 2026-10-15. Steps 2–8 are being built in parallel behind the
frozen seam, so the working tree runs ahead of this table; a row counts as landed
here only when the commit exists. The pipeline is still a stub: it returns
`unavailable`, and there is no AI test and no AI audit.

| M4 scope item | State | Evidence |
| --- | --- | --- |
| Provider interface | Done | `backend/internal/ai/provider.go`, `Provider{Descriptor, Complete}`, one completion in / text out, no tools, no filesystem; committed in `7c951de11` |
| Deterministic fake provider | **Missing** | no implementation of `Provider` in `backend/internal/ai/`; the package has no `_test.go` |
| OpenAI-compatible transport + server-side keys | **Missing** | `backend/go.mod` has no AI/vendor dependency and no HTTP provider; no key config in `cmd/api/main.go` |
| Challenge request + artifact schemas | Done | `contracts/ai.schema.json` (284 lines); `web/src/api/aiContract.ts` (types only) |
| Generation pipeline (§11.1) | Stub | `backend/internal/ai/pipeline.go`: `generate` returns `s.unavailable(...)`; the nine stage names are declared but nothing runs them |
| Tutoring | Stub | `Service.Tutor` returns `AI_UNAVAILABLE` (`pipeline.go`) |
| Critique / defect / patch (§6.5) | Stub | `Service.Critique`, `ReportDefect`, `ApplyPatch` all return `AI_UNAVAILABLE` |
| Persistence seam | **Missing** (step 1 only) | `ai.Depot` is declared in `backend/internal/ai/api.go`; `app.challenges` + `app.patches` and the `store` implementations are parallel work in the working tree, not in `7c951de11` |
| HTTP routes | Stubs (step 1 only) | `POST /courses/generate` → `handleGenerateCourse` echoes a canned shape; `POST /challenges/generate` → `{"ok":true}`; no `/challenges`, `/tutor`, `/critique`, `/report` or `/patches` route is in `7c951de11` (parallel work is adding them in the working tree) |
| `ai` on `GET /capabilities` | **Missing** | `handleCapabilities` returns `hard_timeout`, `explain`, `max_concurrent`, `isolation` only |
| Frontend lab UI | **Missing** | `rg aiContract web/src` finds no importer; the types exist and no screen compiles against them |
| AI tests / audit | **Missing** | no `backend/internal/ai/*_test.go`; no `audit-ai` target in `Makefile` (only the M3 `audit-*` set) |

Read plainly: M4 has a contract and the interfaces that every later step must
compile against, and nothing that generates, stores, serves or displays a
challenge yet. This plan does not claim any part of that is green.

The parallel work already on disk when this plan was written: `httpapi/ai.go`
and the extra routes in `server.go`, the `store` `Depot` implementations, is a
step 4/5 advance, and step status is tracked in `progress.md` rather than here.
This table is the baseline the plan is written against, so it does not move when
that work lands.

## Sequencing

| # | Step | Ships | Depends on |
| --- | --- | --- | --- |
| 1 | The AI generation seam | `contracts/ai.schema.json`, `backend/internal/ai`, `web/src/api/aiContract.ts` | **(done, `7c951de11`)** |
| 2 | Provider: deterministic fake + OpenAI-compatible transport | `DeterministicProvider`, `HTTPProvider`, key config | 1 |
| 3 | The §11.1 pipeline | the nine stages, `Limits` enforced, provenance and attempts | 2 |
| 4 | HTTP routes + the `ai` capability block | the challenge/tutor/critique/report/patch routes; `ai` on `/capabilities`; OpenAPI | 3 |
| 5 | Persistence | challenges survive restart; reset; the §6.5 defect flow | 3, 4 |
| 6 | Frontend seam + the lab UI | server-rendered stages; explain-on-request; the lab hidden with a reason when AI is off | 4, 5 |
| 7 | AI-off proof + exit-gate audit | `make audit-ai`; every non-AI feature green with no provider | 1–6 |
| 8 | M4 release | tag `v0.4.0` (owner's go-ahead), docs updated | 1–7 |

Step 2 comes before step 3 so the pipeline is written against a provider that is
deterministic and offline: the first end-to-end run of §11.1 happens with no
network and no key. Step 3 before step 4 so no route can expose an artifact that
has not been validated. Steps 4–5 are the backend surface and step 6 is the
frontend; they meet at step 7, which is the exit gate rather than a feature.
Step 8 only ships with the owner's go-ahead, matching M3's step 8.

## The §11.1 pipeline, in order

The stage names are the ones the seam already declares
(`backend/internal/ai/api.go`); the pipeline runs them in this order and reports
each as `done`, `failed` or `skipped`, never silently. The reason matters as
much as the order, because the order is what makes "nothing becomes visible"
checkable.

1. `validate_request`: validate the structured request (topics, difficulty,
   scenario, constraints). **Why:** the model is fed only well-formed, bounded
   input, and an oversized or malformed request is refused before it costs a
   completion.
2. `request_bundle`: ask for one candidate bundle under a constrained output
   schema. **Why:** this is the single place the model is called; a JSON Schema
   constrains the reply and `Limits.MaxOutputBytes` bounds it before parsing, so
   provider output is untrusted input from the first byte.
3. `validate_artifact`: validate size limits, table and row caps and allowed SQL
   constructs. **Why:** static policy runs on the untrusted text before any of it
   executes; this is where an invalid generated setup is stopped.
4. `scratch_environment`: create a throwaway environment, run the setup, check
   constraints and expected dataset properties. **Why:** the setup plan is
   untrusted SQL and must run where it cannot touch the learner's workspace or the
   shared dataset (ADR 0003; `Runner.Scratch`).
5. `reference_contract`: run the reference solution and confirm it satisfies its
   own result contract. **Why:** the artifact's own reference must pass its own
   contract, graded by the deterministic evaluator, never by the model.
6. `cross_check`: request a second, independently produced solution and require
   the same result on the generated data. **Why:** it catches some contract
   errors; it is not proof, because both solutions can share a mistake.
7. `adversarial_fixtures`: run the evaluator against known-wrong queries where
   feasible. **Why:** a contract that accepts a deliberately wrong query is too
   loose to grade with.
8. `repair`: repair within a small budget, then fail clearly. **Why:** the model
   may fix its own artifact, but only a bounded number of times (see below).
9. `activate`: save the artifacts and exact versions, then mark the challenge
   active. **Why:** nothing becomes visible to the learner before this stage, and
   provenance is written with the artifact so a later reader can see why it was
   allowed in.

Only `activate` may write to the depot, and only after stages 5–7 are `done`.

## The policy rules that keep generated content safe

- AI is off until a provider is configured, and core features work without it
  (§11.2). The default provider is the fake one, so "off" is the absence of a
  configured provider, not the absence of the feature.
- Provider keys live in the Go service and never reach the browser, `/capabilities`,
  logs or `QueryResult` (§11.2; §12 row *Provider key leak*).
- The first verdict always comes from the deterministic evaluator. AI never
  computes results and never sets pass or fail; only the learner can override a
  failure (`accepted_by_user`), and that is recorded separately (§11.2, §6.4).
- Tutoring never reveals the reference solution, and the request type makes that
  structural (below).
- SQL, comments, prompts and data values are untrusted input. A tool call routes
  through a normal service: schema inspection, query execution, evaluation,
  reset, save draft. There is no shell and no filesystem tool (§11.2; §12 row
  *Prompt injection*).
- A generated setup is untrusted SQL: it runs only in a throwaway environment,
  under the restricted learner role, with bounded statements (§12 row *Malicious
  generated setup*).
- Output size, setup statements, repairs, hints, timeout and concurrency are
  bounded by `Limits`; every zero value means "use the default", never
  "unlimited".
- A defect is verified by execution before a patch is proposed, and no patch is
  applied without the learner's approval (below; §6.5, §11.2).
- Rounds and tokens are capped, a verified or rejected claim is cached by exercise
  version plus claim, and reports per exercise per day are capped (§6.5).
- Nothing a learner sees is produced by anything but steps 5–9 above; a reading of
  the artifact that disagrees with the evaluator is a defect report, not an
  opinion.

### The repair loop, and why its bound is a policy number

`Limits.MaxRepairs` (default **2** in `DefaultLimits`) bounds stage 8. A repair is
not a transport retry: it is another provider completion *plus* another
scratch-environment run, so each unit costs money, time and an artifact that can
itself be wrong. The bound answers a product question: *how many times may the
model try to fix its own artifact before the run is refused*, which is why it is
a policy number reviewed with the rest of `Limits` and not a retry count copied
from a client library. Exhausting it fails with `AI_REPAIR_EXHAUSTED`, names the
stage, and reports the attempts; a dropped connection may be retried by the
transport, and that is invisible to this budget.

### Tutoring: never reveal the reference solution

`TutorRequest` carries the learner's query, the objective, the error and the hint
level, and deliberately carries no reference solution, so the policy cannot leak
what it was never given. `ProviderRequest` for a tutor call is built from that
same material. A `TutorResponse` whose `reveals_solution` is true is refused
rather than shown, and the hint level is capped by `Limits.MaxHints` (default 3).

### Defect verification and repair: execution first, approval always

A learner's report starts §6.5, not a mutation. The critic states a falsifiable
claim with the probes that would confirm it; the verifier sees the claim and the
exercise files, not the critic's reasoning, and runs the probes on the engine
through a read-only role. Only a defect confirmed by execution yields a
`PatchProposal`, which is stored and shown as a diff. The learner chooses Apply or
Dismiss; `applyPatch` refuses an unverified proposal (`AI_PATCH_UNVERIFIED`) and
refuses one without an approval record (`AI_PATCH_UNAPPROVED`). Applying bumps the
exercise `version` and adds a regression check; earlier attempts stay recorded
against the version they were graded on. A different prompt or model is preferred
for the verifier, so a model's own claim is not its own referee.

### The "AI is off" contract

What must keep working with no provider configured: lessons, chapter content,
`POST /query` and `POST /answers/evaluate`, hints and reference solutions, the
`accepted_by_user` override, progress, export/import, the tables drawer and
workspace reset, library and search. The AI surfaces are strictly additive, so
"off" is a smaller app, not a broken one.

How it is proven: `make verify-mock` and the existing `audit-*` suites run with no
provider and no key; a gateway with no provider answers the generation routes with
`AI_UNAVAILABLE` and a reason (never a fabricated challenge), or
`ENVIRONMENT_UNAVAILABLE` when a provider is configured but no scratch runner
exists; `GET /capabilities` reports `ai.available == false` with the reason; the
lab UI is hidden or disabled with that reason. Step 7 wraps this as
`make audit-ai`.

## Step 1: The AI generation seam (done)

Shipped in commit `7c951de11`.

1. `contracts/ai.schema.json`: the generation request, the `outcome` envelope
   (`active` is the only status that makes an artifact visible), the activated
   `challengeArtifact` with `provenance`, the §11.1 stage evidence, and the
   tutoring, critique and defect surfaces. Its error enum is an AI envelope,
   deliberately separate from the closed `QueryResult` enum.
2. `backend/internal/ai/`: `Provider`, `Depot`, `Runner`, `Limits`, `Service`,
   the stage names, `Evidence`, `Outcome`, and the honest stubs that return
   `unavailable`. Generation is off unless a provider, a runner and a depot are all
   present, and `Available()` says which one is missing.
3. `web/src/api/aiContract.ts`: the same shapes for the client, types only.

**Files/areas.** `contracts/ai.schema.json`, `contracts/README.md`,
`backend/internal/ai/{api,pipeline,provider}.go`, `web/src/api/aiContract.ts`.

**Verification.** `cd backend && go build ./... && go vet ./...`;
`cd web && npx tsc --noEmit`.

**Does not prove:** that anything generates, validates, stores or serves a
challenge. The pipeline bodies return `unavailable` on purpose.

## Step 2: The provider, a deterministic fake, and the OpenAI-compatible transport

1. `backend/internal/ai/fake.go`: `DeterministicProvider` satisfies `Provider`
   with `Descriptor().Deterministic = true` and a fixed, schema-valid bundle per
   request kind, derived from a stable hash of the request so the same request
   returns the same bytes. It never touches the network. It is a provider like any
   other: the fake produces the artifact and every later stage is the same code a
   hosted provider goes through.
2. `backend/internal/ai/http_provider.go`: `HTTPProvider`, a thin client for an
   OpenAI-compatible `POST {base}/chat/completions` (base URL, model and key from
   config, server-side). It sends only `ProviderRequest` fields, bounds the reply
   with `MaxOutputBytes` while reading, and maps transport, quota and malformed
   responses to `ErrProviderUnavailable`: never to a silent empty bundle.
3. Config in `cmd/api/main.go`: `QC_AI_BASE_URL`, `QC_AI_MODEL`, `QC_AI_KEY`
   (key server-side only). Unset selects the fake provider, so the default build
   generates with no network and no key. Provider selection is configuration, not
   code.

**Files/areas.** `backend/internal/ai/fake.go` (new),
`backend/internal/ai/http_provider.go` (new), `backend/internal/ai/fake_test.go`,
`backend/internal/ai/http_provider_test.go`, `backend/cmd/api/main.go`,
`README.md` / `backend/README.md` (the three variables). No `go.mod` change.

**Verification.** `cd backend && go test ./internal/ai/ -run Provider`: determinism
(same request → identical bytes) and schema validity; `HTTPProvider` against an
`httptest.Server` asserting the request shape, the byte cap and the
`ErrProviderUnavailable` mapping; `go build ./...` proves no vendor dependency was
added.

**Does not prove:** that a hosted provider's output is good, or that the transport
handles every provider's dialect: only the compatible subset it uses. End-to-end
provider latency and cost are unmeasured until a key is configured.

## Step 3: The §11.1 pipeline

1. Replace each stub body in `pipeline.go` with the real stage, in the declared
   order, reporting `Stage{name,state,detail,ms}` for each. Nothing before
   `activate` writes to the depot.
2. `validate_request` uses `GenerationRequest` and `Limits.MaxPromptChars`;
   `request_bundle` calls `Provider.Complete` with the artifact JSON Schema and
   `MaxOutputBytes`; `validate_artifact` checks caps, the allowed SQL constructs
   and the `SetupPlan` statement cap.
3. `scratch_environment` uses `Runner.Scratch` (a throwaway environment, never the
   learner workspace), applies the setup and checks the expected dataset
   properties; the release function is always called.
4. `reference_contract` runs the reference solution through the real
   `engine.Execute` plus `eval` and requires it to satisfy its own
   `ResultContract`.
5. `cross_check` requires the second solution to return the same result on the
   generated data. The result is recorded as evidence, never as proof.
6. `adversarial_fixtures` requires the evaluator to reject every injected wrong
   query; a contract that accepts one fails the stage.
7. `repair` is entered only for a failed stage and is bounded by `MaxRepairs`;
   each attempt is recorded in `attempts[]`; exhaustion is
   `AI_REPAIR_EXHAUSTED` with the stage named.
8. `activate` writes the artifact and outcome through `Depot.SaveChallenge` with
   provenance (`provider`, `model`, `provider_version`, `pipeline_version`,
   `evaluator_version`, `dataset_version`, `repairs`).

**Files/areas.** `backend/internal/ai/pipeline.go`, `backend/internal/ai/pipeline_test.go`
(new), a scripted provider for failure injection in tests. `Runner` is satisfied
by `envdb` (step 5 wiring) and by `ProvidedRunner` in tests.

**Verification.** `cd backend && go test ./internal/ai/`: with the fake provider a
valid request activates; a malformed bundle is rejected and nothing is stored; a
reference that fails its contract is rejected at `reference_contract`; a cross-check
disagreement is rejected at `cross_check`; a contract that accepts a wrong fixture
is rejected at `adversarial_fixtures`; a bundle that never converges exhausts
`MaxRepairs`. A test asserts every declared stage name can be produced, in order.

**Done when:** every stage name in `api.go` is reachable from a test, and `active`
is the only status that stores a challenge.

**Does not prove:** that the checks are complete: the cross-check shares blind
spots with the reference, and the adversarial fixtures run "where feasible": nor
that the model produces good content.

## Step 4: HTTP routes and the `ai` capability block

1. Register the routes in `server.go` (`GET /challenges`, `GET /challenges/{id}`,
   `POST /challenges/{id}/reset`, `POST /tutor`, `POST /challenges/{id}/critique`,
   `POST /challenges/{id}/report`,
   `POST /challenges/{id}/patches/{patchID}`) and replace the two generation
   stubs' bodies with `Service.GenerateChallenge` / `Service.GenerateCourse`.
2. `handleCapabilities` gains an `ai` object from `Service.Available()`
   (`configured`, `available`, `provider`, `model`, `deterministic`, `reason`),
   matching `availability` in `contracts/ai.schema.json`.
3. OpenAPI (`openapi.go`) describes every route and the `ai` capability, and
   `openapi_test.go`'s route list is extended so a route without a description
   fails the test.
4. Status and the envelope: an unavailable service answers with `AI_UNAVAILABLE`
   (or `ENVIRONMENT_UNAVAILABLE`) and a reason; a refused artifact answers with
   `status: rejected`/`failed` from the outcome schema; pick one code per case and
   assert it. The learner-facing message names the stage.

**Files/areas.** `backend/internal/httpapi/server.go`, `handlers.go` (or a new
`ai.go`), `openapi.go`, `openapi_test.go`, `contract_test.go`.

**Verification.** `cd backend && go test ./internal/httpapi/`: each AI route's
response validates against `contracts/ai.schema.json`; `/capabilities` carries
`ai`; `POST /challenges/generate` with no provider is `unavailable`, never a
`201` with a challenge.

**Done when:** every route listed above exists, every response validates against
the AI schema, and `/capabilities` is the one honest source of whether the lab can
run.

**Does not prove:** the UI (step 6), or that a hosted provider answers inside the
timeout.

## Step 5: Persistence, surviving a restart and resetting on demand

1. Make `Depot` the only path to a stored challenge; `Depot` is already backed by
   `app.challenges` / `app.patches` (store, working tree). `Service.Challenges`,
   `Challenge` and `Reset` read and delete through it. `Reset` deletes the
   challenge and its patch proposals; learner attempts are the learner's data and
   are not touched.
2. Wire the §6.5 defect flow behind `POST /challenges/{id}/report` and
   `POST /challenges/{id}/patches/{patchID}`: verify by execution, then store a
   proposal, then apply only on approval (rules section above).
3. A restart is a new `Service` over the same store, so a stored challenge is read
   back with the `outcome` captured at activation.

**Files/areas.** `backend/internal/store/{store,postgres,memory,challenges}.go`,
`backend/internal/ai/api.go` (`Depot` usage), `backend/internal/httpapi/`
(report/patch handlers), `backend/cmd/api/main.go` (construct the `Service`).

**Verification.** `cd backend && go test ./internal/store/ ./internal/ai/`:
write → rebuild the service → read back → delete; a report of an unverified claim
yields no patch (`AI_PATCH_UNVERIFIED`), a verified claim yields a stored proposal,
applying without approval is refused (`AI_PATCH_UNAPPROVED`), applying with
approval is recorded and bumps the version. `make verify` still green.

**Done when:** a challenge generated before a restart is listed after it, reset
removes it, and no patch is applied without an approval record.

**Does not prove:** that persistence survives every failure mode (restart and
reset are the named cases, and a failed write is surfaced rather than silently
dropped); ownership rules beyond the `owner_id` the schema already carries.

## Step 6: Frontend seam and the lab UI

1. `web/src/api/aiContract.ts` is the compile target; the operations are added to
   the `ExecutionService` seam (`web/src/api/`). A screen never calls `fetch` and
   never fabricates a result (AGENTS.md).
2. The lab renders the stages the server reports, in order, with
   `done`/`failed`/`skipped` and the stage detail. The UI does not infer progress
   or invent a stage the server did not report.
3. Generation is disabled with a reason when `ai.available` is false, and the
   reason is the server's. When the adapter has no gateway at all (browser-only),
   the lab says the same honest thing: AI is off.
4. Explain-on-request calls `POST /tutor` and renders the hint; it never shows the
   reference solution. The report flow renders the verdict and the diff with Apply
   and Dismiss.

**Files/areas.** `web/src/api/` (operations through the seam), `web/src/components/`
(the lab), `web/src/state/`, `web/src/types.ts` (`ai` on capabilities). No backend
files beyond step 4.

**Verification.** `cd web && npx tsc --noEmit`; a component or audit test
asserting: the lab hides with a reason when `ai.available` is false; a stage list
renders exactly the server's states; a tutor response with `reveals_solution: true`
is refused rather than shown.

**Done when:** the lab is driven entirely by server responses, and a learner
cannot reach a generation control that the service would refuse.

**Does not prove:** the pedagogy of a generated challenge, or that the lab is
pleasant at provider latency: no latency budget is set here.

## Step 7: AI-off proof and the M4 exit-gate audit

1. `scripts/ai-audit.mjs` + `make audit-ai` (added to `make test`): boot the
   gateway with no provider and assert every non-AI route still answers and
   `/capabilities` reports `ai.available == false` with a reason; then boot with
   the fake provider and assert a challenge passes all nine stages to `active` and
   is listed; assert a malformed bundle is rejected and stored nowhere.
2. `make verify` and `make verify-mock` green with no provider configured.
3. Walk the six exit-gate clauses against the evidence above and publish the
   result in this plan's gate table and in `progress.md`.

**Files/areas.** `scripts/ai-audit.mjs` (new), `Makefile` (`audit-ai`, added to
`test`), `docs/technical-design.md` §14 M4 note, `progress.md`.

**Verification.** `make audit-ai`; `make verify`; `make verify-mock`.

**Done when:** the lab runs end-to-end against the fake provider and is honestly
off without one, and each gate row has a measured result rather than a promise.

**Does not prove:** a hosted provider, or that the fake is representative of one.

## Step 8: M4 release

Release checklist: the owner's go-ahead is required before the tag (the "do not
commit or tag without asking" constraint holds):

1. `make verify` and `make verify-mock` green; `make audit-ai` green with the
   fake provider.
2. The live-only suites re-run against a booted gateway (`make audit-load`,
   `make audit-metrics-live`, `make audit-adversarial-live`) so the AI routes
   exist in the same build.
3. Docs consistent: README (the AI variables and the "AI is off" behaviour),
   `progress.md` (the M4 board all `done`), this plan,
   `docs/technical-design.md` §14 M4 and §15.
4. Version: `web/package.json` `0.3.0` → `0.4.0`.
5. Commit the M4 work, then tag `v0.4.0`.
6. Flip the M4 row in `progress.md` to `done, tagged v0.4.0` and mark the design
   §14 M4 section shipped.

**M4 exit gate, mapped:** invalid setup never active → steps 3 + 5; reference and
cross-check before activation → step 3; bounded retries, understandable failures
→ step 3; survive restart and reset → step 5; defect verified before a patch and
no patch without approval → steps 3 + 5; AI off leaves everything else working →
step 7.

## Risks and open questions

- **Cross-check blind spots.** Two solutions can share a mistake, so the
  independent signal is the adversarial fixtures, which run "where feasible". The
  plan does not claim they are feasible for every artifact shape; a shape with no
  fixture is recorded as such.
- **The repair budget is a cost decision without a price.** `MaxRepairs = 2` is a
  policy default, not a measured optimum; if a completion's price is high, this is
  the first number to revisit.
- **Provider output shape is unmeasured.** The transport uses the compatible
  subset (`POST /chat/completions`, a JSON reply, a byte cap). A provider that
  streams or uses a different structured-output dialect is a spike question, not
  an assumption.
- **A configured-but-broken provider is a third state.** No provider reports
  `AI_UNAVAILABLE` with a reason; a configured provider that fails a call reports
  the same code from a different path. Step 7 proves the first, not the identity of
  the two.
- **Where the key comes from.** This plan assumes environment variables. Reading a
  key from a file or a secret store would be a new decision.
- **Cost and rate.** `Limits` bounds bytes, statements, repairs, hints, timeout and
  concurrency, not dollars. §6.5's reports-per-day cap has no number yet.

## What this does NOT prove

- **That a generated challenge is pedagogically good.** Execution cannot settle
  teaching quality (§6.5); that stays a human judgment.
- **That the cross-check proves correctness.** It catches some contract errors and
  not shared ones (§11.1 step 6).
- **That the policy catches every contract-shaped mistake.** A provider can still
  return something schema-valid and wrong; the cross-check catches some, not all.
  This is the decision's stated residual risk (ADR 0005).
- **That the fake provider exercises a model's failure modes.** It is
  deterministic by design; it proves the pipeline, not the model.
- **That the model cannot be prompt-injected.** The rule is that content is
  untrusted and tools enforce policy independently; a misleading explanation is
  the residual risk §12 already records.
- **That cost is bounded in money.** The bounds are bytes, repairs, hints, timeout
  and concurrency.
- **That persistence survives every failure.** Restart and reset are the named
  cases; a failed write is surfaced, not exhaustively tested.
- **That a hosted provider works end-to-end.** CI runs the fake provider; the real
  transport is exercised only against a local test server.
- **That the AI-off proof covers a misconfigured provider.** It covers no
  provider.

## Constraints (do not violate)

- Greenfield: no migrations, no backward compatibility, no data backfill.
- Every frontend data access goes through the `ExecutionService` seam
  (`web/src/api/`); a screen never calls `fetch` and never fabricates a result.
- The Go gateway stays honest: with no provider or no database it reports
  `AI_UNAVAILABLE` / `ENVIRONMENT_UNAVAILABLE`, never invented rows or challenges.
- Provider keys are server-side only; no secret, DSN or key in logs,
  `QueryResult`, `/capabilities` or `/diagnostics` (§12).
- AI never computes results or sets pass/fail; the deterministic evaluator owns the
  verdict and only the learner overrides.
- A new grading rule is added as fixtures first
  (`contracts/fixtures/`, `make audit-fixtures`).
- The single binary stays cgo-free and Docker-free; `make check-cgo` stays green.
- Do not commit or tag without asking.
