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

/** Extra wall clock beyond the advertised budget, so the pipeline's own answer
    always wins the race against the client giving up. */
export const AI_DEADLINE_SLACK_MS = 30_000;

/** Until the gateway has said how long a run may take, wait long enough that a
    slow-but-working run is never mistaken for an unreachable gateway. */
export const AI_DEADLINE_FALLBACK_MS = 10 * 60_000;

/** advertisedRunBudgetMs reads the AI run budget out of a /capabilities body.
    Not every capabilities read asks for it, and an older gateway does not send
    it, so a missing or nonsensical value is `null` and the caller keeps the
    last known budget in place. */
export function advertisedRunBudgetMs(body: unknown): number | null {
  if (typeof body !== 'object' || body === null) return null;
  const ai = (body as { ai?: unknown }).ai;
  if (typeof ai !== 'object' || ai === null) return null;
  const ms = (ai as { timeout_ms?: unknown }).timeout_ms;
  return typeof ms === 'number' && Number.isFinite(ms) && ms > 0 ? ms : null;
}

/** providerDeadlineMs is the deadline a provider-backed call gets. It is
    strictly longer than the budget the gateway enforces, which is the property
    that makes "still working" distinguishable from "unreachable". */
export function providerDeadlineMs(advertised: number | null): number {
  return advertised === null || advertised <= 0 ? AI_DEADLINE_FALLBACK_MS : advertised + AI_DEADLINE_SLACK_MS;
}
