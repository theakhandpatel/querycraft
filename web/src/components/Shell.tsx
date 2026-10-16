import React from 'react';
import Icon from './ui/Icon';
import SearchBox from './SearchBox';
import { useStore, SCREEN_CRUMBS, SCREEN_NAV } from '../state/store';
import { courseShort, findExercise } from '../lib/model';
import type { FoundExercise } from '../lib/model';

function NavButton({ icon, label, active, badge, onClick }: { icon: string; label: string; active: boolean; badge?: React.ReactNode; onClick: () => void }) {
  return (
    /* The label is hidden below 760px, where the rail collapses to 64px of
       icons. `title` keeps the name reachable with a pointer and `aria-label`
       keeps it reachable with a screen reader; without both, the narrow layout
       is nine anonymous glyphs. */
    <button className={`nav${active ? ' active' : ''}`} onClick={onClick} aria-label={label} title={label}>
      <Icon name={icon} />
      <span className="navtext">{label}</span>
      {badge != null && <span className="navbadge">{badge}</span>}
    </button>
  );
}

export function Sidebar() {
  const s = useStore();
  const { screen, progress, course, openLessonIn, session, role } = s;
  /* Course authoring is the app's admin surface; learners get the learning and
     practice tracks only. */
  const isAdmin = role === 'admin';

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

  return (
    <aside>
      <div className="brand">
        <div className="brandmark"><Icon name="terminal" /></div>
        <span className="brandname">querycraft</span>
      </div>

      {/* Only the nav scrolls. The account row below is pinned, so sign-out
          stays reachable on a short viewport instead of sliding under the fold. */}
      <div className="navscroll">
      <div className="navgroup">
        <div className="label">Learn</div>
        <NavButton icon="graduation" label="Home" active={SCREEN_NAV[screen] === 'home'} onClick={() => s.showScreen('home')} />
        <NavButton icon="layers" label={courseShort(course)} active={SCREEN_NAV[screen] === 'course'} onClick={() => s.showScreen('course')} />
        <NavButton icon="search" label="Library" active={SCREEN_NAV[screen] === 'library'} onClick={() => s.showScreen('library')} />
      </div>

      <div className="navgroup">
        <div className="label">Practice</div>
        <NavButton icon="book" label="Workbook" active={SCREEN_NAV[screen] === 'workbook'} badge={`${s.pct}%`} onClick={() => s.showScreen('workbook')} />
        <NavButton icon="clock" label="Progress" active={SCREEN_NAV[screen] === 'progress'} onClick={() => s.showScreen('workbook-progress')} />
        <NavButton icon="flask" label="Challenge lab" active={SCREEN_NAV[screen] === 'lab'} onClick={() => s.showScreen('challenge-lab')} />
      </div>

      {isAdmin && (
        <div className="navgroup">
          <div className="label">Create</div>
          <NavButton icon="sparkles" label="Create a course" active={SCREEN_NAV[screen] === 'create'} onClick={() => s.showScreen('create')} />
        </div>
      )}

      {recent.length > 0 && (
        <div className="navgroup">
          <div className="label">Recent</div>
          {recent.slice(0, 3).map((f) => (
            <button
              key={f.course.id + f.index}
              className="nav"
              onClick={() => openLessonIn(f.course.id, f.index)}
              aria-label={`Chapter ${f.module.n}: ${f.module.title}`}
              title={`Chapter ${f.module.n}: ${f.module.title}`}
            >
              <Icon name="clock" />
              <span className="navtext">{f.module.title}</span>
            </button>
          ))}
        </div>
      )}

      <div className="spacer" />
      <div className="navgroup">
        <NavButton icon="settings" label="Appearance" active={SCREEN_NAV[screen] === 'appearance'} onClick={() => s.showScreen('appearance-settings')} />
      </div>
      </div>
      <div className="profile">
        <div className="avatar">{(session?.user.name || 'Q').slice(0, 1).toUpperCase()}</div>
        <div className="name">
          {session?.user.name || 'Workspace'}<br />
          <span className="muted">{session?.user.email || 'Not signed in'}</span>
        </div>
        {session?.token && (
          <button className="small ghost" onClick={() => void s.logout()} aria-label="Sign out" title="Sign out">
            <Icon name="arrow-left" />
          </button>
        )}
      </div>
    </aside>
  );
}

export function Header() {
  const { screen, effectiveTheme, cycleTheme, api } = useStore();
  return (
    <header>
      <div id="crumb">{SCREEN_CRUMBS[screen] || screen}</div>
      {/* Design §10: one search field for the whole shell, Cmd/Ctrl+K. */}
      <SearchBox />
      <div className="headright">
        <span className="enginestate hide-small">
          <span className="dot"></span>{' '}
          {api.mode === 'http' ? 'gateway · live' : 'postgres 18 · in-browser'}
        </span>
        <button className="small ghost" onClick={cycleTheme} aria-label="Toggle theme" title="Toggle theme">
          <Icon name={effectiveTheme === 'dark' ? 'sun' : 'moon'} />
        </button>
      </div>
    </header>
  );
}
