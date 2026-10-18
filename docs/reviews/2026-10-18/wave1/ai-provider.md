# ai-provider — slop audit (wave 1)

Scope: `backend/internal/ai/provider/**` (`provider.go`, `course.go`,
`http_provider.go`, `piai/pi_ai.go`) plus `backend/internal/ai/jobs/**` and
`backend/internal/ai/transcript/**`. Read-only. `go build ./internal/ai/...` is
green. Earlier review `docs/reviews/2026-10-17/02_ai_pipeline.md` read only to
skip landed items; the provider collapse, the dead pre-pi wire, `RawConfig`,
`StreamFn` on `Config` and `resolveCourseModel` are gone and are not re-reported.

## Verdict

The collapse landed: one `CourseCapability` on the provider seam, one custom
model builder, one single-shot HTTP transport. What is left is a thinner layer
of the same disease. The provider seam carries two sentinels, four attribution
fields and one config knob that no production caller reads, so `provider.go` and
`http_provider.go` describe outcomes nobody branches on. The one real duplication
still standing is the OpenAI-compatible model record: `provider.CustomModel`
(`course.go:51`) and the custom branch of `piai.New` (`pi_ai.go:70-79`) are
byte-for-byte the same `piai.Model`, and `piai` already imports `provider`, so
the copy is free to delete. The `Jobs` registry and the `transcript.Recorder`
are earned: every method has a live HTTP or pipeline consumer, and the bounds
(`maxFinishedJobsPerOwner`, `jobHeadroom`, the entry and byte caps) are the
stated contract. Below the fold: one dead concurrency guard that `Limits.Merge`
already makes unreachable, one write-only `PIAI.api` field, and a doc claim that
a keyless local server is supported while the only keyless path is refused by
`Complete`.

## Delete

- `ErrProviderUnavailable` — `provider.go:15`. Why it exists: the sentinel was
  added so a caller could tell "provider down" from "bug" and report
  `AI_UNAVAILABLE`. Evidence it has no consumer: `grep -rn 'ErrProviderUnavailable'
  backend --include=*.go` returns only `provider.go` and the twelve wrapping
  sites in `http_provider.go:146-224`; nothing outside the package does
  `errors.Is`. The pipeline maps every provider error to `CodeUnavailable`
  unconditionally (`pipeline.go:465-469`), so the distinction is already lost at
  the only boundary that reads errors. Remedy: delete the var, change the twelve
  `fmt.Errorf("%w: ...", ErrProviderUnavailable, ...)` to plain `fmt.Errorf`, and
  drop the `errors.Is` assertions in `http_provider_test.go:156,182,204,226,239,309`.
  If the owner wants the distinction instead, add the `errors.Is` branch in
  `completeRaw`; do not keep a write-only sentinel.

- `ProviderResponse.Provider` and `ProviderResponse.Model` — `provider.go:70-71`.
  Why they exist: pre-pi attribution on the raw reply. Evidence: the only writers
  are the providers (`piai/pi_ai.go:164-165`, `http_provider.go:229-230`,
  `fake.go:104-105`); the only readers are tests (`piai/pi_ai_test.go:91`,
  `http_provider_test.go:57`). Provenance does not read them; it reads
  `ProviderDescriptor` through `provenance()` (`pipeline.go:792-802`). Two truths
  for one fact. Remedy: delete both fields, the three writer blocks, and the two
  test assertions.

- `ProviderResponse.TokensIn` and `ProviderResponse.TokensOut` — `provider.go:72-73`.
  Why they exist: token accounting on the seam. Evidence: readers are tests only
  (`piai/pi_ai_test.go:121`, `http_provider_test.go:60`); no production path
  reads them (`grep -rn 'TokensIn\|TokensOut' internal cmd` outside the provider
  packages returns nothing). Remedy: delete both fields; they were only ever
  filled from `httpChatResponse.Usage` (`http_provider.go:231-232`) and
  `msg.Usage` (`pi_ai.go:166-167`), so delete the writers and the tests that
  assert them.

- `httpChatResponse.Usage` — `http_provider.go:122-125`. Why it exists: the
  `usage` block of the OpenAI reply. Evidence: its two fields feed only
  `TokensIn`/`TokensOut` (`http_provider.go:231-232`), which the item above
  deletes; nothing else reads `parsed.Usage`. Remedy: delete the struct field and
  the JSON tag; the reply body no longer needs `usage`.

