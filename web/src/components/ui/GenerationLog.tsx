import { Fragment } from 'react';
import type { LogLine, LogSegment } from '../../lib/generationPoll';

/*
 * The generation stream: one paced line per entry, each with its own timestamp.
 *
 * Both the build screen and the challenge lab render the gateway's own
 * transcript under this markup, so the shape lives here once. A line is data
 * (`LogLine`), never HTML: the text and its two inline tones become spans
 * here, so no screen escapes markup by hand and nothing untrusted reaches
 * `dangerouslySetInnerHTML`.
 */
function isPlainSegment(seg: LogSegment): seg is string {
  return typeof seg === 'string';
}

function renderSegments(text: readonly LogSegment[]) {
  return text.map((seg, i) =>
    isPlainSegment(seg) ? (
      <Fragment key={i}>{seg}</Fragment>
    ) : (
      <span className={seg.tone} key={i}>{seg.text}</span>
    )
  );
}

export default function GenerationLog({ log, open }: { log: LogLine[]; open: boolean }) {
  return (
    <div className={`stream${open ? ' open' : ''}`}>
      {/* Append-only transcript: lines are pushed and never reordered. */}
      {log.map((l, i) => (
        <div className="ln" key={i}>
          <span className="t">{l.t}</span>
          <span>
            {l.glyph !== undefined && <span className={l.tone}>{l.glyph}</span>}
            {l.glyph !== undefined && ' '}
            {l.caret ? <span className="caret" /> : renderSegments(l.text)}
          </span>
        </div>
      ))}
    </div>
  );
}
