# ADR 0005: A provider-neutral completion interface, not a vendor SDK

- **Status:** accepted
- **Date:** 2026-10-15
- **Deciders:** project owner + agent session
- **Question:** what does the app depend on to call a model, a vendor SDK, or an
  interface it owns, and what provider is the default when nothing is
  configured?

Settles **AI provider and SDK** in `docs/technical-design.md` §15. Shaped by
§11.2 (*"the SDK is chosen after a spike against a provider-neutral interface, not
by preference"*) and by the §12 threat table row *Provider key leak*.

## Context

§11 splits the AI Challenge Lab into two roles: the model is an untrusted content
generator, and the Go service is the policy. M4 step 1 already shipped the seam
that role split implies: `backend/internal/ai/provider.go`:

```go
type Provider interface {
    Descriptor() ProviderDescriptor
    Complete(ctx context.Context, req ProviderRequest) (ProviderResponse, error)
}
```

One completion in, text out, no tools and no filesystem reach. `ProviderRequest`
carries a `Kind`, a system prompt, a prompt, an output JSON Schema, a
`MaxOutputBytes` bound, and (for repair) the previous output and the failure.
It does **not** carry the reference solution, so tutoring cannot leak what it was
never given.

The spike compares two ways of satisfying that interface: depend on a vendor SDK,
or depend on the interface and own the transport. The decision is about *where
policy lives*, which is why the SDK question is not a packaging preference.

## Decision

1. **The app depends on `Provider`**, not on a vendor SDK. Everything vendor
   (auth, retries, request shaping, error types) lives *behind* the interface, in
   a transport that is reviewed as policy-adjacent code.
2. **Default transport: a thin HTTP client for an OpenAI-compatible
   `POST {base}/chat/completions`**, selected by base URL + model + key from
   config. One small code path covers hosted servers and local ones
   (llama.cpp- / Ollama-style) with no extra dependency.
3. **Default provider when nothing is configured: a deterministic built-in fake
   provider** that runs the real §11.1 pipeline with no network and no key, so
   generation, validation and the exit gate are testable in CI and the app is
   demonstrably usable with AI off.
4. **Keys are server-side only** and never reach the browser (§11.2; §12 *Provider
   key leak*: "Server-side storage, redacted logs").
5. **The provider is bounded by policy, not by a client library's defaults:**
   `Limits{MaxOutputBytes 64 KiB, MaxSetupStatements 8, MaxRepairs 2, MaxHints 3,
   MaxConcurrent 2, Timeout 20 s, MaxPromptChars 4000}`.

## Why a vendor SDK loses: the policy boundary

The §11.1 pipeline is a sequence of refusals, and every refusal has to be
inspectable in one place. A vendor SDK would absorb some of them into its own
types and defaults:

| Concern | Where a vendor SDK puts it | Where §11.2 wants it |
| --- | --- | --- |
| Request/response types | vendor structs and streaming callbacks | `ProviderRequest`/`ProviderResponse`: the fields policy is allowed to send |
| Retry / backoff | the SDK's defaults, often undocumented | `Limits`, applied by our pipeline |
| Key handling | an SDK client constructed wherever convenient | one server-side transport; no key in the browser |
| Output bounding | after decode, if at all | `MaxOutputBytes` enforced while reading the reply |
| Errors | vendor error taxonomy | `ErrProviderUnavailable` plus the AI envelope codes |
| Tools / filesystem | the SDK makes them available by proximity | the interface has none; a tool call routes through a normal service |

A vendor SDK also makes the second provider a second SDK. Because the interface
is a single completion, a second provider is a second implementation of two
methods, not a second dependency graph.

## Why the OpenAI-compatible shape, and why the fake is the default

The app needs text in, JSON out, over HTTP. That is the intersection every
*hosted* and *local* option here already speaks: the compatible
`/chat/completions` request is small enough to construct with `net/http`, and it
means a learner who points the app at a local server gets the same code path as
one who points it at a hosted one. The difference is a base URL, a model name and
a key.

The fake provider is the default because the exit gate's last clause is *"Disabling
AI leaves every other feature working."* If the only provider were a hosted one,
that clause would be trivially true (nothing could run) and the pipeline would be
untestable in CI. A deterministic provider that produces a real, schema-valid
artifact lets the whole §11.1 pipeline, including the scratch environment, the
reference run, the cross-check and the adversarial fixtures, run offline, and it
is the only provider the CI gate needs.

## Consequence: keys never leave the server

The browser talks to the gateway, never to the provider. `GET /capabilities`
reports whether a provider is *configured* and *available* and its name, never a
key, an endpoint or a model credential. This is the §12 control for *Provider key
leak*, and it is also why "let the client choose the provider" is not on the table
below: it would move credential handling and cost decisions into the untrusted
surface.

## Alternatives rejected

| Alternative | Concrete reason it lost |
| --- | --- |
| **A vendor SDK as the dependency** | Vendor types, retry policy and credential handling land inside the policy boundary; the interface grows vendor concepts; a second provider means a second SDK. §11.2 asks for the interface first. |
| **A provider-specific client per provider, now** | More code and more review surface before there is a second provider. The surface is one completion; every candidate speaks it. If shaping ever becomes load-bearing, adapters go *behind* `Provider` (see below). |
| **A local model only (llama.cpp) as the product's provider** | Ties the product to one machine and one hardware profile. The compatible HTTP path already covers local servers without making them mandatory. |
| **No default provider (AI always off)** | Makes the *"AI off"* clause vacuous and the pipeline untestable in CI. The fake provider runs the real pipeline with no network. |
| **Stream tokens to the browser** | Puts the vendor protocol, and eventually the key or a long-lived proxy, on the untrusted side, for a payload that is a JSON bundle, not prose. Not needed. |
| **Let the client pick provider and model** | Moves credential handling and cost control into the browser (§12), and makes `/capabilities`'s honest `available` flag meaningless. |

## Consequences

- The provider decision is code review's problem, not a library's: the entire
  model surface is two methods and a request struct, so a change in what the model
  may see is a diff in one file.
- The gateway needs one new piece of configuration (base URL, model, key) and one
  new HTTP client; it adds no dependency to `go.mod`.
- "AI off" is the default state, and it is exercised on every `make test` run.
- The bounds are a policy surface that is easy to audit and easy to get wrong:
  they are numbers in `Limits`, and changing a number changes product behaviour.
- A provider that is not compatible with `POST /chat/completions` needs a second
  transport, not a second architecture.

## What the spike measured

The spike is architectural, so its measurements are of the surface it is deciding
about, all read from the repository on 2026-10-15:

| Measurement | Value |
| --- | --- |
| Model surface the app owns | 2 methods (`Descriptor`, `Complete`) + a descriptor; `provider.go` is 51 lines |
| What the model may see | `ProviderRequest` fields only; the reference solution is structurally absent for generation, and for the tutor the expected result may appear only after an attempt while the reference query never does (ADR 0008) |
| Vendor SDK in the build | none, `backend/go.mod` has 4 direct deps, none an AI SDK |
| Marginal cost of the transport | 0 new modules; `net/http` and `encoding/json` from the standard library |
| Policy owned by us | 7 `Limits` fields + `ProviderRequest.MaxOutputBytes`, versus a library's retry/backoff defaults |
| Offline testability | the fake provider runs the entire §11.1 pipeline with no network and no key |

**Not measured:** end-to-end latency, output quality, token price and a hosted
provider's exact JSON. No key is configured in this environment by design. Those
numbers would sharpen the transport (timeouts, retry posture) but would not move
the decision: the decision is *where policy lives*, and under either option the
policy would live in code we review.

## What would change our mind

- **A provider whose API is not "text in, JSON out"**, server-side tools,
  long-running jobs, a streaming-only contract, would need a second interface or a
  second transport. `Provider` stays the policy-facing shape; the new work is
  behind it.
- **Provider-specific shaping becoming load-bearing for correctness** (structured
  output dialects, system-prompt precedence, strict JSON mode) is the case for a
  thin adapter per provider, still behind `Provider`, never in front of it.
- **Measured price or latency showing an SDK's retry/backoff materially matters**, 
  then the transport adopts a retry posture, set and bounded by our `Limits`, not
  by a library.
- **A vendor shipping a hard requirement we cannot express over `net/http`** (a
  signed request protocol, a required client attestation). That is a real
  constraint and would be revisited, not worked around.
- **A second provider arriving** that the compatible subset cannot serve. The
  interface is the thing being preserved; the transport is replaceable.

## What is not proven here

- No end-to-end measurement of a hosted provider: latency, cost and output quality
  are unmeasured until a key is configured.
- No claim that every "OpenAI-compatible" endpoint is compatible; the subset used
  is a `POST /chat/completions` request with a model, a JSON reply and a byte cap.
- **Residual risk, stated:** a provider can still return a contract-shaped
  mistake. The §11.1 cross-check catches some of those, not all, because two
  solutions can share a mistake: no interface choice removes that.

## Reopening note, 2026-10-17: the wire layer is adopted, not owned

The decision above held until the providers customers actually asked for went
past the OpenAI wire format. The 250-line client had no SSE streaming, no
Retry-After-aware retries, no token accounting and no native Anthropic or
Gemini support — and none of those primitives are this product's to own: they
are solved problems with tested implementations.

The wire layer is now `github.com/sky-valley/pi/ai` (the Go port of pi's
unified multi-provider LLM API), wrapped as one more `provider.Provider`
implementation in `internal/ai/provider/piai`. The seam is unchanged: two
methods, one completion in, text out. The pipeline, the repair rules, the
schema enforcement and the redaction stay in the gateway, because grading
policy is the product. The fake provider stays, so CI still proves the whole
pipeline offline. A custom OpenAI-compatible server (a model gateway, vLLM,
llama.cpp) is supported through the same adapter by naming a base URL instead
of a catalog entry.

Cost of the adoption, stated plainly: the backend's Go floor moves from 1.25
to 1.26 (the port declares `go 1.26`), and a fast-moving third-party module
now sits in the grading path — contained behind the seam, where a revert or a
replacement is one package.
