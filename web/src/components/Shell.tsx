import React, { useRef } from 'react';
import Icon from './ui/Icon';
import SearchBox from './SearchBox';
import { useStore, SCREEN_NAV } from '../state/store';
import { courseShort, findExercise, activeModules, lessonPercent } from '../lib/model';
import type { FoundExercise } from '../lib/model';
import { useDrawerFocus } from './ui/useDrawerFocus';

/*
 * The shell is one fixed top bar and two drawers.
 *
 * The old layout spent two left columns on navigation before reading could
 * start; at 1920px the chapter text began almost 500px in. Now the destinations
 * live in the bar, the chapter list lives in a drawer opened from the lesson's
 * own segment of that bar, and below 760px the destinations join the chapter
 * list in a drawer of their own.
 *
 * Deliberate: no primary link carries the active underline on the lesson or
 * course screens. The bar's contextual segment (course, chapter, prev/next) is
 * the location cue there; underlining nothing avoids pretending the learner is
 * in the Workbook or the Library.
 */

function TopNavLink({ icon, label, active, badge, onClick }: { icon: string; label: string; active: boolean; badge?: string; onClick: () => void }) {
  return (
    <button className={`toplink${active ? ' active' : ''}`} onClick={onClick} aria-current={active ? 'page' : undefined} aria-label={label} title={label}>
      <Icon name={icon} />
      {/* The label is hidden below 1120px, where the links collapse to icons
          with the bar's context segment squeezed out. aria-label and title keep
          the name reachable with a screen reader and a pointer. */}
      <span className="toplink-label">{label}</span>
      {badge != null && <span className="navbadge">{badge}</span>}
    </button>
  );
}

export function TopBar() {
  const s = useStore();
  const { screen, progress, course, session, role, activeLesson } = s;
  const isAdmin = role === 'admin';
  const nav = SCREEN_NAV[screen];
  const mods = activeModules(course);
  const onCourseTrack = screen === 'lesson' || screen === 'course';

  /* The lesson's own segment of the bar: where you are in the course, and the
     two moves a learner makes most. stepLesson owns the end-of-course behavior
     (it toasts), so the bar never duplicates that decision. */
  const canPrev = screen === 'lesson' && activeLesson > 0;
  const canNext = screen === 'lesson' && activeLesson < mods.length - 1;
  const pct = lessonPercent(course, progress);

  return (
    <header>
      <button className="hamburger" onClick={() => s.setNavOpen(true)} aria-label="Open navigation" title="Navigation">
        <Icon name="menu" />
      </button>

      <button className="brand" onClick={() => s.showScreen('home')} aria-label="Querycraft home">
        <span className="brandmark"><Icon name="terminal" /></span>
        <span className="brandname">querycraft</span>
      </button>

      <nav className="topnav" aria-label="Primary">
        <TopNavLink icon="graduation" label="Home" active={nav === 'home'} onClick={() => s.showScreen('home')} />
        <TopNavLink icon="search" label="Library" active={nav === 'library'} onClick={() => s.showScreen('library')} />
        <TopNavLink icon="book" label="Workbook" active={nav === 'workbook'} badge={`${s.pct}%`} onClick={() => s.showScreen('workbook')} />
        <TopNavLink icon="clock" label="Progress" active={nav === 'progress'} onClick={() => s.showScreen('workbook-progress')} />
        <TopNavLink icon="flask" label="Challenge lab" active={nav === 'lab'} onClick={() => s.showScreen('challenge-lab')} />
        {isAdmin && <TopNavLink icon="sparkles" label="Manage" active={nav === 'manage'} onClick={() => s.showScreen('manage')} />}
      </nav>

      {onCourseTrack && course.id && (
        <div className="topcontext">
          <span className="topcontext-course" title={course.title}>{courseShort(course)}</span>
          {screen === 'lesson' && mods.length > 0 && (
            <>
              <span className="topcontext-pos">Ch.{(mods[activeLesson] || mods[0]).n} of {mods.length} · {pct}%</span>
              <button className="small ghost iconbtn" onClick={() => s.setChaptersOpen(true)} aria-label="All chapters" title="All chapters">
                <Icon name="list" />
              </button>
              <button className="small ghost iconbtn" disabled={!canPrev} onClick={() => s.stepLesson(-1)} aria-label="Previous chapter" title="Previous chapter">
                <Icon name="arrow-left" />
              </button>
              <button className="small ghost iconbtn" disabled={!canNext} onClick={() => s.stepLesson(1)} aria-label="Next chapter" title="Next chapter">
                <Icon name="arrow-right" />
              </button>
            </>
          )}
        </div>
      )}

      {/* Design §10: one search field for the whole shell, Cmd/Ctrl+K. */}
      <div className="topsearch"><SearchBox /></div>

      <div className="headright">
        <span className="enginestate">
          <span className="dot"></span>{' '}
          {s.api.mode === 'http' ? 'gateway · live' : 'postgres 18 · in-browser'}
        </span>
        <button className="small ghost" onClick={s.cycleTheme} aria-label="Toggle theme" title="Toggle theme">
          <Icon name={s.effectiveTheme === 'dark' ? 'sun' : 'moon'} />
        </button>
        <button
          className="avatar avatarbtn"
          onClick={() => s.showScreen('appearance-settings')}
          aria-label="Appearance and account"
          title="Appearance and account"
        >
          {(session?.user.name || 'Q').slice(0, 1).toUpperCase()}
        </button>
      </div>
    </header>
  );
}

