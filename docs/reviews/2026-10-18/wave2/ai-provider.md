# ai-provider — wave 2 verification

## Verdict

Almost all of wave 1 survives. Of its 19 findings, 11 are confirmed as written (10 actions),
7 need a corrected remedy (4 actions), and 1 is a suggestion the report itself declines. The
domain has not moved: every cited line still resolves, and `rg` over the whole repo
(including `web/`, `scripts/`, tests) returns the same small consumer sets the report names.
Two Delete items are under-counted, not wrong: `ProviderResponse.{Provider,Model,TokensIn,TokensOut}`
are written from test doubles the writer list omits, so their remedy must name two more files.
The one Collapse item the job called out, the duplicated OpenAI-compatible model record,
checks out field for field; `provider.CustomModel` and the `piai.New` literal build the same
`piai.Model` with no behavioral delta. The final list is 10 confirmed actions, 4 amended, 1
dropped, and 1 new gap.

## Confirmed actions

Ordered by value.

- [ ] Collapse the OpenAI-compatible model record: in `piai.New` set
  `p.model = provider.CustomModel(cfg.BaseURL, cfg.Model)` and delete the inline literal.
  Files: `backend/internal/ai/provider/piai/pi_ai.go:70-79`.
  Evidence: field-by-field diff of `provider.CustomModel` (`provider/course.go:52-61`) and
  the literal (`pi_ai.go:70-79`) is identical: `ID`, `Name`, `Api:"openai-completions"`,
  `Provider:"custom"`, `BaseURL`, `Input:["text"]`, `ContextWindow:128000`,
  `MaxTokens:16384`. The only substitution is `cfg.Model`→`model` and `cfg.BaseURL`→`baseURL`.
  No cycle: `piai` imports `provider` (`pi_ai.go:35`), `provider` imports `pi/ai`
  (`course.go:5`) but not `piai`. `p.provID` stays `"custom"`; `p.api` is item 4.
  `rg -n 'openai-completions' backend` shows the string in `course.go:55`, `pi_ai.go:73,80`
  only. Safe because the record is a pure value with no registration side effect.

- [ ] Delete `ErrProviderUnavailable`: drop the var and its two comments, change the 12
  `fmt.Errorf("%w: ...", ErrProviderUnavailable, ...)` wraps to plain `fmt.Errorf`, drop the
  6 `errors.Is` assertions, and fix the two docs that name it.
  Files: `provider.go:12-15,26`; `http_provider.go:140,146,149,181,185,195,197,206,209,212,217,220,224`;
  `provider/http_provider_test.go:156,182,204,226,239,309`;
  `docs/adr/0005-ai-provider-and-sdk.md:68`; `docs/m4-implementation-plan.md:251,265`.
  Evidence: `rg -n 'ErrProviderUnavailable' . --glob '!docs/**'` returns only
  `provider.go`, `http_provider.go`, `http_provider_test.go`. `pipeline.go:466-469` maps
  every provider error to `CodeUnavailable` unconditionally, so the distinction is already
  lost. Safe: nothing outside the package does `errors.Is`.

- [ ] Delete `PIAI.api` and its three writes.
  Files: `provider/piai/pi_ai.go:56,80,92`; `provider/piai/pi_ai_test.go:49`.
  Evidence: `rg -n '\.api\b' backend/internal/ai/provider/piai` returns only writes.
  `Complete` resolves the Api off the model (`pi_ai.go:156`, `ai.CompleteSimple(ctx, p.model, ...)`);
  the test's model already carries `Api: api` (`pi_ai_test.go:48`). Safe.

- [ ] Delete `HTTPProviderConfig.HTTPClient`, its comment, and the nil fallback; keep
  `client := &http.Client{}`.
  Files: `http_provider.go:40-41,48,55-58`.
  Evidence: `rg -n 'HTTPClient' . --glob '!docs/**'` returns only the comment, the field and
  the read at `:55`; `http_provider_test.go:35` drives `httptest.NewServer` through
  `BaseURL` with the default client. Safe.

- [ ] Delete `ToolCall.Type` and the three test literals.
  Files: `provider.go:35`; `backend/internal/ai/course_agent_test.go:188,200,210`.
  Evidence: `rg -n 'call\.Type|tc\.Type' backend` returns nothing; `rg -n 'Type:' backend/internal/ai`
  shows the three `"function"` literals plus unrelated `aid`/event types. Writers
  `course_agent_tools.go:490,512` and `pi_ai.go:179` set only `ID`/`Function`. Safe.

