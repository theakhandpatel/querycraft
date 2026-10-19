import { useRef } from 'react';
import Icon from './ui/Icon';
import SearchBox from './SearchBox';
import { useStore } from '../state/store';
import { SCREEN_CRUMBS, SCREEN_NAV } from '../state/screens';
import { courseShort, findExercise, activeModules, lessonPercent, nextChapterIndex, coursePassed } from '../lib/model';
import { effectiveDialect } from '../lib/dialect';
import { SIMPLE_MODE } from '../lib/mode';
import { formatDuration } from '../lib/progress';
import type { FoundExercise } from '../lib/model';
import { useDrawerFocus } from './ui/useDrawerFocus';

/*
 * The Ledger frame.
 *
 * On desktop the shell is a page: a margin spine on the left names every
 * destination, states where the learner is, and keeps the engine and session
 * marks; the field on the right is the reading and writing surface. Rules
 * separate the two rather than cards floating on a grey canvas.
 *
 * Below 1120px the spine becomes an icon rail so the field keeps its measure.
 * Below 760px the destinations move to a bottom action bar, with Continue as
 * the primary target in thumb reach, and the full list (plus search, recent
 * chapters and the session) opens as a sheet. Every destination, the admin
 * gate, the theme button and the session controls survive at every width.
 */

function NavLink({ icon, label, active, badge, variant, onClick }: { icon: string; label: string; active?: boolean; badge?: string; variant: 'top' | 'drawer'; onClick: () => void }) {
  const top = variant === 'top';
  /* With a badge the accessible name has to contain the badge's visible text as
     well as the label, so the name is the label plus the badge. */
  const name = badge == null ? label : `${label} ${badge}`;
  return (
    <button
      className={`${top ? 'toplink' : 'nav'}${active ? ' active' : ''}`}
      onClick={onClick}
      aria-current={top && active ? 'page' : undefined}
      aria-label={name}
      title={label}
    >
      <Icon name={icon} />
      {/* In the top variant the label is hidden below 1120px, where the spine
          collapses to an icon rail. aria-label and title keep the name
          reachable with a screen reader and a pointer. */}
      <span className={top ? 'toplink-label' : 'navtext'}>{label}</span>
      {badge != null && (
        /* The badge is visible text inside the button. aria-label overrides the
           name the control would otherwise take from its content, so it has to
           carry the badge too, or "0%" is visible text the name does not contain
           (the a11y audit's label-content-name-mismatch). The name and the badge
           text both carry one separating space: the flex item trims it on
           screen, and axe compares the name against the unstripped text. */
        <span className="navbadge">{` ${badge}`}</span>
      )}
    </button>
  );
}