- `httpChatResponse.Choices[].FinishReason` — `http_provider.go:120`. Why it
  exists: the pre-pi course wire needed a stop reason; `ProviderResponse.FinishReason`
  is already deleted. Evidence: `grep -rn 'FinishReason' internal` returns only
  this declaration. Remedy: delete the field. `http_provider_test.go:299` sends
  `"finish_reason": "stop"` in a fixture, which stays harmless, but the assertion
  at `:306-311` is about missing content and does not need it.

- `ToolCall.Type` — `provider.go:35`. Why it exists: the OpenAI tool-call
  envelope carries `type: "function"`. Evidence: `grep -rn 'Type:' internal`
  shows no production writer; the only writers are three test literals
  (`course_agent_test.go:188,200,210`), and `Type` is read nowhere (`grep -rn
  'call\.Type\|tc\.Type' internal` returns nothing). The transport that would
  have serialised it (tools in the request) is deleted, and `ProviderResponse`
  is never marshalled. Remedy: delete the field and the three test literals.

- `HTTPProviderConfig.HTTPClient` — `http_provider.go:48`, with its comment
  `http_provider.go:40-41`. Why it exists: "a seam for tests". Evidence:
  `grep -rn 'HTTPClient' backend --include=*.go` returns the doc comment, the
  field and the `cfg.HTTPClient` read at `:55`; no test and no production caller
  sets it. `http_provider_test.go:35` drives `httptest.NewServer` through
  `BaseURL` with the default client. A seam with zero consumers is not a seam.
  Remedy: delete the field and the nil fallback; keep `client := &http.Client{}`.

- `PIAI.api` — `pi_ai.go:56`, written at `:80,:92` and `pi_ai_test.go:49`. Why it
  exists: a residue of the pre-collapse resolver that kept the resolved Api
  separately from the model. Evidence: `grep -rn 'p\.api' internal/ai/provider/piai`
  returns only writes; `CompleteSimple` resolves the Api off `p.model`
  (`pi_ai.go:156`). Remedy: delete the field and its three writes; `p.provID`
  stays because `Descriptor()` reads it (`pi_ai.go:128`).

- `(*Jobs).Job` context parameter — `jobs.go:246-247` (`_ = ctx`). Why it exists:
  the comment at `:244-245` says a future store-backed registry will need it.
  Evidence: no method body uses it; the only callers pass a context
  (`httpapi/ai.go:298`, `jobs_test.go:150,215,353`). Speculative generality, not a
  live need. Remedy: drop the parameter from the signature and the call sites.

- `NewJobs`'s `MaxConcurrent` fallback — `jobs.go:149-151`. Why it exists: a
  guard for a zero limit. Evidence: `l := limits.Merge(ai.DefaultLimits())`
  (`:148`) fills a zero `MaxConcurrent` from the fallback
  (`api.go:223-225`), and `DefaultLimits().MaxConcurrent` is `2` (`api.go:199`),
  so `l.MaxConcurrent <= 0` is unreachable. Remedy: delete the three-line guard;
  `maxRunning: l.MaxConcurrent` reads the merged value directly.

- `Jobs.maxFinished` field — `jobs.go:133`, set at `:155`. Why it exists: it
  mirrors the bound as a field. Evidence: the only writer is the constant
  `maxFinishedJobsPerOwner` (`jobs.go:123,155`), and there is no setter;
  `evictLocked` reads it once (`:346`). A compile-time constant dressed as
  configuration. Remedy: delete the field and use `maxFinishedJobsPerOwner`
  directly in `evictLocked`.

- `transcript_test.go:68-69` — `got, ok := rec.Finish(...), true; _ = ok`. Why it
  exists: a leftover from an earlier assertion. Evidence: `ok` is assigned
  `true` and immediately blanked; the test only uses `got`. Remedy: delete both
  lines, keep `got := rec.Finish(...)`. Small, but it is the kind of line that
  teaches a reader that the recorder has a second return value it does not.

## Collapse

- The OpenAI-compatible model record is written twice.
  `provider.CustomModel(baseURL, model)` (`course.go:51-62`) and the custom
  branch of `piai.New` (`piai/pi_ai.go:70-79`) build the identical
  `piai.Model`: `Api: "openai-completions"`, `Provider: "custom"`,
  `Input: ["text"]`, `ContextWindow: 128000`, `MaxTokens: 16384`. `provider`
  already imports `piai/ai` (`course.go:5`) and `piai` imports `provider`
  (`pi_ai.go:35`), so no cycle blocks the call. Remedy: `piai.New` sets
  `p.model = provider.CustomModel(cfg.BaseURL, cfg.Model)`, and `provID` stays
  `"custom"`; the inline literal and its drift risk disappear.

