import React, { useEffect, useRef } from 'react';
import Icon from '../ui/Icon';
import SqlCell from '../SqlCell';
import Markdown from '../ui/Markdown';
import { useStore } from '../../state/store';
import { chapterOutline, lessonPercent, activeModules } from '../../lib/model';
import { accrueStudyTime } from '../../lib/progress';
import type { Block, ModalName } from '../../types';

type OutlineBlock = Block | { type: 'list'; items: string[] };

export default function LessonScreen() {
  const s = useStore();
  const { course, progress, activeLesson } = s;
  const mods = activeModules(course);
  const m = mods[activeLesson] || mods[0];
  const mainRef = useRef(null);
  const pct = lessonPercent(course, progress);

  useEffect(() => {
    const ws = document.querySelector('.workspace');
    if (ws) ws.scrollTop = 0;
  }, [activeLesson, m && m.id]);

  /* Measure study time instead of inventing it.
   *
   * The Progress screen used to print `attempts * 3 + 6` minutes, so an
   * untouched workspace claimed six minutes of work. This counts only time the
   * chapter is actually open AND the tab is actually visible, and commits in
   * ~20s chunks so a crash loses at most one tick. A hidden tab is not study
   * time, so `document.hidden` pauses the clock rather than accruing it. */
  const addStudyTime = s.addStudyTime;
  const courseId = course.id;
  useEffect(() => {
    let last = Date.now();
    let pending = 0;
    const commit = () => {
      if (pending <= 0) return;
      const ms = pending;
      pending = 0;
      addStudyTime(courseId, ms);
    };
    const sample = () => {
      const now = Date.now();
      pending = accrueStudyTime(pending, now - last, document.hidden);
      last = now;
      if (pending >= 20000) commit();
    };
    const onVisibility = () => {
      /* Flush on either direction: leaving the tab banks what was measured,
         returning starts a fresh window instead of billing the gap. */
      sample();
      commit();
      last = Date.now();
    };
    const iv = window.setInterval(sample, 1000);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.clearInterval(iv);
      document.removeEventListener('visibilitychange', onVisibility);
      sample();
      commit();
    };
  }, [addStudyTime, courseId]);

  if (!m) return null;

  /* The chapter's reading content comes from the database as markdown. When it
     is present it replaces the bundled prose blocks; the interactive blocks
     (syntax, examples, exercises) always come from the curriculum. */
  const docs = s.content[m.id] ?? [];
  const blocks: Block[] = docs.length > 0
    ? [
        ...docs.map((d): Block => ({ type: 'prose', section: d.section, html: d.markdown, markdown: true })),
        ...(m.blocks || []).filter((b) => b.type !== 'prose'),
      ]
    : (m.blocks || []);
  const outline = chapterOutline({ ...m, blocks });

  function jumpToSection(gi: number) {
    const el = document.getElementById(`sec-${m.id}-${gi}`);
    if (el) el.scrollIntoView({ behavior: 'smooth', block: 'start' });
  }

  function addSection() {
    s.requestPrompt({
      title: `Name this section in chapter ${m.n}`,
      label: 'Section name',
      defaultValue: 'New section',
      submitLabel: 'Add section',
      onSubmit: (v) => s.addSection(v),
    });
  }

  /* Notes are meant to be taken while reading, and the empty state on Home
     already told learners to do exactly that -- but the only button lived in
     the workbook. Attaching the note to the chapter on screen is what the copy
     promised. */
  function addNote() {
    s.requestPrompt({
      title: `Note on chapter ${m.n}`,
      label: 'Note',
      placeholder: 'What is worth remembering here?',
      submitLabel: 'Save note',
      onSubmit: (v) => s.addNote(v),
    });
  }

  return (
    <div className="lessonlayout">
      <div className="lessonside">
        <div className="sidehead">COURSE · {(course.modules || []).length} CHAPTERS</div>
        <strong className="sidecourse-title">{course.title}</strong>
        <div className="sideprogress-label">Chapter {m.n} of {mods.length} · {pct}%</div>
        <div className="progressline"><span style={{ width: `${pct}%` }}></span></div>
        <div>
          {mods.map((mm, i) => (
            <button
              key={mm.id}
              className={`lessonnav${i === activeLesson ? ' active' : ''}${progress.done[mm.id] ? ' done' : ''}`}
              onClick={() => s.openLessonIn(course.id, i)}
            >
              <span className="bullet">{progress.done[mm.id] ? '✓' : i + 1}</span>
              <span className="lessonnavtext">{mm.title}</span>
            </button>
          ))}
        </div>
        <button className="small ghost mt-18" onClick={() => s.showScreen('course')}><Icon name="arrow-left" />Course home</button>
      </div>

      <div className="lessonmain" ref={mainRef}>
        <div className="lessonheading">
          <div>
            <div className="eyebrow">Chapter {m.n} · {m.kind}</div>
            <h1>{m.title}</h1>
            <p>{m.summary}</p>
            <ul className="objectives">
              {(m.objectives || []).map((o, i) => (
                <li key={i}><Icon name="check" /><span>{o}</span></li>
              ))}
            </ul>
            {/* Design 14 (M2 exit gate): a lesson with no graded exercise says
                why it is reading-only, in the same place the objectives are. */}
            {m.readingOnly ? (
              <p className="readingonlynote"><Icon name="note" /><span>{m.readingOnly}</span></p>
            ) : null}
          </div>
          <div className="actions">
            <button onClick={() => s.setTablesOpen(true)}><Icon name="table" />Tables</button>
            <button onClick={addNote}><Icon name="note" />Add note</button>
            {/* Authoring controls are admin-only; learners keep notes, which are
                their own record, but cannot rewrite the chapter. */}
            {s.role === 'admin' && (
              <>
                <button onClick={addSection}><Icon name="plus" />Add section</button>
                <button onClick={() => s.openModal('editLesson')}><Icon name="edit" />Edit chapter</button>
              </>
            )}
          </div>
        </div>

        <article className="reader">
          <div className="chapterindex">
            <h3>In this chapter</h3>
            <div className="idxrows">
              {outline.map((g, i) => {
                const ec = g.exIds.length;
                return (
                  <button className="idxrow" key={i} onClick={() => jumpToSection(i)}>
                    <span className="idxnum">{i + 1 < 10 ? '0' : ''}{i + 1}</span>
                    <span className="idxlabel">{g.title}</span>
                    <span className="idxmeta">{ec ? `${ec} ex${ec > 1 ? 's' : ''}` : `${g.items.length} part${g.items.length === 1 ? '' : 's'}`}</span>
                  </button>
                );
              })}
            </div>
          </div>

          {outline.map((g, gi) => {
            const exCount = g.exIds.length;
            return (
              <React.Fragment key={gi}>
                <div className="sec-head" id={`sec-${m.id}-${gi}`}>
                  <span className="sec-kicker">SECTION {gi + 1}</span>
                  <h2>{g.title}</h2>
                  <span className="sec-meta">{exCount ? `${exCount} exercise${exCount > 1 ? 's' : ''}` : `${g.items.length} part${g.items.length === 1 ? '' : 's'}`}</span>
                  {/* Design §10: bookmarking a section is what the workbook's
                      bookmark list points back at, so the control lives on the
                      section it marks rather than somewhere global. */}
                  <button
                    className={`small ghost secbookmark${s.isBookmarked(m.n, gi) ? ' on' : ''}`}
                    aria-pressed={s.isBookmarked(m.n, gi)}
                    title={s.isBookmarked(m.n, gi) ? 'Remove this bookmark' : 'Bookmark this section'}
                    onClick={() => s.toggleBookmark(g.title, m.n, gi)}
                  >
                    <Icon name="bookmark" />{s.isBookmarked(m.n, gi) ? 'Bookmarked' : 'Bookmark'}
                  </button>
                </div>
                {g.items.map((b: OutlineBlock, bi) => {
                  if (b.type === 'prose') {
                    return b.markdown
                      ? <Markdown key={bi} source={b.html as string} />
                      : <p key={bi} dangerouslySetInnerHTML={{ __html: b.html as string }} />;
                  }
                  if (b.type === 'list')
                    return (
                      <ul key={bi}>
                        {b.items.map((it, ii) => <li key={ii} dangerouslySetInnerHTML={{ __html: it }} />)}
                      </ul>
                    );
                  if (b.type === 'syntax')
                    return (
                      <React.Fragment key={bi}>
                        <pre className="syntaxblock">{b.sql}</pre>
                        {b.note ? <div className="blocknote">{b.note}</div> : null}
                      </React.Fragment>
                    );
                  if (b.type === 'example' || b.type === 'exercise') return <SqlCell key={bi} block={b} module={m} course={course} />;
                  return null;
                })}
              </React.Fragment>
            );
          })}
        </article>

        <div className="bottomlesson">
          <button disabled={activeLesson === 0} onClick={() => s.stepLesson(-1)}><Icon name="arrow-left" />Previous</button>
          <button className="ghost" onClick={s.markLessonDone}><Icon name="check" />{progress.done[m.id] ? 'Completed' : 'Mark chapter complete'}</button>
          {activeLesson === mods.length - 1 ? (
            <button className="primary" onClick={() => s.stepLesson(1)}>Finish<Icon name="check" /></button>
          ) : (
            <button className="primary" onClick={() => s.stepLesson(1)}>Next chapter<Icon name="arrow-right" /></button>
          )}
        </div>
      </div>
    </div>
  );
}
