import { useStore } from '../../state/store';
import Icon from '../ui/Icon';
import GenerationLog from '../ui/GenerationLog';
import { PIPE_STEPS, useGenerationRun } from './useGenerationRun';

import { courseExerciseTotal } from '../../lib/model';
import { executionEngineLabel, generationProvenance, provenanceLabel } from '../../lib/provenance';

export default function BuildingScreen() {
  const s = useStore();
  const { state, logOpen, setLogOpen, approve, discard, cancel, retry } = useGenerationRun();
  /* The draft this build is for. Null until the build has produced one, which is
     the honest state for the opening seconds: there is no course yet. */
  const draftCourse = s.draftCourse;
  const { phase, log, steps, built, report, failure, generated, job, recordStatus, awaiting } = state;
  const approving = phase === 'approving';
  const discarding = phase === 'discarding';

  const shown = built ?? draftCourse;
  const exerciseTotal = shown ? courseExerciseTotal(shown) : null;
  /* The report is the one measured record this run produced; the card reads it
     rather than keeping a second copy of the same numbers. `totals.verified`
     counts executed references, so the rest of the total are the failures. */
  const totals = report?.totals ?? null;
  const failed = totals ? Math.max(0, totals.exercises - totals.verified) : 0;

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Course generation</div>
          <h1>Building a course you can inspect</h1>
          <p>Drafting the path is only one step. Exercises are generated, executed, and checked before the course is presented for review.</p>
        </div>
        <button onClick={cancel}>Cancel</button>
      </div>

      {awaiting?.plan && (
        <div className="card" style={{ borderColor: 'var(--green)' }}>
          <h2>Your outline is ready to approve</h2>
          <p className="mt-8" style={{ fontSize: 13 }}>
            The gateway designed the index below and stored it. Nothing is written to your learning yet — approve it to
            build the chapters, or discard it and the course is removed.
          </p>
          <div className="mt-12">
            <div className="reportrow"><span><strong>{awaiting.plan.title}</strong></span><span className="muted">{awaiting.plan.chapters.length} chapter{awaiting.plan.chapters.length === 1 ? '' : 's'}</span></div>
            {awaiting.plan.subtitle && <div className="reportrow"><span className="muted">{awaiting.plan.subtitle}</span><span /></div>}
            {awaiting.plan.chapters.map((ch) => (
              <div className="reportrow" key={ch.id}>
                <span>
                  <span className="mono" style={{ fontSize: 12 }}>{ch.id}</span>
                  &nbsp; {ch.title}
                  {ch.summary ? <><br /><span className="muted" style={{ fontSize: 12 }}>{ch.summary}</span></> : null}
                </span>
                <span className="muted">{ch.objectives.length} objective{ch.objectives.length === 1 ? '' : 's'}</span>
              </div>
            ))}
          </div>
          <div className="actions mt-12">
            <button className="primary" disabled={approving} onClick={() => { if (awaiting.plan) approve(awaiting.plan.id); }}>
              {approving ? 'Building the chapters …' : 'Build it'}
            </button>
            <button className="ghost" disabled={discarding || approving} onClick={() => { if (awaiting.plan) discard(awaiting.plan.id); }}>
              {discarding ? 'Discarding …' : 'Discard'}
            </button>
          </div>
        </div>
      )}

      {failure && (
        <div className="card" style={{ borderColor: 'var(--amber)' }}>
          <h2>Nothing was created</h2>
          <p className="mt-8" style={{ fontSize: 13 }}><strong>{failure.title}.</strong> {failure.detail}</p>
          {failure.exercises.length > 0 && (
            <div className="mt-12">
              {failure.exercises.map((f) => (
                <div className="reportrow" key={f.id}>
                  <span><span className="mono" style={{ fontSize: 12 }}>{f.id}</span> &nbsp;{f.title}</span>
                  <span className="fail">{f.stage}</span>
                </div>
              ))}
            </div>
          )}
          <div className="actions mt-12">
            <button className="primary" onClick={retry}>Try again</button>
            <button onClick={() => s.showScreen('create')}>Change the goal</button>
            {report && <button className="ghost" onClick={() => { s.setDraftReport(report); s.setReviewTab('validation'); s.showScreen('review'); }}>See what was measured</button>}
          </div>
        </div>
      )}

      <div className="grid2">
        <div className="card">
          <h2>Build pipeline</h2>
          <div className="callout mt-12">
            <strong>Your request</strong><br />
            {/* A resumed run has no form behind it, so the goal comes from the
                breadcrumb rather than the input the learner had typed. */}
            <span className="mono" style={{ fontSize: 12 }}>{s.goalInput || s.resumeRun?.goal || '—'}</span>
          </div>
          <div className="pipeline">
            {/* PIPE_STEPS is a fixed-length constant list; the index is the step. */}
            {PIPE_STEPS.map(({ title, small }, i) => (
              <div className={`pipe ${steps[i]}`} key={i}>
                <div className="check">{steps[i] === 'done' ? '✓' : i + 1}</div>
                <div><strong>{title}</strong><small>{small}</small></div>
                <span className="state">{steps[i] === 'done' ? 'Done' : steps[i] === 'running' ? 'Working' : 'Queued'}</span>
              </div>
            ))}
          </div>
          <button className="small ghost mt-12" onClick={() => setLogOpen((v) => !v)}>
            <Icon name="chevron-down" />Show generation stream
          </button>
          <GenerationLog log={log} open={logOpen} />
        </div>
        <div className="card">
          <h2>Why this takes a moment</h2>
          <div className="reportrow mt-8"><span>Chapters planned</span><span>{shown ? (shown.modules ?? []).length : '—'}</span></div>
          {/* A background run reports its own chapter count as it goes, so the
              row is the gateway's, not this tab's guess — and it is absent for a
              blocking run, which has no live view to read. */}
          {job && job.chapters_planned !== undefined && (
            <div className="reportrow">
              <span>Chapters verified and stored</span>
              <span>{`${job.chapters_verified !== undefined ? job.chapters_verified : 0} of ${job.chapters_planned}`}</span>
            </div>
          )}
          {job?.current_chapter && (
            <div className="reportrow"><span>Chapter in progress</span><span>{job.current_chapter}</span></div>
          )}
          {!generated && recordStatus && (
            <div className="reportrow"><span>Stored course</span><span>{recordStatus}</span></div>
          )}
          <div className="reportrow"><span>Exercises with result contracts</span><span>{exerciseTotal ?? '—'}</span></div>
          <div className="reportrow">
            <span>Exercises executed and cross-checked</span>
            <span>{totals ? `${totals.verified}/${totals.verified + failed} · ${totals.rows} rows` : 'measuring …'}</span>
          </div>
          <div className="reportrow">
            <span>Engine time</span>
            <span>{totals ? `${totals.ms} ms` : '—'}</span>
          </div>
          <div className="reportrow">
            <span>Reference failures</span>
            <span className={totals && failed > 0 ? 'fail' : undefined}>
              {totals ? (failed > 0 ? `${failed} to review` : '0') : '—'}
            </span>
          </div>
          <div className="reportrow"><span>Execution engine</span><span className="mono">{executionEngineLabel(s.api)}</span></div>
          <div className="reportrow"><span>Draft pipeline</span><span className="mono" style={{ fontSize: 11 }}>{report?.source === 'authored' ? 'built-in curriculum (no AI)' : generationProvenance(s.api)}</span></div>
          {generated?.plan && (
            <div className="reportrow">
              <span>Provider</span>
              <span className="mono" style={{ fontSize: 11 }}>{provenanceLabel(generated.plan.provenance)}</span>
            </div>
          )}
          {generated && (
            <div className="reportrow">
              <span>Generation</span>
              <span className={generated.status === 'active' || generated.status === 'unavailable' || generated.status === 'awaiting_approval' ? undefined : 'fail'}>
                {generated.status}{generated.error ? ` · ${generated.error.code}` : ''}
              </span>
            </div>
          )}
          <div className="callout mt-16">
            <strong>What is measured here.</strong> A step turns Done when the pipeline reports that stage complete —
            never on a timer. The three opening lines are this tab quoting the request you gave it and asking; every
            line after them is the gateway's own transcript. Each reference query runs on the engine named above, so
            the row counts, timings and failures printed are the ones it returned.
            {s.aiAvailability?.available
              ? ' Because a generation service is configured, the design and the verification both happen on the gateway and the stages and durations shown are its own.'
              : ' No generation service is configured, so the course is the built-in curriculum and its references are executed here, in this tab.'}
          </div>
        </div>
      </div>
    </div>
  );
}
