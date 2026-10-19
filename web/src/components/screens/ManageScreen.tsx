import { useCallback, useEffect, useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import type { DiagnosticsReport } from '../../types';
import type { AdminAIConfig, AdminAIModelEntry, AiCourseRecord, AiRunSummary } from '../../api/aiContract';

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

const STATUS_LABELS = new Map<string, string>([
  ['active', 'active'],
  ['building', 'building'],
  ['awaiting_approval', 'awaiting approval'],
  ['rejected', 'rejected'],
  ['failed', 'failed'],
]);

function statusLabel(status: string): string {
  return STATUS_LABELS.get(status) ?? status;
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
  const [runs, setRuns] = useState<AiRunSummary[] | null>(null);
  const [runsError, setRunsError] = useState<string | null>(null);

  const [ai, setAi] = useState<AdminAIConfig | null>(null);
  const [aiForm, setAiForm] = useState<{ provider: 'off' | 'fake' | 'openai-compatible' | 'pi-ai'; base_url: string; model: string; key: string; timeout_ms: string }>({
    provider: 'fake',
    base_url: '',
    model: '',
    key: '',
    timeout_ms: '',
  });
  const [aiError, setAiError] = useState<string | null>(null);
  const [aiBusy, setAiBusy] = useState(false);
  const [catalog, setCatalog] = useState<AdminAIModelEntry[] | null>(null);

  const applyAi = useCallback((cfg: AdminAIConfig) => {
    setAi(cfg);
    setAiForm({
      provider: cfg.provider,
      base_url: cfg.base_url ?? '',
      model: cfg.model ?? '',
      key: '',
      timeout_ms: cfg.timeout_ms ? String(cfg.timeout_ms) : '',
    });
  }, []);

  const load = useCallback(() => {
    if (!onGateway || !isAdmin) return;
    let alive = true;
    setLoadError(null);
    void s.api.diagnostics().then((d) => { if (alive) setDiagnostics(d); });
    void s.api.adminCourses()
      .then((list) => { if (alive) { setRecords(list); setLoadError(null); } })
      .catch((e) => { if (alive) setLoadError(e instanceof Error ? e.message : 'The inventory could not be read.'); });
    void s.api.adminAI()
      .then((cfg) => { if (alive) { setAiError(null); applyAi(cfg); } })
      .catch((e) => { if (alive) setAiError(e instanceof Error ? e.message : 'The AI configuration could not be read.'); });
    void s.api.adminAIModels()
      .then((list) => { if (alive) setCatalog(list.length > 0 ? list : null); })
      .catch(() => { if (alive) setCatalog(null); });
    setRunsError(null);
    void s.api.listRuns()
      .then((list) => { if (alive) setRuns(list); })
      .catch((e) => { if (alive) setRunsError(e instanceof Error && e.message ? e.message : 'The run list could not be read.'); });
    return () => { alive = false; };
  }, [onGateway, isAdmin, s.api, applyAi]);

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

  async function saveAI() {
    setAiBusy(true);
    setAiError(null);
    const timeout = aiForm.timeout_ms.trim() === '' ? undefined : Number(aiForm.timeout_ms);
    if (timeout !== undefined && (!Number.isFinite(timeout) || timeout < 0)) {
      setAiError('timeout_ms must be empty (for the shipped default) or a non-negative number.');
      setAiBusy(false);
      return;
    }
    try {
      const cfg = await s.api.saveAdminAI({
        provider: aiForm.provider,
        base_url: aiForm.provider === 'openai-compatible' || aiForm.provider === 'pi-ai' ? aiForm.base_url.trim() || undefined : undefined,
        model: aiForm.provider === 'off' ? undefined : aiForm.model.trim(),
        key: aiForm.key.trim() === '' ? undefined : aiForm.key.trim(),
        timeout_ms: timeout,
      });
      applyAi(cfg);
      s.toast('AI configuration saved and applied.');
      load();
    } catch (e) {
      setAiError(e instanceof Error && e.message ? e.message : 'The AI configuration could not be saved.');
    } finally {
      setAiBusy(false);
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

          <section className="card pad" aria-label="AI provider">
            <h2>AI provider</h2>
            {ai === null && !aiError && <p className="muted">Reading the configuration…</p>}
            {aiError && <p className="authhint">{aiError}</p>}
            {ai !== null && (() => {
              const mode = aiForm.provider;
              return (
              <form
                className="aiform"
                onSubmit={(e) => { e.preventDefault(); void saveAI(); }}
              >
                <p className="muted">
                  Live configuration, source: <strong>{ai.source === 'database' ? 'saved here' : 'the config file'}</strong>.
                  The key never travels back in a body; leave the field empty to keep the stored one.
                </p>
                <div className="airow">
                  <label htmlFor="ai-provider">Provider</label>
                  <select id="ai-provider" value={aiForm.provider} onChange={(e) => setAiForm({ ...aiForm, provider: e.target.value as typeof aiForm.provider })}>
                    <option value="fake">fake — deterministic, no network</option>
                    <option value="openai-compatible">openai-compatible</option>
                    <option value="pi-ai">pi-ai — any catalog provider (Anthropic, OpenAI, Gemini, …)</option>
                    <option value="off">off</option>
                  </select>
                </div>
                {(mode === 'openai-compatible' || mode === 'pi-ai') && (
                  <>
                    <div className="airow">
                      <label htmlFor="ai-base">{mode === 'pi-ai' ? 'Base URL (optional)' : 'Base URL'}</label>
                      <input id="ai-base" value={aiForm.base_url} onChange={(e) => setAiForm({ ...aiForm, base_url: e.target.value })} placeholder={mode === 'pi-ai' ? 'Custom OpenAI-compatible server; empty uses the catalog' : 'https://llm.internal.example/v1'} />
                    </div>
                    <div className="airow">
                      <label htmlFor="ai-model">Model</label>
                      <input id="ai-model" value={aiForm.model} onChange={(e) => setAiForm({ ...aiForm, model: e.target.value })} list={mode === 'pi-ai' && catalog ? 'ai-model-catalog' : undefined} placeholder={mode === 'pi-ai' ? 'provider/model, e.g. anthropic/claude-sonnet-4-5' : 'inhouse-4'} />
                      {mode === 'pi-ai' && catalog && (
                        <datalist id="ai-model-catalog">
                          {catalog.map((c) => (
                            <option key={`${c.provider}/${c.model}`} value={`${c.provider}/${c.model}`}>
                              {c.name}
                            </option>
                          ))}
                        </datalist>
                      )}
                    </div>
                    <div className="airow">
                      <label htmlFor="ai-key">API key</label>
                      <input id="ai-key" type="password" value={aiForm.key} onChange={(e) => setAiForm({ ...aiForm, key: e.target.value })} placeholder={ai.key_set ? 'Stored — leave empty to keep it' : 'Not set'} />
                    </div>
                  </>
                )}
                <div className="airow">
                  <label htmlFor="ai-timeout">Call timeout (ms)</label>
                  <input id="ai-timeout" inputMode="numeric" value={aiForm.timeout_ms} onChange={(e) => setAiForm({ ...aiForm, timeout_ms: e.target.value })} placeholder={String(ai.timeout_ms || 120000)} />
                </div>
                <div className="airow">
                  <label htmlFor="ai-current">Current</label>
                  <span id="ai-current" className="muted">
                    {ai.provider === 'off' ? 'off' : `${ai.provider}${ai.model ? ` · ${ai.model}` : ''}${ai.source === 'database' ? ' · saved here' : ' · from the config file'}`}
                  </span>
                </div>
                <div className="airow">
                  {/* A grid spacer, not a label: it holds the label column so the
                      button lines up with the fields above. */}
                  <span aria-hidden="true" />
                  <button className="primary" type="submit" disabled={aiBusy}>
                    {aiBusy ? 'Applying…' : 'Save and apply'}
                  </button>
                </div>
                <p className="muted">
                  Saving applies to requests and jobs submitted afterwards; a run
                  already in flight keeps the provider it started with. The
                  setting survives a restart because the boot path applies it.
                </p>
              </form>
              );
            })()}
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

          <section className="card pad" aria-label="Generation runs">
            <div className="sectiontitle">
              <h2>Generation runs</h2>
              <button className="small ghost" onClick={() => s.showScreen('runs')}>
                <Icon name="list" />All runs
              </button>
            </div>
            {runsError && <p className="authhint">{runsError}</p>}
            {!runsError && runs === null && <p className="muted">Reading the run list…</p>}
            {!runsError && runs !== null && runs.length === 0 && (
              <p className="muted">No generation runs recorded yet.</p>
            )}
            {!runsError && runs !== null && runs.length > 0 && (
              <div className="inventory">
                {runs.slice(0, 5).map((run) => (
                  <div className="inventoryrow" key={run.id}>
                    <div className="inventorymain">
                      <strong>{run.kind} run · {run.id}</strong>
                      <span className="muted">
                        {statusLabel(run.status)} · {run.entry_count} {run.entry_count === 1 ? 'entry' : 'entries'} · {new Date(run.started_at).toLocaleDateString()}
                        {run.truncated ? ' · truncated' : ''}
                      </span>
                    </div>
                    <div className="inventoryactions">
                      <button className="small ghost" onClick={() => s.showScreen('runs')}>Open</button>
                    </div>
                  </div>
                ))}
              </div>
            )}
            <p className="muted">
              The gateway records a transcript per generation run and scopes the
              list to your session; another account's run never appears here.
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