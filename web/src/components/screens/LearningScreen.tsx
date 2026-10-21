import Icon from '../ui/Icon';
import PageHead from '../ui/PageHead';
import DialectBadge from '../ui/DialectBadge';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { activeModules, enrolledCourses, lessonPercent, nextChapterIndex } from '../../lib/model';
import { effectiveDialect } from '../../lib/dialect';

/*
 * Learning is the page a returning learner lands on. It lists only the
 * courses they have started, with the next chapter and a continue action.
 * The full catalog lives on Courses, not here.
 */
export default function LearningScreen() {
  const s = useStore();
  const taking = enrolledCourses();
  const start = COURSES[0];
  const startModules = activeModules(start);
  const first = startModules[0];

  return (
    <div className="content">
      <PageHead
        eyebrow="Learning"
        title="Your learning"
        action={<button className="small ghost" onClick={() => s.showScreen('library')}><Icon name="search" />Browse courses</button>}
      >
        <p>Continue a course, or start with the first SQLite chapter.</p>
      </PageHead>

      {taking.length === 0 ? (
        <div className="card learningstart">
          <div className="sectiontitle">
            <div>
              <p className="eyebrow">Start here</p>
              <h2>{start.title}</h2>
            </div>
            <DialectBadge dialect={effectiveDialect(undefined, start)} />
          </div>
          <p>{start.blurb}</p>
          <div className="reportrow"><span>First chapter</span><span>{first ? `${first.n} · ${first.title}` : '—'}</span></div>
          <div className="reportrow"><span>Length</span><span>{first?.minutes ?? 0} min · {startModules.length} chapters</span></div>
          <div className="actions mt-14">
            <button className="primary" onClick={() => s.openLessonIn(start.id, 0)}><Icon name="play" />Start chapter 1</button>
            <button className="ghost" onClick={() => s.showScreen('library')}><Icon name="search" />Browse courses</button>
          </div>
        </div>
      ) : (
        <div className="grid3">
          {taking.map((course) => {
            const modules = activeModules(course);
            const pct = lessonPercent(course, s.progress);
            const next = modules[nextChapterIndex(course, s.progress)];
            return (
              <div className="coursecard" key={course.id}>
                <div className="coursemeta">
                  <span className="pill subtle">{pct}% complete</span>
                  <DialectBadge dialect={effectiveDialect(undefined, course)} />
                </div>
                <h2>{course.title}</h2>
                <p>{next ? `Next: chapter ${next.n} · ${next.title}` : 'Every chapter is complete.'}</p>
                <div className="meter"><span style={{ width: `${pct}%` }} /></div>
                <div className="actions mt-8">
                  <button className="small primary" onClick={() => s.openLessonIn(course.id, nextChapterIndex(course, s.progress))}>
                    <Icon name="play" />{pct === 100 ? 'Review' : pct === 0 ? 'Start' : 'Continue'}
                  </button>
                  <button className="small ghost" onClick={() => { s.selectCourse(course.id); s.showScreen('course'); }}>Course</button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}
