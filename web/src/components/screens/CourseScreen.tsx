import Icon from '../ui/Icon';
import DialectBadge, { useChapterDialect } from '../ui/DialectBadge';
import { useStore } from '../../state/store';
import {
  activeModules, courseExerciseTotal, exerciseIdsOfModule, lessonPercent, nextChapterIndex,
} from '../../lib/model';
import type { Course, CourseModule } from '../../types';

/* One chapter row's badge. It is its own component because a chapter list
   renders one badge per row and each subscribes to the learner's dialect
   choice — a hook called from inside the list's `map` would break the rules of
   hooks. The row and the open lesson therefore always agree. */
function ChapterDialect({ mod, course }: { mod: CourseModule; course: Course }) {
  const dialect = useChapterDialect(mod, course);
  return <DialectBadge dialect={dialect} />;
}

export default function CourseScreen() {
  const s = useStore();
  const { progress } = s;
  const course = s.course as Course & { blurb?: string; domain?: string };
  const pct = lessonPercent(course, progress);
  const ms = activeModules(course);
  const nextIdx = nextChapterIndex(course, progress);
  const complete = ms.length > 0 && ms.every((m) => !!progress.done[m.id]);

  return (
    <div className="content">
      <div className="pagehead">
        <div>
          <div className="eyebrow">Course</div>
          <h1>{course.title}</h1>
          <p>{course.blurb}</p>
          <div className="actions mt-12">
            <span className="pill"><span className="dot"></span>{pct}% complete</span>
            <span className="pill subtle">{course.domain}</span>
            <span className="pill subtle">{ms.length} chapters · {courseExerciseTotal(course)} exercises</span>
          </div>
        </div>
        <div className="actions">
          {complete ? (
            <button className="primary" onClick={s.resumeCourse}><Icon name="eye" />Review chapter 1</button>
          ) : (
            <button className="primary" onClick={s.resumeCourse}><Icon name="play" />Continue chapter {nextIdx + 1}</button>
          )}
        </div>
      </div>

      <div className="mt-16">
        <div className="card">
          <div className="sectiontitle">
            <h2>Chapter index</h2>
            {/* Authoring is the admin surface; a learner sees the curriculum
                without the controls that rewrite it. */}
            {s.role === 'admin' && (
              <div className="actions">
                <button className="small" onClick={() => s.openModal('addLesson')}><Icon name="plus" />Add chapter</button>
              </div>
            )}
          </div>
          <p className="muted mt-8" style={{ fontSize: 12 }}>Work through the chapters in order. Pick up any chapter to review it, and mark it complete when you are done.</p>
          <div className="lessonlist">
            {ms.map((m, i) => {
              const exs = exerciseIdsOfModule(m);
              const isDone = progress.done[m.id];
              return (
                <div className={`lessonrow${isDone ? ' done' : ''}`} key={m.id}>
                  <span className="lessonnum">{isDone ? '✓' : i + 1}</span>
                  <div className="lessontext">
                    <span className="lessontitle">
                      <strong>{m.title}</strong>
                      <ChapterDialect mod={m} course={course} />
                    </span>
                    <small>{m.summary} · {exs.length ? `${exs.length} exercise${exs.length > 1 ? 's' : ''}` : 'reading'} · {m.minutes} min</small>
                  </div>
                  <div className="actions">
                    <button className="small primary" onClick={() => s.openLessonIn(course.id, i)}>
                      <Icon name="play" />{isDone ? 'Review' : 'Start'}
                    </button>
                  </div>
                </div>
              );
            })}
          </div>
        </div>
      </div>
    </div>
  );
}