export function TopBar() {
  const s = useStore();
  const { screen, progress, course, session, role, activeLesson } = s;
  const isAdmin = role === 'admin';
  const nav = SCREEN_NAV[screen];
  const mods = activeModules(course);
  const courseDialect = effectiveDialect(undefined, course);
  const onCourseTrack = screen === 'lesson' || screen === 'course';

  /* The lesson's own segment of the bar: where you are in the course, and the
     two moves a learner makes most. stepLesson owns the end-of-course behavior
     (it toasts), so the bar never duplicates that decision. */
  const canPrev = screen === 'lesson' && activeLesson > 0;
  const canNext = screen === 'lesson' && activeLesson < mods.length - 1;
  const pct = lessonPercent(course, progress);
  const nextIdx = nextChapterIndex(course, progress);
  const here = screen === 'lesson' ? mods[activeLesson] : mods[nextIdx];
  const passed = coursePassed(course, progress);
  const study = formatDuration(progress.timeMs?.[course.id] || 0);

  return (
    <>
      <aside className="spine">
        <div className="spine-top">
          <button className="hamburger" onClick={() => s.setNavOpen(true)} aria-label="Open navigation" title="Navigation">
            <Icon name="menu" />
          </button>
          <button className="brand" onClick={() => s.showScreen('home')} aria-label="Querycraft home">
            <span className="brandmark" aria-hidden="true" />
            <span className="brandname">Querycraft</span>
          </button>
        </div>

        <div className="spine-context">
          <div className="spine-kicker">SQL workbook</div>
          <div className="spine-course" title={course.title}>{courseShort(course)}</div>
          <div className="spine-position">
            {screen === 'lesson' ? 'Chapter' : 'Next · chapter'} {here ? here.n : '—'} of {mods.length} · {pct}%
          </div>
          <div className="spine-ledgerline">
            {passed} passed · {study} · {pct}%
          </div>
        </div>

        <nav className="topnav" aria-label="Primary">
          <NavLink variant="top" icon="graduation" label="Home" active={nav === 'home'} onClick={() => s.showScreen('home')} />
          <NavLink variant="top" icon="search" label="Library" active={nav === 'library'} onClick={() => s.showScreen('library')} />
          <NavLink variant="top" icon="book" label="Workbook" active={nav === 'workbook'} badge={`${s.pct}%`} onClick={() => s.showScreen('workbook')} />
          <NavLink variant="top" icon="clock" label="Progress" active={nav === 'progress'} onClick={() => s.showScreen('workbook-progress')} />
          {!SIMPLE_MODE && <NavLink variant="top" icon="flask" label="Challenge lab" active={nav === 'lab'} onClick={() => s.showScreen('challenge-lab')} />}
          {isAdmin && <NavLink variant="top" icon="sparkles" label="Manage" active={nav === 'manage'} onClick={() => s.showScreen('manage')} />}
        </nav>

        <div className="spine-foot">
          <span className="enginestate">
            <span className="dot"></span>{' '}
            {s.api.mode === 'http'
              ? 'gateway · live'
              : `${courseDialect === 'sqlite' ? 'sqlite' : 'postgres'} · in-browser`}
          </span>
          {session && (
            <div className="spine-profile">
              <span className="avatar" aria-hidden="true">{(session.user.name || 'Q').slice(0, 1).toUpperCase()}</span>
              <span className="spine-profile-name">
                {session.user.name || 'Workspace'}
                <span className="spine-profile-email">{session.user.email || 'Not signed in'}</span>
              </span>
              {s.api.mode === 'http' && (
                <button className="small ghost iconbtn" onClick={() => void s.logout()} aria-label="Sign out" title="Sign out">
                  <Icon name="logout" />
                </button>
              )}
            </div>
          )}
        </div>
      </aside>

      <header className="runhead">
        <div className="runhead-context">
          <span className="runhead-section">{SCREEN_CRUMBS[screen]}</span>
          <span className="runhead-rule" aria-hidden="true" />
          <span className="runhead-course" title={course.title}>{course.title}</span>
          {onCourseTrack && screen === 'lesson' && mods.length > 0 && (
            <span className="runhead-moves">
              <button className="small ghost iconbtn" onClick={() => s.setChaptersOpen(true)} aria-label="All chapters" title="All chapters">
                <Icon name="list" />
              </button>
              <button className="small ghost iconbtn" disabled={!canPrev} onClick={() => s.stepLesson(-1)} aria-label="Previous chapter" title="Previous chapter">
                <Icon name="arrow-left" />
              </button>
              <button className="small ghost iconbtn" disabled={!canNext} onClick={() => s.stepLesson(1)} aria-label="Next chapter" title="Next chapter">
                <Icon name="arrow-right" />
              </button>
            </span>
          )}
        </div>

        {/* Design §10: one search field for the whole shell, Cmd/Ctrl+K. */}
        <div className="topsearch"><SearchBox /></div>

        <div className="headright">
          <button className="small ghost" onClick={() => s.showScreen('appearance-settings')} aria-label="Themes" title="Themes">
            <Icon name="palette" />
          </button>
          <button
            className="avatar avatarbtn"
            onClick={() => s.showScreen('appearance-settings')}
            aria-label={SIMPLE_MODE ? 'Appearance' : 'Appearance and account'}
            title={SIMPLE_MODE ? 'Appearance' : 'Appearance and account'}
          >
            {(session?.user.name || 'Q').slice(0, 1).toUpperCase()}
          </button>
        </div>
      </header>

      <nav className="bottombar" aria-label="Quick navigation">
        <button className={`bbitem${nav === 'home' ? ' active' : ''}`} onClick={() => s.showScreen('home')}>
          <Icon name="graduation" /><span>Home</span>
        </button>
        <button className={`bbitem${nav === 'library' ? ' active' : ''}`} onClick={() => s.showScreen('library')}>
          <Icon name="search" /><span>Library</span>
        </button>
        <button className="bbitem bbcontinue" onClick={s.resumeCourse}>
          <Icon name="play" /><span>Continue</span>
        </button>
        <button className={`bbitem${nav === 'workbook' ? ' active' : ''}`} onClick={() => s.showScreen('workbook')}>
          <Icon name="book" /><span>Workbook</span>
        </button>
        <button className="bbitem" onClick={() => s.setNavOpen(true)} aria-label="More sections" aria-haspopup="dialog" aria-expanded={s.navOpen}>
          <Icon name="menu" /><span>More</span>
        </button>
      </nav>
    </>
  );
}

/*
 * Below 760px the primary links leave the shell for this sheet. On desktop the
 * hamburger is hidden and the sheet never opens, but the component stays
 * mounted and inert, exactly like the tables drawer.
 */