- [ ] Bound the jobs registry to one truth: delete the `NewJobs` zero-limit fallback
  (`jobs.go:149-151`) and the `Jobs.maxFinished` field (`jobs.go:133,155`); read
  `maxFinishedJobsPerOwner` directly in `evictLocked` (`jobs.go:346`).
  Files: `backend/internal/ai/jobs/jobs.go`.
  Evidence: `l := limits.Merge(ai.DefaultLimits())` (`:148`) fills a zero `MaxConcurrent`
  (`api.go:223-225`) from `DefaultLimits().MaxConcurrent = 2` (`api.go:199`), so
  `l.MaxConcurrent <= 0` is unreachable; `maxFinished` has one writer (the constant) and one
  read. Safe.

- [ ] Drop the unused `ctx` from `(*Jobs).Job` and update the callers.
  Files: `jobs.go:244-247`; `backend/internal/httpapi/ai.go:298`;
  `backend/internal/ai/jobs/jobs_test.go:150,215,353`.
  Evidence: `_ = ctx` at `jobs.go:247`; `rg -n '\.Job\(' backend` shows the three callers.
  Safe: no body use.

- [ ] Delete the dead assertion lines `got, ok := rec.Finish(...), true` / `_ = ok`, keep
  `got := rec.Finish(...)`.
  Files: `backend/internal/ai/transcript/transcript_test.go:68-69`.
  Evidence: `ok` is assigned `true` and blanked; only `got` is used at `:70-74`. Safe.

- [ ] Reword the two stale "Tools" comments to the single-shot reality and replace the test
  block comment; delete the reference to a test that does not exist.
  Files: `provider.go:66-68,79-80`; `provider/http_provider_test.go:257-268` (the name at
  `:266`).
  Evidence: `ProviderRequest` has no `Tools` field (`provider.go:48-61`);
  `rg -n 'TestHTTPProviderSendsToolsAndParsesAToolCall' backend` matches only the comment
  line; the block is executed by `TestHTTPProviderAsksForTheSchema` (`:269`). Safe: comments.

- [ ] Alias the vendored package to `piAI` where it collides with the local `piai`
  subpackage.
  Files: `provider/course.go:5`, `jobs/jobs_test.go:16`.
  Evidence: `rg -n 'sky-valley/pi/ai"' backend` shows `piai` aliasing the vendored package at
  `course.go:5` and `jobs/jobs_test.go:16`, while `provider/piai` is a package named `piai`.
  The report cited `jobs_test.go:18`; the alias is on line 16. Hygiene only.

## Amended

- [ ] Delete the dead reply surface (Delete cases 2-5, one edit): `ProviderResponse.Provider`,
  `.Model`, `.TokensIn`, `.TokensOut` (`provider.go:70-73`); `httpChatResponse.Usage`
  (`http_provider.go:122-125`); `Choices[].FinishReason` (`http_provider.go:120`).
  Direction is right; the writer lists are incomplete.
  What changed: `Provider`/`Model` are also written at `backend/internal/ai/course_agent_test.go:54`
  and `backend/internal/ai/helpers_test.go:331` (both `return provider.ProviderResponse{...}`),
  and `TokensIn`/`TokensOut` are also written at `backend/internal/ai/fake.go:106-107`
  (`len(req.Prompt)/4`). The report counted only `pi_ai.go:164-167`, `http_provider.go:229-232`
  and `fake.go:104-105`. Files to touch so it compiles: `provider.go`;
  `http_provider.go:120,122-125,229-232`; `piai/pi_ai.go:164-167`; `fake.go:104-107`;
  `provider/http_provider_test.go:57,60-61`; `provider/piai/pi_ai_test.go:91,121-122` (rename
  `TestCompleteCarriesRepairContextAndTokens` at `:96`); `course_agent_test.go:54`;
  `helpers_test.go:331`. The `usage` fixtures at `http_provider_test.go:30` and the
  `finish_reason` at `:299` stay harmless.
  Evidence: `rg -n 'TokensIn|TokensOut' . --glob '!docs/**'` -> writers `fake.go:106-107`,
  `http_provider.go:231-232`, `pi_ai.go:166-167`; readers `http_provider_test.go:60-61`,
  `pi_ai_test.go:121-122` only. `rg -n 'resp\.Provider|resp\.Model' backend` ->
  `piai_test.go:91`, `http_provider_test.go:57` only; `rg -n 'FinishReason' .` ->
  `http_provider.go:120` plus `progress.md`. Every field's only reader is a test;
  `ProviderResponse` is never marshalled.

