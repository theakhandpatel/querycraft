import Icon from '../ui/Icon';
import PageHead from '../ui/PageHead';
import Ring from '../ui/Ring';
import DialectBadge from '../ui/DialectBadge';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { effectiveDialect } from '../../lib/dialect';
import { coursePassed, courseDoneCount, courseExerciseTotal, lessonPercent, nextChapterIndex, courseShort, activeModules, enrolledCourses } from '../../lib/model';
import { currentStreak } from '../../lib/progress';
import type { Course } from '../../types';

type ViewCourse = Course & { blurb?: string; domain?: string };

export default function HomeScreen() {
  const s = useStore();
  const { progress } = s;
  const course = s.course as ViewCourse;
  const pct = lessonPercent(course, progress);
  const ms = activeModules(course);
  const doneN = courseDoneCount(course, progress);
  const nextIdx = nextChapterIndex(course, progress);
  const nextM = ms[nextIdx];

  const enrolled = enrolledCourses();
  const notes = (progress.notes || []).slice().sort((a, b) => (b.ts || 0) - (a.ts || 0));
  const more = COURSES.filter((c) => !c.enrolled) as ViewCourse[];
  /* Course authoring is the admin surface; learners do not see the CTA. */
  const isAdmin = s.role === 'admin';

  return (
    <div className="content">
      <PageHead
        eyebrow="Home"
        title="Keep learning"
        action={isAdmin && (
          <button className="primary" onClick={() => s.showScreen('create')}><Icon name="sparkles" />Create a course</button>
        )}
      >
        <p>Continue the course you started, keep your notes, or discover something new.</p>
      </PageHead>

      <div className="continue">
        <div style={{ flex: 1, minWidth: 'min(280px, 100%)' }}>
          <div className="eyebrow">Continue learning · {course.domain}</div>
          <h2>{course.title}</h2>
          {nextM ? (
            <p>Next up — chapter {nextIdx + 1}: {nextM.title}. {nextM.summary}</p>
          ) : (
            <p>You have reached the end of this course. Revisit a chapter or generate a harder challenge.</p>
          )}
          <div className="meta">
            <span className="pill"><span className="dot"></span>{pct}% complete</span>
            <span className="pill subtle">{doneN} / {ms.length} chapters</span>
            <span className="pill subtle">{coursePassed(course, progress)} / {courseExerciseTotal(course)} exercises</span>
          </div>
          <div className="progress-mini"><span style={{ width: `${pct}%` }}></span></div>
          <div className="actions mt-14">
            <button className="primary" onClick={s.resumeCourse}><Icon name="play" />Continue chapter {nextIdx + 1}</button>
            <button onClick={() => s.showScreen('course')}><Icon name="layers" />Course home</button>
            <button className="ghost" onClick={() => s.showScreen('challenge-lab')}><Icon name="flask" />Challenge lab</button>
          </div>
        </div>
        <Ring pct={pct} />
      </div>

      <div className="stats">
        <div className="stat"><strong>{currentStreak(progress.events)}</strong><span>Day streak</span></div>
        <div className="stat"><strong>{Object.keys(progress.passed).length}</strong><span>Exercises passed</span></div>
        <div className="stat"><strong>{progress.attempts}</strong><span>Total attempts</span></div>
        <div className="stat"><strong>{(progress.notes || []).length + (progress.saved || []).length}</strong><span>Notes &amp; saved</span></div>
      </div>

      <div className="grid2 mt-16">
        <div className="card">
          <div className="sectiontitle">
            <h2>Your courses</h2>
            <button className="small ghost" onClick={() => s.showScreen('library')}>Browse library</button>
          </div>
          <p className="muted mt-8" style={{ fontSize: 12 }}>The course you are taking is selected. Switch between everything you are enrolled in.</p>
          <div className="switcher">
            {enrolled.length === 0 ? (
              <div className="empty">No enrolled courses yet. Browse the library to start one.</div>
            ) : (
              enrolled.map((c) => (
                <button key={c.id} className={`cswitch${c.id === course.id ? ' active' : ''}`} onClick={() => { s.selectCourse(c.id); s.showScreen('home'); }}>
                  <strong>{c.title}</strong>
                  <small>{lessonPercent(c, progress)}% · {(c.modules || []).length} chapters</small>
                </button>
              ))
            )}
          </div>
        </div>
        <div className="card">
          <div className="sectiontitle">
            <h2>Recent notes</h2>
            <button className="small ghost" onClick={() => s.showScreen('workbook')}>Open workbook</button>
          </div>
          <div className="mt-8">
            {notes.length === 0 ? (
              <div className="empty">No notes yet. Use “Add a note” while reading a chapter.</div>
            ) : (
              notes.slice(0, 4).map((n) => {
                const cc = COURSES.find((c) => c.id === n.course) || course;
                return (
                  <div className="reportrow" key={`${n.kind}-${n.ts}`}>
                    <span><Icon name={n.kind === 'bookmark' ? 'bookmark' : 'note'} style={{ verticalAlign: -2 }} /> {n.text}</span>
                    <span className="muted">{courseShort(cc)} · ch {n.lesson}</span>
                  </div>
                );
              })
            )}
          </div>
        </div>
      </div>

      <div className="card mt-16">
        <div className="sectiontitle">
          <h2>Recommended for you</h2>
          <span className="pill subtle">Library</span>
        </div>
        <p className="muted mt-8" style={{ fontSize: 12 }}>Courses you are not enrolled in yet. Enrolling adds them to your learning list and your progress stays separate per course.</p>
        <div className="grid3 mt-14">
          {more.length === 0 ? (
            <div className="empty">You are enrolled in everything we offer.</div>
          ) : (
            more.map((c) => (
              <div className="coursecard" key={c.id}>
                <div className="coursemeta">
                  <span className="pill subtle">{c.level}</span>
                  {/* The course card says which engine its chapters run on. */}
                  <DialectBadge dialect={effectiveDialect(undefined, c)} />
                </div>
                <h2>{c.title}</h2>
                <p>{c.blurb}</p>
                <div className="actions mt-8">
                  <button className="small primary" onClick={() => s.enrollCourse(c.id)}>Enroll<Icon name="plus" /></button>
                  <button className="small ghost" onClick={() => s.toast('Preview: this course would generate with the same pipeline.')}>Preview</button>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
