import { useCallback, useEffect, useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import type { DiagnosticsReport } from '../../types';
import type { AiCourseRecord } from '../../api/aiContract';

/*
 * The manage surface: the administrator's inventory and the gateway's own
 * health, in one screen.
 *
 * It exists because a generated course a run did not finish was otherwise
 * unreachable: the review screen is fed by the run that produced it, the
 * library lists the learner's own records only, and the health data
 * (/diagnostics) had no face at all. Everything here is read from the gateway
 * through the seam — GET /admin/courses and GET /diagnostics — never
 * fabricated client-side; the routes refuse a learner with 403.
 */

const STATUS_LABELS: Record<string, string> = {
  active: 'active',
  building: 'building',
  awaiting_approval: 'awaiting approval',
  rejected: 'rejected',
  failed: 'failed',
};

function statusLabel(status: string): string {
  return STATUS_LABELS[status] ?? status;
}

export default function ManageScreen() {
  const s = useStore();
  const isAdmin = s.role === 'admin';
  const onGateway = s.api.mode === 'http';
  /* COURSES is the mutable catalog the store splices gateway records into, so
     the built-in list filters to what actually ships with the app. */
  const builtIn = COURSES.filter((c) => !c.gateway);

  const [diagnostics, setDiagnostics] = useState<DiagnosticsReport | null>(null);
  const [records, setRecords] = useState<AiCourseRecord[] | null>(null);
  const [loadError, setLoadError] = useState<string | null>(null);
  const [busyId, setBusyId] = useState<string | null>(null);

  const load = useCallback(() => {
    if (!onGateway || !isAdmin) return;
    let alive = true;
    setLoadError(null);
    void s.api.diagnostics().then((d) => { if (alive) setDiagnostics(d); });
    void s.api.adminCourses()
      .then((list) => { if (alive) { setRecords(list); setLoadError(null); } })
      .catch((e) => { if (alive) setLoadError(e instanceof Error ? e.message : 'The inventory could not be read.'); });
    return () => { alive = false; };
  }, [onGateway, isAdmin, s.api]);

  useEffect(() => load(), [load]);

  if (!isAdmin) {
    return (
      <div className="content">
        <div className="card pad">
          <p>This surface is for course administrators.</p>
        </div>
      </div>
    );
  }

  async function resume(record: AiCourseRecord) {
    setBusyId(record.plan.id);
    try {
      const answer = await s.api.approveCourse(record.plan.id);
      if (answer.job) {
        /* The run breadcrumb is the store's own resume mechanism: writing it
           and landing on Home sends the run back to the building screen, the
           same path a reload mid-build takes. */
        s.runStarted(answer.job.id, record.plan.title);
        s.toast('Build resumed; watching the run.');
        s.showScreen('home');
      } else {
        s.toast('The build finished in the blocking call; refresh to see it.');
        load();
      }
    } catch (e) {
      s.toast(e instanceof Error && e.message ? e.message : 'The build could not be resumed.');
    } finally {
      setBusyId(null);
    }
  }

  async function remove(record: AiCourseRecord) {
    if (record.owner_id !== s.session?.user.id) return;
    setBusyId(record.plan.id);
    try {
      await s.api.deleteCourse(record.plan.id);
      s.toast('Course removed.');
      load();
    } catch (e) {
      s.toast(e instanceof Error && e.message ? e.message : 'The course could not be removed.');
    } finally {
      setBusyId(null);
    }
  }

  return (
    <div className="content">
      <div className="pagehead">
        <h1>Manage</h1>
        <p>
          Every stored course, every owner, and the gateway's own readiness.
          A learner is refused here with 403; the role never comes from the client.
        </p>
      </div>

      {!onGateway && (
        <div className="card pad authhint">
          <Icon name="info" />
          <p>
            This session runs in the browser, so there is no gateway to inventory
            or measure. The built-in courses below ship with the app; everything
            else needs the gateway.
          </p>
        </div>
      )}

      {onGateway && (
        <>
          <section className="card pad" aria-label="Gateway health">
            <h2>Gateway health</h2>
            {diagnostics === null ? (
              <p className="muted">The readiness report has not arrived yet.</p>
            ) : (
              <dl className="healthrows">
                <div className="healthrow"><dt>Build</dt><dd>{diagnostics.build || 'dev'}</dd></div>
                <div className="healthrow">
                  <dt>Engine</dt>
                  <dd>
                    {diagnostics.engine.name} {diagnostics.engine.version && `· ${diagnostics.engine.version}`}
                    {diagnostics.engine.ready ? ' · ready' : ` · not ready (${diagnostics.engine.reason})`}
                  </dd>
                </div>
                <div className="healthrow">
                  <dt>Limits</dt>
                  <dd>
                    {diagnostics.engine.limits.max_rows} rows · {diagnostics.engine.limits.max_bytes} bytes ·
                    {' '}{diagnostics.engine.limits.timeout_ms} ms · {diagnostics.engine.limits.max_concurrent} concurrent
                  </dd>
                </div>
                <div className="healthrow">
                  <dt>Store</dt>
                  <dd>{diagnostics.store.kind}{diagnostics.store.persistent ? ' · persistent' : ' · in memory'}</dd>
                </div>
                <div className="healthrow"><dt>Dataset</dt><dd>{diagnostics.dataset.schema} v{diagnostics.dataset.version}</dd></div>
                <div className="healthrow"><dt>Progress model</dt><dd>v{diagnostics.progress.version}</dd></div>
              </dl>
            )}
          </section>

          <section className="card pad" aria-label="Stored courses">
            <h2>Stored courses</h2>
            {loadError && <p className="authhint">{loadError}</p>}
            {records === null && !loadError && <p className="muted">Reading the inventory…</p>}
            {records !== null && records.length === 0 && (
              <p className="muted">
                No stored courses yet. Generate one with
                {' '}<button className="linklike" onClick={() => s.showScreen('create')}>Create a course</button>.
              </p>
            )}
            {records !== null && records.length > 0 && (
              <div className="inventory">
                {records.map((rec) => {
                  const mine = rec.owner_id === s.session?.user.id;
                  const busy = busyId === rec.plan.id;
                  const openable = rec.status === 'active';
                  const resumable = rec.status === 'awaiting_approval' || rec.status === 'building';
                  return (
                    <div className="inventoryrow" key={`${rec.plan.id}-${rec.created_at}`}>
                      <div className="inventorymain">
                        <strong>{rec.plan.title || rec.plan.id}</strong>
                        <span className="muted">
                          {statusLabel(rec.status)} · {rec.owner_id?.slice(0, 8)}
                          {rec.owner_id === s.session?.user.id ? ' (you)' : ''} · {new Date(rec.created_at).toLocaleDateString()}
                        </span>
                      </div>
                      <div className="inventoryactions">
                        {openable && (
                          <button className="small" disabled={busy} onClick={() => s.openLessonIn(rec.plan.id, 0)}>
                            Open
                          </button>
                        )}
                        {resumable && (
                          <button className="small" disabled={busy} onClick={() => void resume(rec)}>
                            {busy ? 'Resuming…' : 'Resume build'}
                          </button>
                        )}
                        {mine && !busy && (
                          <button className="small ghost" onClick={() => void remove(rec)}>
                            Remove
                          </button>
                        )}
                      </div>
                    </div>
                  );
                })}
              </div>
            )}
            <p className="muted">
              The detail routes stay owner-scoped: a course that is not yours is
              404 there, so an id still cannot be probed for existence.
            </p>
          </section>
        </>
      )}

      <section className="card pad" aria-label="Built-in courses">
        <h2>Built-in courses</h2>
        <p className="muted">{builtIn.length} courses ship with the app; their chapters are the authored markdown bundle.</p>
        <div className="inventory">
          {builtIn.map((c) => (
            <div className="inventoryrow" key={c.id}>
              <div className="inventorymain">
                <strong>{c.title}</strong>
                <span className="muted">{(c.modules || []).length} chapters · {c.dialect || 'postgresql'}</span>
              </div>
              <div className="inventoryactions">
                <button className="small ghost" onClick={() => s.openLessonIn(c.id, 0)}>View</button>
              </div>
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}