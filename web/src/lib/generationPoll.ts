/**
 * The pure half of the generation-run UI.
 *
 * A log line is data, not markup: `{t, tone, glyph, text}` where `text` is a
 * list of strings and the two mid-line spans the screens use (`hi`, `mono`).
 * `GenerationLog` turns that into JSX, so no screen escapes HTML by hand and
 * nothing untrusted is ever handed to `dangerouslySetInnerHTML`.
 *
 * `poll` and `settlePhase` hold the two decisions the run makes that are worth
 * testing without a server: what a job view adds to the stream, and which
 * phase a settled outcome lands in. Both take their inputs as arguments and
 * return new values; neither reads a clock, a timer or the network.
 */

import type { AiJob, AiOutcome } from '../api/aiContract';

export type LogTone = 'ok' | 'fail' | 'hi' | 'mono';

/** One run of text inside a line. A bare string is plain; an object carries a
    tone, which `GenerationLog` renders as a class name. */
export type LogSegment = string | { text: string; tone: 'hi' | 'mono' };

export interface LogLine {
  t: string;
  /** The leading glyph's class: `ok` / `fail` / `hi`. */
  tone?: LogTone;
  /** The glyph or label that opens the line, in its own span. */
  glyph?: string;
  /** True for the empty caret line that closes a block of output. */
  caret?: boolean;
  text: readonly LogSegment[];
}

/** A line before the effect stamps it with the run's clock. */
export type LogLineDraft = Omit<LogLine, 't'>;

/* Reserved keys in the dedup map, so poll keeps one memory of what it has
   already printed for the chapter facts as well as the stages. A stage can
   never be named one of these, so the two never collide. */
const PLANNED = '\u0000planned';
const VERIFIED = '\u0000verified';
const CHAPTER = '\u0000chapter';

/** The lines one job view adds to the stream, and the memory to pass to the
    next call. A stage prints once per state change; the chapter facts print
    once each time the gateway changes them. */
export interface PollResult {
  lines: LogLineDraft[];
  seen: Map<string, string>;
}

export function poll(view: AiJob, seen: ReadonlyMap<string, string>): PollResult {
  const next = new Map(seen);
  const lines: LogLineDraft[] = [];

  for (const stage of view.stages) {
    if (next.get(stage.name) === stage.state) continue;
    next.set(stage.name, stage.state);
    const glyph =
      stage.state === 'done' ? '✓' : stage.state === 'failed' ? '✕' : stage.state === 'running' ? '→' : '–';
    const tone: LogTone = stage.state === 'done' ? 'ok' : stage.state === 'failed' ? 'fail' : 'hi';
    const ms = stage.ms !== undefined ? ` · ${stage.ms} ms` : '';
    lines.push({ tone, glyph, text: [`${stage.name}${ms}${stage.detail ? ` — ${stage.detail}` : ''}`] });
  }

  if (view.chapters_planned !== undefined && next.get(PLANNED) !== String(view.chapters_planned)) {
    next.set(PLANNED, String(view.chapters_planned));
    const n = view.chapters_planned;
    lines.push({ text: ['the outline plans ', { text: String(n), tone: 'hi' }, ` chapter${n === 1 ? '' : 's'}`] });
  }

  if (view.chapters_verified !== undefined && next.get(VERIFIED) !== String(view.chapters_verified)) {
    next.set(VERIFIED, String(view.chapters_verified));
    const n = view.chapters_verified;
    const of = view.chapters_planned !== undefined ? ` of ${view.chapters_planned}` : '';
    lines.push({ tone: 'ok', glyph: '✓', text: [`${n}${of} chapter${n === 1 ? '' : 's'} verified and stored`] });
  }

  if (view.current_chapter && next.get(CHAPTER) !== view.current_chapter) {
    next.set(CHAPTER, view.current_chapter);
    lines.push({ text: ['writing ', { text: view.current_chapter, tone: 'hi' }] });
  }

  return { lines, seen: next };
}

/** Which phase a settled outcome lands in. `awaiting` means the run paused on a
    stored outline; `finishing` means a course was activated; `failed` covers
    every refusal, including a pause whose outline did not come back. */
export function settlePhase(outcome: AiOutcome): 'awaiting' | 'finishing' | 'failed' {
  if (outcome.status === 'awaiting_approval') return outcome.plan ? 'awaiting' : 'failed';
  if (outcome.status === 'active' && outcome.plan) return 'finishing';
  return 'failed';
}