- [ ] Collapse the run kind to one field — the derivation remedy must fix the test helper.
  What changed: the code proof stands (`jobs.go:230-232` overwrites `Request.Kind`;
  `GenerateChallenge`/`GenerateCourse` overwrite it again at `api.go:326,332`; the HTTP path
  sets both at `httpapi/ai.go:188,263`; the continuation sets only `Kind` at `:635`). But
  `jobs_test.go` submits `Kind:"course"` with `courseRequest()` whose `Kind` is empty
  (`jobs_test.go:177,206,272,302,331,375`). Deriving kind from `Request.Kind` silently flips
  those five tests to `challenge`. The remedy must set `Kind:"course"` on `courseRequest()`
  (or keep `Submission.Kind`) in the same change; name `jobs_test.go:177` and its five call
  sites. Until then, delete the redundant `req.Kind = kind` line only.

- [ ] `jobs.Job` vs `jobs.JobView` is a confirmed duplicate, but the wave-1 remedy is too
  broad.
  What changed: the ack repeats three fields of the view and always says `status:"running"`
  (`jobs.go:238`; `Job{ID,Kind,Status}` `:49-53`). The client reads both shapes
  (`web/src/api/ai.ts:643-646` `asJobHandle`, `:661-681` `asJob`) and OpenAPI documents them
  as `AIJobHandle` (`apidocs.go:1800-1813`) and `AIJob` (`:1780-1821`). "Return the view from
  `Submit` and delete `Job`" changes the response body, the documented `AIJobHandle` schema
  and the client in one step. Safer, still one truth: drop `Job.Status` (it is a constant),
  reducing the ack to `{id, kind}`; do it with the client change. Not backend-only.

- [ ] Keyless local server: the contradiction is real, but the remedy is a product decision.
  What changed: the code facts hold — `http_provider.go:73-75` says a key is
  `MissingCredential`'s job "because a local server needs no key", `api.go:293-300` repeats
  it, `MissingCredential` returns true for an empty key (`http_provider.go:92-94`), and
  `Complete` refuses an empty key (`:148-150`); `admin_ai.go:95-96` reads
  `KeySet: !mc.MissingCredential()`. Which mode the gateway wants is not provable from code.
  If keyless is intended: skip the `Authorization` header when the key is empty
  (`http_provider.go:187-188`) and stop reporting it missing. If a key is required: keep the
  current code and correct the two comments. Needs an owner call; it cannot be a confirmed
  action.

## Dropped

- Prompt-rendering split into `prompt.go` (`UserContent`, `ClipRunes`, `maxPreviousContextChars`,
  `maxFailureChars`, redaction helpers at `http_provider.go:237-296`): no counter-evidence,
  but the report itself says "Not required at 321 lines", so there is no action to verify.

## New

- `PIAI.Complete` never sets `ProviderResponse.LatencyMS`, so every pi-ai transcript entry
  records `0` ms while the HTTP and fake providers record real latency.
  Files: `provider/piai/pi_ai.go:163-168` (no `LatencyMS`), against
  `http_provider.go:233` and `fake.go:108` (both set it); the live reader is
  `transcript/transcript.go:266` (`r.record(..., resp.LatencyMS)`).
  Evidence: `rg -n 'LatencyMS' backend --include='*.go'` -> field at `provider.go:74`, two
  writers, one reader; pi-ai is absent.
  Remedy: set `LatencyMS: float64(time.Since(start).Microseconds())/1000` in `PIAI.Complete`,
  or delete the field and stop storing `MS`. One truth either way; today one provider leaves
  a field blank that a consumer prints.

## Still uncertain

- `ProviderDescriptor.Version`: reading settles the fact, not the decision. `pipeline.go:796`
  reads it; `HTTPProvider` and `FakeProvider` hardcode `"1"` (`http_provider.go:83`,
  `fake.go:44`); `PIAI.Descriptor` never sets it (`pi_ai.go:126-130`), so a pi-ai run stores
  an empty `ProviderVersion`. Owner chooses: keep and populate, or drop from provenance.
- Nil-receiver guards on `*HTTPProvider` (`Descriptor` `:77-79`, `MissingCredential` `:93`,
  `redact` `:310`): code evidence says unreachable in-repo — `NewHTTPProvider`
  (`http_provider.go:63`) is the only constructor and never returns nil, and
  `rg -n '\(\*HTTPProvider\)\(nil\)' backend` finds nothing. A runtime proof is only needed
  if an out-of-repo caller can form a typed nil. Cheap guards; keep unless that caller exists.
- Keyless local server support (Amended above): a product call, not a code proof.
