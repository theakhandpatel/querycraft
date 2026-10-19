/*
 * How long the transport waits on a provider-backed call.
 *
 * A generation run is bounded by the pipeline's own budget, which is minutes,
 * not seconds: a real course took 72 s across four provider calls plus one
 * repair. The transport's query deadline would abort that run while it was
 * still working, and because the aborted call was retried, the learner saw
 * three dead requests and "Could not reach the execution gateway" for a gateway
 * that was answering the whole time.
 *
 * The gateway advertises its budget as `ai.timeout_ms` on /capabilities and
 * hands the pipeline headroom over it, so the client waits for the budget plus
 * a slack. The pipeline then always reports its own honest failure first, and
 * a timeout on this side means something is genuinely wrong.
 *
 * Pure on purpose: no browser globals, no Effect, so an audit can bundle this
 * one file and pin the arithmetic.
 */

import type { Schema } from 'effect';

/** Extra wall clock beyond the advertised budget, so the pipeline's own answer
    always wins the race against the client giving up. */
const AI_DEADLINE_SLACK_MS = 30_000;

/** Until the gateway has said how long a run may take, wait long enough that a
    slow-but-working run is never mistaken for an unreachable gateway. */
export const AI_DEADLINE_FALLBACK_MS = 10 * 60_000;

/** A JSON object, as the capabilities body is before a schema reads it. */
function isJsonObject(value: Schema.Json): value is Schema.JsonObject {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

/** A usable millisecond budget. */
function isBudgetMs(value: Schema.Json | undefined): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0;
}

/** advertisedRunBudgetMs reads the AI run budget out of a /capabilities body.
    Not every capabilities read asks for it, and an older gateway does not send
    it, so a missing or nonsensical value is `null` and the caller keeps the
    last known budget in place. */
export function advertisedRunBudgetMs(body: Schema.Json): number | null {
  if (!isJsonObject(body)) return null;
  const ai = body.ai;
  if (!isJsonObject(ai)) return null;
  return isBudgetMs(ai.timeout_ms) ? ai.timeout_ms : null;
}

/** providerDeadlineMs is the deadline a provider-backed call gets. It is
    strictly longer than the budget the gateway enforces, which is the property
    that makes "still working" distinguishable from "unreachable". */
export function providerDeadlineMs(advertised: number | null): number {
  return advertised === null || advertised <= 0 ? AI_DEADLINE_FALLBACK_MS : advertised + AI_DEADLINE_SLACK_MS;
}
