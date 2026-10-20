import React, { useEffect, useRef } from 'react';
import Icon from '../ui/Icon';
import SqlCell from '../SqlCell';
import Markdown from '../ui/Markdown';
import { useStore } from '../../state/store';
import DialectBadge, { useChapterDialect } from '../ui/DialectBadge';
import { chapterOutline, activeModules } from '../../lib/model';
import { accrueStudyTime } from '../../lib/progress';
import { scrollToNode } from '../../lib/scroll';
import type { Block } from '../../types';

type OutlineBlock = Block | { type: 'list'; items: string[] };

export default function LessonScreen() {
  const s = useStore();
  const { course, progress, activeLesson } = s;
  const mods = activeModules(course);
  const m = mods[activeLesson] || mods[0];
  const mainRef = useRef(null);
  /* M6 step 7: the dialect this chapter runs on, as the header badge. The
     choice store is subscribed inside the hook, so a switch in any cell is
     reflected here without a remount. */
  const dialect = useChapterDialect(m, course);

  /* `moduleId` names the value the scroll reset depends on; `m && m.id` hid it
     behind an expression, which is what React Doctor flags. */
  const moduleId = m?.id;
  useEffect(() => {
    const ws = document.querySelector('.workspace');
    if (ws) ws.scrollTop = 0;
  }, [activeLesson, moduleId]);

  /* Next and Previous replace the chapter under the same button. Move focus to
     the main region after a chapter change so Enter does not skip the new
     chapter. The first mount is left alone. */
  const firstChapter = useRef(true);
  useEffect(() => {
    if (firstChapter.current) {
      firstChapter.current = false;
      return;
    }
    document.getElementById('main')?.focus();
  }, [activeLesson, moduleId]);

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
    scrollToNode(document.getElementById(`sec-${m.id}-${gi}`));
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
    <div className="lessonmain" ref={mainRef}>
        <div className="lessonheading">
          <div>
            <div className="eyebrow">Chapter {m.n} · {m.kind}</div>
            {/* The dialect sits beside the chapter title, one word, in the same
                pill language as every other metadata chip. */}
            <div className="chaptertitle">
              <h1>{m.title}</h1>
              <DialectBadge dialect={dialect} />
            </div>
            <p>{m.summary}</p>
            <ul className="objectives">
              {/* A fixed-length authored list; the order is the curriculum's. */}
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
                  /* The index is the identity: jumpToSection(i) navigates to it. */
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
                  /* Keyed by the block's own id, never by its position. A cell
                     holds editable state (the draft, the result, the plan), and
                     a positional key made React reuse the chapter 1 cell when
                     chapter 3 has a cell in the same slot: the editor then
                     showed the previous chapter's SQL under this exercise's id,
                     so a learner could run and be graded on a query they never
                     wrote. Block ids carry their module prefix and are unique
                     across the curriculum. */
                  if (b.type === 'example' || b.type === 'exercise') return <SqlCell key={b.id ?? bi} block={b} module={m} course={course} />;
                  return null;
                })}
              </React.Fragment>
            );
          })}
        </article>

        <div className="bottomlesson">
          <button disabled={activeLesson === 0} onClick={() => s.stepLesson(-1)}><Icon name="arrow-left" />Previous</button>
          {progress.done[m.id] ? (
            <span className="pill pass chaptercomplete"><Icon name="check" />Completed</span>
          ) : (
            <button className="ghost" onClick={s.markLessonDone}><Icon name="check" />Mark chapter complete</button>
          )}
          {activeLesson === mods.length - 1 ? (
            <button className="primary" onClick={() => s.stepLesson(1)}>Finish<Icon name="check" /></button>
          ) : (
            <button className="primary" onClick={() => s.stepLesson(1)}>Next chapter<Icon name="arrow-right" /></button>
          )}
        </div>
      </div>
  );
}
