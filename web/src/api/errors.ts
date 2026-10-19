import { Data } from 'effect';

/* The typed failure every adapter can raise. It carries a stable contract code
   (see contracts/query-result.schema.json) so callers can branch on meaning
   rather than on transport mechanics. It lives in its own module because the
   AI seam (`./ai`) raises and inspects it without pulling in the browser
   engine that `./execution` also owns. */
export class GatewayError extends Data.TaggedError('GatewayError')<{
  readonly code: string;
  readonly message: string;
  readonly detail?: string | null;
  readonly status?: number;
}> {}

/** A failed call, normalised to the typed error channel. */
// oxlint-disable-next-line anti-slop/no-unknown-parameters -- a thrown or rejected value is untrusted; this is its normalise boundary.
export function asGatewayError(e: unknown): GatewayError {
  if (e instanceof GatewayError) return e;
  return new GatewayError({
    code: 'ENVIRONMENT_UNAVAILABLE',
    message: e instanceof Error ? e.message : 'The gateway response could not be read.',
  });
}

export function authFailure(code: string, message: string, status?: number): GatewayError {
  return new GatewayError({ code, message, status });
}