function NavButton({ icon, label, active, badge, onClick }: { icon: string; label: string; active?: boolean; badge?: React.ReactNode; onClick: () => void }) {
  return (
    <button className={`nav${active ? ' active' : ''}`} onClick={onClick} aria-label={label} title={label}>
      <Icon name={icon} />
      <span className="navtext">{label}</span>
      {badge != null && <span className="navbadge">{badge}</span>}
    </button>
  );
}

/*
 * Below 760px the primary links leave the bar for this drawer. On desktop the
 * hamburger is hidden and the drawer never opens, but the component stays
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
          <h2>Navigation</h2>
          <button className="small ghost" onClick={() => s.setNavOpen(false)} aria-label="Close navigation"><Icon name="x" /></button>
        </div>
        <div className="drawerbody navdrawerbody">
          {/* Mid-chapter on a phone, the chapter moves are the actions that
              matter; the bar keeps the buttons, and the drawer keeps the list. */}
          {screen === 'lesson' && mods.length > 0 && (
            <div className="navgroup">
              <NavButton icon="list" label="All chapters" onClick={go(() => s.setChaptersOpen(true))} />
              <NavButton icon="arrow-left" label="Previous chapter" onClick={go(() => s.stepLesson(-1))} />
              <NavButton icon="arrow-right" label="Next chapter" onClick={go(() => s.stepLesson(1))} />
            </div>
          )}
          <div className="navgroup">
            <NavButton icon="graduation" label="Home" active={nav === 'home'} onClick={go(() => s.showScreen('home'))} />
            <NavButton icon="layers" label={courseShort(course)} active={nav === 'course'} onClick={go(() => s.showScreen('course'))} />
            <NavButton icon="search" label="Library" active={nav === 'library'} onClick={go(() => s.showScreen('library'))} />
          </div>
          <div className="navgroup">
            <NavButton icon="book" label="Workbook" active={nav === 'workbook'} badge={`${s.pct}%`} onClick={go(() => s.showScreen('workbook'))} />
            <NavButton icon="clock" label="Progress" active={nav === 'progress'} onClick={go(() => s.showScreen('workbook-progress'))} />
            <NavButton icon="flask" label="Challenge lab" active={nav === 'lab'} onClick={go(() => s.showScreen('challenge-lab'))} />
          </div>
          {isAdmin && (
            <div className="navgroup">
              <NavButton icon="sparkles" label="Manage courses" active={nav === 'manage'} onClick={go(() => s.showScreen('manage'))} />
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
            <NavButton icon="settings" label="Appearance" active={nav === 'appearance'} onClick={go(() => s.showScreen('appearance-settings'))} />
          </div>
        </div>
        <div className="drawerfoot">
          <div className="profile">
            <div className="avatar">{(session?.user.name || 'Q').slice(0, 1).toUpperCase()}</div>
            <div className="name">
              {session?.user.name || 'Workspace'}<br />
              <span className="muted">{session?.user.email || 'Not signed in'}</span>
            </div>
            {session?.token && (
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