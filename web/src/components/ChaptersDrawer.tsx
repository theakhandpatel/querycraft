import { useRef } from 'react';
import Icon from './ui/Icon';
import { useStore } from '../state/store';
import { activeModules, lessonPercent } from '../lib/model';
import { useDrawerFocus } from './ui/useDrawerFocus';

/*
 * The chapter list, as a drawer.
 *
 * It used to be a permanent left column beside the reading column; together
 * the two chrome columns took about 40% of a 1920px screen before a sentence
 * started. The list now lives here, opened from the lesson's own segment of
 * the top bar, and the reading column is centered on the page.
 */
export default function ChaptersDrawer() {
  const s = useStore();
  const { course, progress, activeLesson } = s;
  const asideRef = useRef<HTMLElement>(null);
  useDrawerFocus(s.chaptersOpen, asideRef);
  const mods = activeModules(course);
  const pct = lessonPercent(course, progress);

  const go = (i: number) => {
    s.setChaptersOpen(false);
    s.openLessonIn(course.id, i);
  };

  return (
    <>
      <div className={`drawer-back${s.chaptersOpen ? ' open' : ''}`} onClick={() => s.setChaptersOpen(false)} aria-hidden="true" />
      <aside
        ref={asideRef}
        tabIndex={-1}
        className={`drawer left${s.chaptersOpen ? ' open' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label="Chapters"
        aria-hidden={!s.chaptersOpen}
      >
        <div className="drawerhead">
          <div>
            <div className="eyebrow">Course · {mods.length} chapters</div>
            <h2>{course.title}</h2>
          </div>
          <button className="small ghost" onClick={() => s.setChaptersOpen(false)} aria-label="Close chapters"><Icon name="x" /></button>
        </div>
        <div className="drawerbody">
          <div className="sideprogress-label">Chapter {activeLesson + 1} of {mods.length} · {pct}%</div>
          <div className="progressline"><span style={{ width: `${pct}%` }}></span></div>
          <div className="chapterlist">
            {mods.map((mm, i) => (
              <button
                key={mm.id}
                className={`lessonnav${i === activeLesson ? ' active' : ''}${progress.done[mm.id] ? ' done' : ''}`}
                onClick={() => go(i)}
              >
                <span className="bullet">{progress.done[mm.id] ? '✓' : i + 1}</span>
                <span className="lessonnavtext">{mm.title}</span>
              </button>
            ))}
          </div>
          <button
            className="small ghost mt-18"
            onClick={() => { s.setChaptersOpen(false); s.showScreen('course'); }}
          >
            <Icon name="arrow-left" />Course home
          </button>
        </div>
      </aside>
    </>
  );
}