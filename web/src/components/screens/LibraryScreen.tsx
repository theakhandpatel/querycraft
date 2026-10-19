import Icon from '../ui/Icon';
import PageHead from '../ui/PageHead';
import DialectBadge from '../ui/DialectBadge';
import { useStore } from '../../state/store';
import { COURSES } from '../../data/curriculum';
import { effectiveDialect } from '../../lib/dialect';
import { courseExerciseTotal } from '../../lib/model';
import type { Course } from '../../types';

type ViewCourse = Course & { blurb?: string; domain?: string; minutes?: number; source?: string };

const FILTERS = [
  { key: 'all', label: 'All courses' },
  { key: 'enrolled', label: 'Enrolled' },
  { key: 'mine', label: 'Created by you' },
  { key: 'new', label: 'Not started' },
];

export default function LibraryScreen() {
  const s = useStore();
  const f = s.uiState.libraryFilter || 'all';
  /* Authoring is the admin surface; learners can browse and enroll only. */
  const isAdmin = s.role === 'admin';
  const courses = COURSES.filter((c) => {
    if (f === 'enrolled' && !c.enrolled) return false;
    /* "Created by you" is the learner's own courses, which now live on the
       gateway; `source: 'generated'` is how a stored course is marked, and a
       built-in course never carries it. */
    if (f === 'mine' && c.source !== 'created' && c.source !== 'generated') return false;
    if (f === 'new' && c.enrolled) return false;
    return true;
  }) as ViewCourse[];

  return (
    <div className="content">
      <PageHead
        eyebrow="Library"
        title="Find your next course"
        action={isAdmin && (
          <button className="primary" onClick={() => s.showScreen('create')}><Icon name="sparkles" />Create your own</button>
        )}
      >
        <p>Preview what a course teaches, then enroll to add it to your learning list with its own progress and notes.</p>
      </PageHead>

      <div className="filterbar">
        {FILTERS.map((x) => (
          <button
            key={x.key}
            className={`filt${x.key === f ? ' active' : ''}`}
            onClick={() => s.setLibraryFilter(x.key)}
          >{x.label}</button>
        ))}
      </div>

      <div className="grid3">
        {courses.map((c) => {
          /* A build the gateway stopped writing, or one still waiting for the
             learner's approval: its own state, with resume as the action. A
             live `building` course is just in progress and reads normally. */
          const interrupted = c.recordStatus === 'interrupted';
          const awaitingBuild = c.recordStatus === 'awaiting_approval';
          const paused = interrupted || awaitingBuild;
          const tag = c.source === 'created'
            ? <span className="enrolltag mine">You built this</span>
            : paused
              ? <span className="enrolltag">{interrupted ? 'Interrupted' : 'Waiting for approval'}</span>
              : c.enrolled ? <span className="enrolltag">Enrolled</span> : null;
          let btn;
          if (paused) btn = <button className="small primary" onClick={() => s.resumeStoredCourse(c.id, c.title)}><Icon name="play" />Resume</button>;
          else if (c.built && c.enrolled) btn = <button className="small primary" onClick={() => { s.selectCourse(c.id); s.showScreen('course'); }}><Icon name="play" />Continue</button>;
          else if (c.built) btn = <button className="small primary" onClick={() => s.enrollCourse(c.id)}>Enroll<Icon name="plus" /></button>;
          else btn = <button className="small" onClick={() => s.toast('Preview: this course would generate with the same pipeline.')}><Icon name="eye" />Preview</button>;
          return (
            <div className="coursecard" key={c.id}>
              <div className="coursemeta">
                {tag}
                <span className="pill subtle">{c.level}</span>
                {/* The course card says which engine its chapters run on. */}
                <DialectBadge dialect={effectiveDialect(undefined, c)} />
              </div>
              <h2>{c.title}</h2>
              <p>{c.blurb}</p>
              <div className="reportrow"><span>Chapters</span><span>{c.modules ? `${c.modules.length} · ${courseExerciseTotal(c)} exercises` : '—'}</span></div>
              <div className="reportrow"><span>Est. time</span><span>{c.minutes} min · {c.domain}</span></div>
              <div className="actions mt-8">
                {btn}
                {/* A course the gateway stores is the learner's own record, so
                    removing it is a real action; the built-in curriculum is not
                    theirs to delete and never shows this. */}
                {c.gateway === true && (
                  <button className="small ghost" onClick={() => void s.deleteCourse(c.id)}>Remove</button>
                )}
                {isAdmin && (
                  <button className="small ghost" onClick={() => s.showScreen('create')}>Remix</button>
                )}
              </div>
            </div>
          );
        })}
      </div>

      <div className="callout mt-16">
        A library course is a starting point. When you enroll, you get your own copy — prompts, datasets and exercises are yours to change, and fixes are proposed by AI but only applied with your approval.
      </div>
    </div>
  );
}
