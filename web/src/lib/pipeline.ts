import type { PipelineEvent } from '../types';

interface PipelineOptions<TLine> {
  steps?: string[];
  lines?: readonly (readonly [string, TLine])[];
  stepMs: number;
  lineMs: number;
  startMs?: number;
}

/* Drives the "generation stream" UI. Steps and log lines are emitted over time so
   a loading state is real rather than an instant cut. The one caller runs under
   both adapters; no adapter replays this cadence on its own.

   Pass a `signal` to make the run cancellable: aborting clears the pending
   timers, suppresses `all-done`, and resolves `{ ok: false }`. Without this the
   two timer chains kept ticking after the learner cancelled the build and then
   fired `all-done` anyway, which yanked them back into the review screen. */
export function runPipeline<TLine>(
  opts: PipelineOptions<TLine>,
  onEvent: (e: PipelineEvent<TLine>) => void,
  signal?: AbortSignal
): Promise<{ ok: boolean }> {
  return new Promise((resolve) => {
    const steps = opts.steps || [];
    const lines = opts.lines || [];
    const timers = new Set<ReturnType<typeof setTimeout>>();
    let si = 0;
    let li = 0;
    let stepsDone = false;
    let linesDone = false;
    let settled = false;

    function later(fn: () => void, ms: number): void {
      if (settled || signal?.aborted) return;
      const id = setTimeout(() => {
        timers.delete(id);
        fn();
      }, ms);
      timers.add(id);
    }

    function finish(ok: boolean, emitAllDone: boolean): void {
      if (settled) return;
      settled = true;
      for (const id of timers) clearTimeout(id);
      timers.clear();
      signal?.removeEventListener('abort', onAbort);
      if (emitAllDone) onEvent({ type: 'all-done' });
      resolve({ ok });
    }

    function onAbort(): void {
      finish(false, false);
    }

    function maybeEnd(): void {
      if (stepsDone && linesDone) finish(true, true);
    }

    function emitStep(): void {
      if (settled) return;
      if (si > 0) onEvent({ type: 'step', index: si - 1, state: 'done' });
      if (si < steps.length) {
        onEvent({ type: 'step', index: si, state: 'running' });
        si++;
        later(emitStep, opts.stepMs);
      } else {
        stepsDone = true;
        maybeEnd();
      }
    }

    function emitLine(): void {
      if (settled) return;
      if (li < lines.length) {
        const line = lines[li];
        onEvent({ type: 'log', t: line[0], line: line[1] });
        li++;
        later(emitLine, opts.lineMs);
      } else {
        linesDone = true;
        maybeEnd();
      }
    }

    if (signal?.aborted) {
      finish(false, false);
      return;
    }
    signal?.addEventListener('abort', onAbort, { once: true });

    if (!steps.length) stepsDone = true;
    if (!lines.length) linesDone = true;
    later(() => {
      emitStep();
      emitLine();
    }, opts.startMs || 0);
    maybeEnd();
  });
}
