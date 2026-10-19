import { useCallback, useEffect, useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { GatewayError } from '../../api/errors';
import type { AiRunEntry, AiRunSummary, AiRunTranscript } from '../../api/aiContract';

/*
 * The agent-run audit trail: every generation run the signed-in account left,
 * newest first, and the whole stored transcript of any one of them.
 *
 * The gateway records a redacted, bounded transcript per run and scopes every
 * read to the session, so this screen only ever shows the caller's own runs.
 * Nothing here is fabricated: a run the gateway does not return is named as
 * missing, and an unreachable list is an error, never "you have no runs".
 * Read-only by design: a transcript is evidence, not an editable record.
 */

/** The run's own status word, with the one multi-word state spelled out. */
function statusLabel(status: string): string {
  return status === 'awaiting_approval' ? 'awaiting approval' : status;
}

function when(iso: string): string {
  const t = Date.parse(iso);
  return Number.isFinite(t) ? new Date(t).toLocaleString() : 'unknown time';
}

function bytesLabel(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

/** What one ordered fact of a run is, in the reader's words. A message names
    its role; a tool call names the function the model asked for. */
function entryLabel(entry: AiRunEntry): string {
  if (entry.kind === 'tool_call') return entry.tool_name ? `tool call — ${entry.tool_name}` : 'tool call';
  if (entry.kind === 'tool_result') return 'tool result';
  if (entry.kind === 'error') return 'error';
  return entry.role || 'message';
}

/** What a failed read carries: the gateway's own code and message when it gave
    one, a plain fallback otherwise. */
interface RunFailure {
  message: string;
  code: string;
}

/** The one place a thrown read is turned into something to show. */
function failureOf(e: Error, fallback: string): RunFailure {
  if (e instanceof GatewayError) return { message: e.message, code: e.code };
  return { message: e.message || fallback, code: '' };
}

/** One run's whole transcript: the facts that attribute it, then its entries
    in the order the run wrote them. A clipped or scrubbed entry says so. */
function Transcript({ transcript }: { transcript: AiRunTranscript }) {
  const stored = transcript.entries.length;
  return (
    <>
      <div className="sectiontitle">
        <h2 className="mono" style={{ fontSize: 13 }}>{transcript.id}</h2>
        <span className="pill subtle">{statusLabel(transcript.status)}</span>
      </div>
      <dl className="healthrows">
        <div className="healthrow"><dt>Kind</dt><dd>{transcript.kind}</dd></div>
        <div className="healthrow"><dt>Started</dt><dd>{when(transcript.started_at)}</dd></div>
        <div className="healthrow"><dt>Finished</dt><dd>{when(transcript.finished_at)}</dd></div>
        <div className="healthrow"><dt>Entries</dt><dd>{transcript.entry_count} produced · {stored} stored</dd></div>
        <div className="healthrow"><dt>Size</dt><dd>{bytesLabel(transcript.bytes)}</dd></div>
        {transcript.error && (
          <div className="healthrow"><dt>Error</dt><dd>{transcript.error.code}: {transcript.error.message}</dd></div>
        )}
      </dl>
      {transcript.truncated && (
        <p className="muted mt-8">This transcript is a prefix: a bound was reached, so the run produced more than is stored here.</p>
      )}
      {stored === 0 ? (
        <p className="muted mt-8">No entries were stored for this run.</p>
      ) : (
        <div className="inventory mt-8">
          {transcript.entries.map((entry) => (
            <div
              className="inventoryrow"
              key={`${entry.seq}-${entry.kind}`}
              style={{ flexDirection: 'column', alignItems: 'stretch', gap: 4 }}
            >
              <div className="inventorymain">
                <strong>{entryLabel(entry)}</strong>
                <span className="muted">
                  seq {entry.seq} · {entry.kind}
                  {entry.tool_call_id ? ` · ${entry.tool_call_id}` : ''}
                  {entry.ms != null ? ` · ${Math.round(entry.ms)} ms` : ''}
                </span>
              </div>
              {entry.content
                ? <pre className="mono" style={{ margin: 0, whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: 12, color: 'var(--text-2)' }}>{entry.content}</pre>
                : <span className="muted">(no content)</span>}
              {(entry.truncated || entry.redacted) && (
                <span className="muted">
                  {entry.truncated ? 'content clipped to fit' : ''}
                  {entry.truncated && entry.redacted ? ' · ' : ''}
                  {entry.redacted ? 'credential redacted' : ''}
                </span>
              )}
            </div>
          ))}
        </div>
      )}
    </>
  );
}

export default function RunsScreen() {
  const s = useStore();
  const onGateway = s.api.mode === 'http';

  const [runs, setRuns] = useState<AiRunSummary[] | null>(null);
  const [listError, setListError] = useState<{ message: string; code: string } | null>(null);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [transcript, setTranscript] = useState<AiRunTranscript | null>(null);
  const [detailError, setDetailError] = useState<{ message: string; code: string } | null>(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const load = useCallback(() => {
    if (!onGateway) return;
    let alive = true;
    setListError(null);
    s.api
      .listRuns()
      .then((list) => { if (alive) setRuns(list); })
      .catch((e) => { if (alive) setListError(failureOf(e, 'The run list could not be read.')); });
    return () => { alive = false; };
  }, [onGateway, s.api]);

  useEffect(() => load(), [load]);

  const open = useCallback(
    (id: string) => {
      setSelectedId(id);
      setTranscript(null);
      setDetailError(null);
      setDetailLoading(true);
      s.api
        .getRun(id)
        .then((rec) => {
          setTranscript(rec);
          if (!rec) setDetailError({ message: `Run ${id} is not there, or it belongs to another account.`, code: 'NOT_FOUND' });
        })
        .catch((e) => setDetailError(failureOf(e, 'The transcript could not be read.')))
        .finally(() => setDetailLoading(false));
    },
    [s.api],
  );

  const toggle = useCallback(
    (id: string) => {
      if (selectedId === id) {
        setSelectedId(null);
        setTranscript(null);
        setDetailError(null);
        return;
      }
      open(id);
    },
    [selectedId, open],
  );

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Manage · audit trail</div>
          <h1>Generation runs</h1>
          <p>
            Every generation run this account left, newest first. The gateway
            records a redacted, bounded transcript per run and scopes the list to
            your session, so another account's run never appears here.
          </p>
        </div>
        <button className="small ghost" onClick={() => s.showScreen('manage')}>
          <Icon name="arrow-left" />Manage
        </button>
      </div>

      <section className="card pad" aria-label="Generation runs">
        {!onGateway && (
          <p className="authhint">
            <Icon name="info" />
            Generation runs are recorded on the gateway. This session runs in the
            browser, so there is no run store to read.
          </p>
        )}

        {onGateway && listError && (
          <>
            <p className="authhint" role="alert">
              {listError.code === 'AI_UNAVAILABLE'
                ? `Generation runs need the AI service, and this gateway reports it off. ${listError.message}`
                : `The run list could not be read. ${listError.message}`}
            </p>
            <button className="small ghost mt-8" onClick={() => load()}>
              <Icon name="refresh" />Retry
            </button>
          </>
        )}

        {onGateway && !listError && runs === null && (
          <p className="muted">Reading the run list…</p>
        )}

        {onGateway && !listError && runs !== null && runs.length === 0 && (
          <p className="muted">
            No runs recorded yet. Generate a course with
            {' '}<button className="linklike" onClick={() => s.showScreen('create')}>Create a course</button>.
          </p>
        )}

        {onGateway && !listError && runs !== null && runs.length > 0 && (
          <div className="inventory">
            {runs.map((run) => (
              <div key={run.id}>
                <div className="inventoryrow">
                  <div className="inventorymain">
                    <strong>{run.kind} run · {run.id}</strong>
                    <span className="muted">
                      {statusLabel(run.status)} · {run.entry_count} {run.entry_count === 1 ? 'entry' : 'entries'} · {bytesLabel(run.bytes)}
                      {run.truncated ? ' · truncated' : ''} · {when(run.started_at)} → {when(run.finished_at)}
                    </span>
                    {run.error && <span className="muted">{run.error.code}: {run.error.message}</span>}
                  </div>
                  <div className="inventoryactions">
                    <button
                      className="small"
                      aria-expanded={selectedId === run.id}
                      onClick={() => toggle(run.id)}
                    >
                      {selectedId === run.id ? 'Close' : 'Open'}
                    </button>
                  </div>
                </div>
                {selectedId === run.id && (
                  <div className="card pad" style={{ marginTop: 6 }} aria-label={`Run ${run.id} transcript`}>
                    {detailLoading && <p className="muted">Reading the transcript…</p>}
                    {!detailLoading && detailError && (
                      <p className="authhint" role="alert">{detailError.message}</p>
                    )}
                    {!detailLoading && !detailError && transcript && <Transcript transcript={transcript} />}
                  </div>
                )}
              </div>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}