- A keyless local server is documented as supported and refused by the same
  package. `http_provider.go:73-75` says a key is `MissingCredential`'s job
  "because a local server needs no key", and `api.go:293-300` says "a local
  server that needs no key reports configured and available". The only
  implementation of `MissingCredential` (`http_provider.go:92-94`) returns true
  whenever the key is empty, and `Complete` refuses an empty key outright
  (`http_provider.go:148-150`). The package's own purpose is local servers
  (`http_provider.go:17-20`: llama.cpp, Ollama, vLLM). Two truths for one mode.
  Remedy: pick one. To support the stated mode, send no `Authorization` header
  when the key is empty (`:187-188`) and stop reporting a missing credential for
  it; to require a key, delete `MissingCredential`, drop the optional-interface
  branch in `api.go:297-300` and the `KeySet` read in `admin_ai.go:95-96`, and
  correct both comments.

- Two stale "Tools" doc claims describe a wire that is deleted.
  `provider.go:66-68` says an empty `Text` is fine "once Tools were advertised",
  and `provider.go:79-80` says a model may call "the ones the caller advertises
  in Tools". `ProviderRequest` has no `Tools` field. `http_provider_test.go:257-268`
  still heads a test block with "The tool-calling half of the wire ... tools are
  sent without a response_format", and line `266` names
  `TestHTTPProviderSendsToolsAndParsesAToolCall`, a test that does not exist.
  Remedy: reword the two `provider.go` comments to the single-shot reality
  (`ToolCalls` can arrive empty-text; the caller executes any call), and replace
  the test block comment with what `TestHTTPProviderAsksForTheSchema` proves.

- The run kind travels in two fields. `Submission.Kind` (`jobs.go:102`) and
  `Request.Kind` reach `Submit` together from the HTTP path
  (`httpapi/ai.go:188` sets `req.Kind`, `:263` sets `Kind`), and `Submit` then
  overwrites one with the other (`jobs.go:230-232`). Evidence one is needed only
  because the other is not always set: the continuation path passes
  `Kind: "course"` with an empty `Request` (`httpapi/ai.go:633-638`), and
  `jobs_test.go:206` passes `Kind: "course"` with a `Request` whose `Kind` is
  empty. Remedy: make the kind derivable from the submission, such as
  `course` when `BuildCourseID != ""` and `Request.Kind` otherwise, so one field
  carries it; or drop `Submission.Kind` and require `Request.Kind`, setting it in
  the continuation path too.

- `jobs.Job` and `jobs.JobView` describe `id`/`kind`/`status` twice.
  `Job{ID,Kind,Status}` (`jobs.go:49-53`) is the `Accepted` ack
  (`httpapi/ai.go:280`); `JobView{ID,Kind,Status,...}` (`jobs.go:60-88`) is the
  poll body (`:307`). The client reads both shapes (`web/src/api/ai.ts:643-646`
  and `:661-681`), and OpenAPI documents them as `AIJobHandle` and `AIJob`
  (`apidocs.go:1813,1821`). The ack repeats three fields of the view and always
  reports `status: "running"`; a poller cannot use them before the view arrives.
  Remedy (low conviction): return the view from `Submit` and delete `Job`, or
  reduce the ack to `{id, kind}`. Confirm with the client first.

## Move / split

- The alias `piai` means two packages. In `provider/course.go:5` and
  `jobs/jobs_test.go:18`, `piai` is `github.com/sky-valley/pi/ai`; the repo also
  has package `piai` at `provider/piai` (`pi_ai.go`). A reader following
  `piai.Model` in `course.go` lands on the wrong package. Remedy: alias the
  vendored package to `piAI` (or `pimodel`) in the files that use both meanings,
  leaving our package name `piai` unique. File hygiene only.

- Prompt rendering lives in the transport. `UserContent`,
  `maxPreviousContextChars`/`maxFailureChars` and `ClipRunes`
  (`http_provider.go:237-296`) are called by `piai.New`'s `toContext`
  (`pi_ai.go:200`) and by `course_agent.go:1519` and
  `transcript.RecordRequestMessages` (`transcript.go:247-257`), so they are not
  transport-private. Optional split: a `prompt.go` holding `UserContent`,
  `ClipRunes`, the two bounds and the redaction helpers would leave
  `http_provider.go` as wire types plus `Complete`. Not required at 321 lines;
  worth it only if the pi migration adds another prompt site.