export function NavDrawer() {
  const s = useStore();
  const { screen, progress, course, openLessonIn, session, role } = s;
  const isAdmin = role === 'admin';
  const nav = SCREEN_NAV[screen];
  const asideRef = useRef<HTMLElement>(null);
  useDrawerFocus(s.navOpen, asideRef);
  const mods = activeModules(course);

  const recent: FoundExercise[] = [];
  const seen: Record<string, boolean> = {};
  (progress.events || [])
    .slice()
    .sort((a, b) => b.ts - a.ts)
    .forEach((e) => {
      const f = findExercise(e.id);
      if (!f) return;
      const key = f.course.id + ':' + f.index;
      if (seen[key]) return;
      seen[key] = true;
      recent.push(f);
    });

  const go = (fn: () => void) => () => {
    s.setNavOpen(false);
    fn();
  };

  return (
    <>
      <div className={`drawer-back${s.navOpen ? ' open' : ''}`} onClick={() => s.setNavOpen(false)} aria-hidden="true" />
      <aside
        ref={asideRef}
        tabIndex={-1}
        className={`drawer left${s.navOpen ? ' open' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label="Navigation"
        aria-hidden={!s.navOpen}
      >
        <div className="drawerhead">
          <div>
            <div className="drawer-kicker">Querycraft</div>
            <h2>Sections</h2>
          </div>
          <button className="small ghost" onClick={() => s.setNavOpen(false)} aria-label="Close navigation"><Icon name="x" /></button>
        </div>
        <div className="drawerbody navdrawerbody">
          {/* Mid-chapter on a phone, the chapter moves are the actions that
              matter; the sheet keeps the list and the bar keeps the buttons. */}
          {screen === 'lesson' && mods.length > 0 && (
            <div className="navgroup">
              <NavLink variant="drawer" icon="list" label="All chapters" onClick={go(() => s.setChaptersOpen(true))} />
              <NavLink variant="drawer" icon="arrow-left" label="Previous chapter" onClick={go(() => s.stepLesson(-1))} />
              <NavLink variant="drawer" icon="arrow-right" label="Next chapter" onClick={go(() => s.stepLesson(1))} />
            </div>
          )}
          <div className="navgroup">
            <NavLink variant="drawer" icon="graduation" label="Home" active={nav === 'home'} onClick={go(() => s.showScreen('home'))} />
            <NavLink variant="drawer" icon="layers" label={courseShort(course)} active={nav === 'course'} onClick={go(() => s.showScreen('course'))} />
            <NavLink variant="drawer" icon="search" label="Library" active={nav === 'library'} onClick={go(() => s.showScreen('library'))} />
          </div>
          <div className="navgroup">
            <NavLink variant="drawer" icon="book" label="Workbook" active={nav === 'workbook'} badge={`${s.pct}%`} onClick={go(() => s.showScreen('workbook'))} />
            <NavLink variant="drawer" icon="clock" label="Progress" active={nav === 'progress'} onClick={go(() => s.showScreen('workbook-progress'))} />
            {!SIMPLE_MODE && <NavLink variant="drawer" icon="flask" label="Challenge lab" active={nav === 'lab'} onClick={go(() => s.showScreen('challenge-lab'))} />}
          </div>
          {isAdmin && (
            <div className="navgroup">
              <NavLink variant="drawer" icon="sparkles" label="Manage courses" active={nav === 'manage'} onClick={go(() => s.showScreen('manage'))} />
            </div>
          )}
          {recent.length > 0 && (
            <div className="navgroup">
              <div className="label">Recent</div>
              {recent.slice(0, 3).map((f) => (
                <button
                  key={f.course.id + f.index}
                  className="nav"
                  onClick={go(() => openLessonIn(f.course.id, f.index))}
                  aria-label={`Chapter ${f.module.n}: ${f.module.title}`}
                  title={`Chapter ${f.module.n}: ${f.module.title}`}
                >
                  <Icon name="clock" />
                  <span className="navtext">{f.module.title}</span>
                </button>
              ))}
            </div>
          )}
          <div className="navgroup">
            <NavLink variant="drawer" icon="settings" label="Appearance" active={nav === 'appearance'} onClick={go(() => s.showScreen('appearance-settings'))} />
          </div>
        </div>
        <div className="drawerfoot">
          <div className="profile">
            <div className="avatar">{(session?.user.name || 'Q').slice(0, 1).toUpperCase()}</div>
            <div className="name">
              {session?.user.name || 'Workspace'}<br />
              <span className="muted">{session?.user.email || 'Not signed in'}</span>
            </div>
            {s.api.mode === 'http' && session && (
              <button className="small ghost" onClick={() => void s.logout()} aria-label="Sign out" title="Sign out">
                <Icon name="logout" />
              </button>
            )}
          </div>
        </div>
      </aside>
    </>
  );
}
