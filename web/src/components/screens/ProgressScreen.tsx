import { useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import { activeModules, courseExerciseIds, coursePassed, exerciseIdsOfModule, courseShort, findExercise } from '../../lib/model';
import { attemptsByDay, WEEKDAY_LABELS, whenLabel, formatDuration, isEarlierVersion, stampFor } from '../../lib/progress';
import { datasetVersion } from '../../api/dataset';
import type { EventKind } from '../../types';

/** One wording for one outcome, shared by the recent list and the history. */
function outcomeLabel(kind: EventKind): string {
  if (kind === 'passed') return 'passed';
  if (kind === 'failed') return 'not yet';
  if (kind === 'accepted') return 'accepted by you';
  if (kind === 'hint') return 'viewed a hint';
  if (kind === 'bookmark') return 'bookmarked a section';
  if (kind === 'error') return 'execution error';
  return 'viewed solution';
}

function outcomeClass(kind: EventKind): string {
  return kind === 'passed' ? 'pass' : kind === 'failed' ? 'warn' : 'muted';
}

export default function ProgressScreen() {
  const s = useStore();
  const { course, progress } = s;
  const evs = (progress.events || []).filter((e) => (e.course || 'sql-analytics') === course.id);

  const ids = courseExerciseIds(course);
  const exercised = ids.filter((id) => progress.firstTry[id] !== undefined).length;
  const firstTry = ids.filter((id) => progress.firstTry[id]).length;

  const midnight = new Date();
  midnight.setHours(0, 0, 0, 0);
  const labels: string[] = [];
  for (let k = 6; k >= 0; k--) labels.push(WEEKDAY_LABELS[new Date(midnight.getTime() - k * 86400000).getDay()]);
  const data = attemptsByDay(evs);
  const max = Math.max.apply(null, data.concat([1]));

  const recent = evs.slice().sort((a, b) => b.ts - a.ts).slice(0, 8);

  /* Design §14: every attempt for one exercise, newest first — the history the
     learner actually produced, read from the stored events rather than
     re-derived from the passed flags. */
  const attemptedIds = Array.from(
    new Set(evs.filter((e) => e.kind === 'passed' || e.kind === 'failed' || e.kind === 'accepted' || e.kind === 'error').map((e) => e.id)),
  );
  const [pickedId, setPickedId] = useState<string>('');
  const activeId = attemptedIds.includes(pickedId) ? pickedId : attemptedIds[0] || '';
  const history = evs.filter((e) => e.id === activeId).sort((a, b) => b.ts - a.ts);

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Progress · {courseShort(course)}</div>
          <h1>Your history</h1>
          <p>Attempts are immutable. Rerunning makes a new attempt; nothing is deleted, and an earlier pass is never silently re-graded.</p>
        </div>
        <button className="primary" onClick={s.resumeCourse}><Icon name="play" />Resume course</button>
      </div>

      <div className="grid3">
        <div className="stat"><strong>{coursePassed(course, progress)}</strong><span>Passed in this course</span></div>
        <div className="stat"><strong>{exercised ? `${Math.round((firstTry / exercised) * 100)}%` : '—'}</strong><span>First-try pass rate</span></div>
        <div className="stat"><strong>{formatDuration(progress.timeMs?.[course.id] || 0)}</strong><span>Time in this course</span></div>
      </div>

      <div className="grid2 mt-16">
        <div className="card">
          <h2>Attempts this week</h2>
          <div className="bars">
            {data.map((v, i) => (
              <div className="bar" key={i}>
                <span style={{ height: `${Math.round((v / max) * 100)}%` }}></span>
                <small>{labels[i]}</small>
              </div>
            ))}
          </div>
          <div className="mt-22"></div>
          <p className="muted mt-12" style={{ fontSize: 12 }}>Each bar is attempts started in this course. Height reflects volume, not correctness.</p>
        </div>
        <div className="card">
          <h2>Recent attempts</h2>
          {recent.length === 0 ? (
            <div className="empty">No attempts in this course yet. Run an exercise and your history appears here.</div>
          ) : (
            recent.map((e, i) => {
              const label = outcomeLabel(e.kind);
              const cls = outcomeClass(e.kind);
              /* §7.3: an attempt against content that has since changed is not
                 re-graded or hidden — it is labelled. */
              const stale = isEarlierVersion(e, stampFor(findExercise(e.id)?.block, datasetVersion));
              return (
                <div className="reportrow" key={i}>
                  <span><span className={`recdot ${cls}`}></span><span className="mono" style={{ fontSize: 12 }}>{e.id}</span> · {e.title}</span>
                  <span className={cls}>
                    {stale ? <span className="pill subtle" title="The exercise or the dataset changed after this attempt">solved on an earlier version</span> : null}
                    <span className="recenttime">{whenLabel(e.ts)}</span> {label}
                  </span>
                </div>
              );
            })
          )}
        </div>
      </div>

      <div className="card mt-16">
        <h2>Per-exercise history</h2>
        {attemptedIds.length === 0 ? (
          <div className="empty">Run an exercise and every attempt shows here, newest first.</div>
        ) : (
          <>
            <div className="chiprow">
              {attemptedIds.map((id) => (
                <button
                  key={id}
                  className={`small ghost${id === activeId ? ' on' : ''}`}
                  aria-pressed={id === activeId}
                  onClick={() => setPickedId(id)}
                >
                  {id}
                </button>
              ))}
            </div>
            <div className="mt-12">
              {history.map((e, i) => (
                <div className="reportrow" key={i}>
                  <span>
                    <span className={`recdot ${outcomeClass(e.kind)}`}></span>
                    <span className="mono" style={{ fontSize: 12 }}>{whenLabel(e.ts)}</span> · {e.title}
                  </span>
                  <span className={outcomeClass(e.kind)}>
                    {typeof e.datasetVersion === 'number' && typeof e.exerciseVersion === 'number' ? (
                      <span className="pill subtle" title="The content versions this attempt was made against">v{e.datasetVersion}.{e.exerciseVersion}</span>
                    ) : (
                      <span className="pill subtle" title="This attempt predates version stamps">unversioned</span>
                    )}
                    {isEarlierVersion(e, stampFor(findExercise(e.id)?.block, datasetVersion)) ? (
                      <span className="pill subtle" title="The exercise or the dataset changed after this attempt">solved on an earlier version</span>
                    ) : null}
                    {' '}{outcomeLabel(e.kind)}
                  </span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>

      <div className="card mt-16">
        <h2>Chapter completion</h2>
        {activeModules(course).map((m, i) => {
          const exs = exerciseIdsOfModule(m);
          const done = exs.filter((id) => progress.passed[id]).length;
          const tried = exs.filter((id) => progress.firstTry[id] !== undefined).length;
          const firstTryPassed = exs.filter((id) => progress.firstTry[id]).length;
          const complete = progress.done[m.id];
          const pct = exs.length ? Math.round((done / exs.length) * 100) : 0;
          const right = exs.length
            ? `${done}/${exs.length} passed · ${tried ? Math.round((firstTryPassed / tried) * 100) : 0}% first try${complete ? ' · complete' : ''}`
            : `${m.minutes} min · reading`;
          return (
            <button type="button" className="reportrow action" key={m.id} onClick={() => s.openLessonIn(course.id, i)}>
              <span>{m.title}</span>
              <span className={complete ? 'pass' : 'muted'} style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                {exs.length ? <span className="progressline" style={{ width: 90 }}><span style={{ width: `${pct}%` }}></span></span> : null}
                {right}
              </span>
            </button>
          );
        })}
      </div>
    </div>
  );
}