## Earned (checked, keep)

- `provider.Provider` / `Complete` — the seam; live at `pipeline.go:465`,
  `fake.go:52`, `http_provider.go:143`, `pi_ai.go:145`.
- `ProviderDescriptor.Configured` / `.Deterministic` / `.Name` / `.Model` — read
  by `Service.Available` (`api.go:284-292`); not duplicated elsewhere.
- `CourseProvider` / `CourseCapability` / `CourseCapabilityOf` — live at
  `course_agent_tools.go:50-88`; the `bool` return is the "cannot run the agent"
  branch.
- `CustomModel` — live at `http_provider.go:279`; keep, but dedupe per Collapse.
- `UserContent` — live at `pi_ai.go:200` and `transcript.go:254`; the repair
  context it renders is verified by `http_provider_test.go:87-123`.
- `ClipRunes` — live at `course_agent.go:1519` (`maxToolResultChars`).
- `Redact` / `secretRedactor` / `Redactor` — live: the exported method is the
  interface target the recorder calls (`transcript.go:101-106`), proved by
  `transcript_test.go:62-76`.
- `MissingCredential` — live at `api.go:297` and `admin_ai.go:95`; keep as a
  symbol, resolve the keyless contradiction above.
- `DefaultMaxOutputBytes` — live at `config.go:113`, `api.go:195`,
  `admin_ai.go:243`, `http_provider.go:61`.
- `NewHTTPProvider.Config` (`BaseURL/Model/APIKey/Timeout/MaxOutputBytes`) —
  live at `cmd/api/ai.go:47-53` and `admin_ai.go:238-244`.
- `piai.Config` (`Provider/Model/APIKey/BaseURL/TimeoutMS/MaxRetries`) and
  `piai.New` — live at `admin_ai.go:248,261`.
- `piai.Catalog` / `CatalogEntry` — live at `admin_ai.go:114`.
- `Recorder` and all its record methods (`Message`, `RecordRequestMessages`,
  `RecordResponse`, `RecordToolResult`, `RecordError`, `Finish`, `ID`) — live at
  `pipeline.go:332-356`, `course_agent.go:857`, `course_agent_tools.go:102-135`.
- `WithRecorder` / `From` / `WithRunID` / `RunIDFrom` / `NewID` / `MaxRunList` —
  live at `jobs.go:307,397` and `httpapi/runs.go:81`.
- `Jobs` registry surface (`NewJobs`, `Submit`, `Job`, `Running`, `Drain`,
  `DiscardCourse`, `JobRunning`, `JobView`, `ServiceFor`, `Submission`) — every
  one has an HTTP consumer (`httpapi/ai.go:38,81,261,298,548,633`) or the
  `recordJob` hook (`:835`).
- `jobHeadroom`, `maxFinishedJobsPerOwner` — live at `jobs.go:232`, `:155`.
- HTTP wire types `httpChatRequest`, `httpChatMsg`, `responseFormat`,
  `maxCompletionTokens` — live at `http_provider.go:164-176`.
- `truncate` — live at `http_provider.go:212`.

## UNCERTAIN

- `ProviderDescriptor.Version` — read once, by `provenance()`
  (`pipeline.go:796`). `HTTPProvider` and `FakeProvider` hardcode `"1"`;
  `PIAI.Descriptor` (`pi_ai.go:121-131`) never sets it, so a pi-ai run stores an
  empty `ProviderVersion`. The field is live but its content is a constant or a
  blank. Owner decision: is a provider version still a fact worth storing, or
  should provenance drop it?
- Nil-receiver guards on `*HTTPProvider` (`Descriptor` `:77-79`,
  `MissingCredential` `:93`, `redact` `:310`). No typed-nil literal exists
  (`grep -rn '(\*HTTPProvider)(nil)'` returns nothing) and `NewHTTPProvider` never
  returns nil, so the branches look unreachable. They are cheap panic guards, so
  I did not classify them as Delete; a runtime proof that no typed nil reaches
  the interface would settle it.
- `jobs.Job` vs `jobs.JobView` (Collapse item 5). The ack is documented and read
  by the web client, so collapsing needs a client change; confirm with `web/`
  before deleting `Job`.
- Keyless local server support (Collapse item 2). Whether the gateway intends
  keyless Ollama/llama.cpp is a product call, not a code proof.
