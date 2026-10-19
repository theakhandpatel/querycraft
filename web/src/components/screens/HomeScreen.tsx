import { useEffect, useState } from 'react';
import Icon from '../ui/Icon';
import DialectBadge from '../ui/DialectBadge';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { effectiveDialect } from '../../lib/dialect';
import {
  activeModules,
  courseDoneCount,
  coursePassed,
  enrolledCourses,
  lessonPercent,
  moduleExerciseBlocks,
  nextChapterIndex,
} from '../../lib/model';
import { currentStreak, formatDuration } from '../../lib/progress';
import SqlCell from '../SqlCell';
import type { Course } from '../../types';

type ViewCourse = Course & { blurb?: string; level?: string };

/*
 * Home is the workbench, not a dashboard.
 *
 * The selected course's next chapter is the page: its title and summary open
 * the sheet, and when that chapter carries an exercise the real SqlCell renders
 * it through the same execution seam the lesson uses. The verdict inside the
 * cell owns the next action. Progress, streak and study time are margin
 * entries, not a row of zeros. A chapter with no exercise still opens as one
 * ledger sheet with one start action.
 */
export default function HomeScreen() {
  const s = useStore();
  const { progress } = s;
  const course = s.course as ViewCourse;
  const pct = lessonPercent(course, progress);
  const ms = activeModules(course);
  const doneN = courseDoneCount(course, progress);
  const liveIdx = nextChapterIndex(course, progress);
  /* A pass rebuilds progress and can move the course's next chapter on the
     next render. Hold the chapter that was on the page so the verdict and its
     next action stay in front of the learner; leaving Home clears the hold. */
  const [heldIdx, setHeldIdx] = useState<number | null>(null);
  useEffect(() => { setHeldIdx(null); }, [course.id]);
  const nextIdx = heldIdx ?? liveIdx;
  const allDone = heldIdx === null && ms.length > 0 && doneN >= ms.length;
  const nextM = heldIdx !== null ? ms[heldIdx] : allDone ? undefined : ms[liveIdx];
  const exercise = nextM ? moduleExerciseBlocks(nextM)[0] : undefined;
  const exerciseLabel = exercise?.id ? String(exercise.id).replace('ex.', '') : null;

  const enrolled = enrolledCourses() as ViewCourse[];
  const notes = (progress.notes || []).slice().sort((a, b) => (b.ts || 0) - (a.ts || 0));
  const more = COURSES.filter((c) => !c.enrolled) as ViewCourse[];
  const isAdmin = s.role === 'admin';
  const passed = coursePassed(course, progress);
  const study = formatDuration(progress.timeMs?.[course.id] || 0);
  const streak = currentStreak(progress.events);

  function openNext() {
    if (nextM) s.openLessonIn(course.id, nextIdx);
    else s.showScreen('course');
  }

  return (
    <div className="content homework">
      <div className="workbench">
        <div className="workbench-main">
          {nextM ? (
            <>
              <header className="workhead">
                <div className="eyebrow">Workbench · Chapter {nextM.n}</div>
                <h1>{nextM.title}</h1>
                <p className="worklede">{nextM.summary}</p>
                <div className="workmeta">
                  <span>Chapter {nextM.n} of {ms.length}</span>
                  <span>{exercise ? `Exercise ${exerciseLabel}` : `${nextM.minutes} min read`}</span>
                  <DialectBadge dialect={effectiveDialect(nextM, course)} />
                  <button className="linklike worklink" onClick={openNext}>
                    Read the chapter<Icon name="arrow-right" />
                  </button>
                </div>
              </header>

              {exercise ? (
                <SqlCell
                  key={exercise.id ?? nextM.id}
                  block={exercise}
                  module={nextM}
                  course={course}
                  onPassed={() => setHeldIdx(nextIdx)}
                />
              ) : (
                <section className="chapter-sheet" aria-labelledby="next-sheet-title">
                  <div className="entry-kicker">Next chapter · {nextM.n}</div>
                  <h2 id="next-sheet-title">{nextM.title}</h2>
                  <p>{nextM.summary}</p>
                  <div className="sheet-meta">
                    <span>{nextM.minutes} min read</span>
                    <span>{(nextM.objectives || []).length} objectives</span>
                    <span>No graded exercise in this chapter</span>
                  </div>
                  <div className="actions mt-16">
                    <button className="primary" onClick={openNext}>Start chapter {nextM.n}<Icon name="arrow-right" /></button>
                    <button className="ghost" onClick={() => s.showScreen('course')}><Icon name="layers" />Course home</button>
                  </div>
                </section>
              )}
            </>
          ) : (
            <section className="chapter-sheet" aria-labelledby="next-sheet-title">
              <div className="entry-kicker">Workbench · {doneN} of {ms.length} chapters</div>
              <h2 id="next-sheet-title">You have reached the end of this course</h2>
              <p>Revisit a chapter, open the workbook, or start another course from the library. Your notes and attempts stay recorded.</p>
              <div className="actions mt-16">
                <button className="primary" onClick={() => s.showScreen('course')}><Icon name="layers" />Open course home</button>
                <button className="ghost" onClick={() => s.showScreen('library')}><Icon name="search" />Browse the library</button>
              </div>
            </section>
          )}
        </div>

        <aside className="ledger-margin" aria-label="Your ledger">
          <section className="ledger-entry">
            <div className="entry-kicker">Progress</div>
            <p className="ledger-line">
              <strong>{passed}</strong> passed · <strong>{study}</strong> · chapter {nextM ? nextM.n : ms.length} of {ms.length}
            </p>
            <div className="ledger-meter" aria-hidden="true"><span style={{ width: `${pct}%` }}></span></div>
            <dl className="ledger-dl">
              <div><dt>Course</dt><dd>{pct}%</dd></div>
              <div><dt>Day streak</dt><dd>{streak}</dd></div>
              <div><dt>Study time</dt><dd>{study}</dd></div>
              <div><dt>Notes &amp; saved</dt><dd>{(progress.notes || []).length + (progress.saved || []).length}</dd></div>
            </dl>
          </section>

          <section className="ledger-entry">
            <div className="entry-kicker">The course</div>
            <p className="ledger-note">
              {nextM ? `Next is chapter ${nextM.n}: ${nextM.title}.` : 'Every chapter in this course is behind you.'}
            </p>
            <div className="ledger-links">
              <button className="linklike" onClick={() => s.showScreen('course')}>Course home</button>
              <button className="linklike" onClick={() => s.showScreen('workbook')}>Open workbook</button>
              <button className="linklike" onClick={() => s.showScreen('challenge-lab')}>Challenge lab</button>
              {isAdmin && <button className="linklike" onClick={() => s.showScreen('create')}>Create a course</button>}
            </div>
          </section>

          <section className="ledger-entry">
            <div className="entry-kicker">Switch course</div>
            <div className="ledger-courses">
              {enrolled.length === 0 ? (
                <div className="empty">No courses yet. Browse the library to start one.</div>
              ) : (
                enrolled.map((c) => (
                  <button
                    key={c.id}
                    className={`cswitch${c.id === course.id ? ' active' : ''}`}
                    onClick={() => { s.selectCourse(c.id); s.showScreen('home'); }}
                  >
                    <strong>{c.title}</strong>
                    <small>{lessonPercent(c, progress)}% · {(c.modules || []).length} chapters</small>
                  </button>
                ))
              )}
            </div>
          </section>
        </aside>
      </div>

      <div className="ledger-below">
        <section className="ledger-panel" aria-labelledby="notes-title">
          <div className="panelhead">
            <h2 id="notes-title">Recent notes</h2>
            <button className="small ghost" onClick={() => s.showScreen('workbook')}>Open workbook</button>
          </div>
          {notes.length === 0 ? (
            <div className="empty">No notes yet. Use “Add note” while reading a chapter.</div>
          ) : (
            notes.slice(0, 4).map((n) => (
              <div className="reportrow" key={`${n.kind}-${n.ts}`}>
                <span><Icon name={n.kind === 'bookmark' ? 'bookmark' : 'note'} style={{ verticalAlign: -2 }} /> {n.text}</span>
                <span className="muted">ch {n.lesson}</span>
              </div>
            ))
          )}
        </section>

        <section className="ledger-panel" aria-labelledby="library-title">
          <div className="panelhead">
            <h2 id="library-title">More from the library</h2>
            <button className="small ghost" onClick={() => s.showScreen('library')}>Browse all</button>
          </div>
          {more.length === 0 ? (
            <div className="empty">You are enrolled in everything we offer.</div>
          ) : (
            more.slice(0, 3).map((c) => (
              <div className="libraryrow" key={c.id}>
                <div>
                  <strong>{c.title}</strong>
                  <small>{c.level} · {(c.modules || []).length} chapters</small>
                </div>
                <div className="libraryrow-actions">
                  <button className="small primary" onClick={() => s.enrollCourse(c.id)}>Enroll</button>
                  <button className="small ghost" onClick={() => s.toast('Preview: this course would generate with the same pipeline.')}>Preview</button>
                </div>
              </div>
            ))
          )}
        </section>
      </div>
    </div>
  );
}
