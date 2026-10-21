import React, { useState } from 'react';
import Icon from '../ui/Icon';
import { useStore } from '../../state/store';
import ImportControl from '../ui/ImportControl';
import { activeModules, lessonPercent, nextChapterIndex } from '../../lib/model';
import type { SavedItem } from '../../types';

export default function WorkbookScreen() {
  const s = useStore();
  const { course, progress } = s;
  const pct = lessonPercent(course, progress);
  const mods = activeModules(course);
  const nextIdx = nextChapterIndex(course, progress);
  const complete = mods.length > 0 && mods.every((m) => !!progress.done[m.id]);
  const notes = (progress.notes || []).filter((n) => n.course === course.id);
  const saved = (progress.saved || []).filter((q) => q.course === course.id);

  /* A kept query re-runs through the same seam every screen uses (§10), so the
     workbook cannot show a result the engine did not produce. */
  const [savedRun, setSavedRun] = useState<{ ts: number; state: 'running' | 'done' | 'error'; rows?: number; cols?: string[]; message?: string } | null>(null);

  async function runSaved(q: SavedItem) {
    const sql = String(q.sql || '').trim();
    const ts = q.ts ?? -1;
    if (!sql) {
      s.toast('This kept query has no stored SQL, so there is nothing to run.');
      return;
    }
    setSavedRun({ ts, state: 'running' });
    try {
      /* The workbook is the learner's own scratchpad on the shared commerce
         workspace: it belongs to no course, so it names no dataset and the
         adapter's default answers. */
      const res = await s.api.runQuery(sql);
      if (res.status === 'succeeded') {
        setSavedRun({ ts, state: 'done', rows: res.row_count, cols: res.columns.map((c) => c.name) });
      } else {
        setSavedRun({ ts, state: 'error', message: res.error?.code || 'QUERY_FAILED' });
      }
    } catch (e) {
      setSavedRun({ ts, state: 'error', message: (e as { code?: string } | null)?.code || 'ENVIRONMENT_UNAVAILABLE' });
    }
  }

  function copySaved(q: SavedItem) {
    const sql = String(q.sql || '');
    const clipboard = typeof navigator === 'undefined' ? undefined : navigator.clipboard;
    if (!clipboard) {
      s.toast('Copy is not available in this browser.');
      return;
    }
    clipboard.writeText(sql).then(
      () => s.toast('Query copied.'),
      () => s.toast('Copy was blocked by the browser.'),
    );
  }

  return (
    <div className="content">
      <div className="wbhero">
        <div>
          <div className="eyebrow">Your workbook</div>
          <h1>{course.title}</h1>
          <p>Notes, bookmarks and saved queries, kept per course.</p>
        </div>
      </div>

      <div className="mt-16">
        <div className="card">
          <div className="sectiontitle">
            <h2>Continue</h2>
            <div className="actions">
              <button className="small ghost" onClick={s.exportData} title="Download a versioned JSON copy of your data"><Icon name="download" />Export</button>
              <ImportControl className="small ghost" />
              <button className="small primary" onClick={complete ? () => s.showScreen('course') : s.resumeCourse}>
                <Icon name={complete ? 'check' : 'play'} />{complete ? 'Course complete' : `Resume chapter ${nextIdx + 1}`}
              </button>
            </div>
          </div>
          <div className="reportrow mt-8"><span>Current course</span><span>{course.title}</span></div>
          <div className="reportrow"><span>{complete ? 'Status' : 'Next chapter'}</span><span>{complete ? 'Course complete' : mods[nextIdx] ? mods[nextIdx].title : '—'}</span></div>
          <div className="meter"><span style={{ width: `${pct}%` }}></span></div>
          <p className="muted mt-8" style={{ fontSize: 12 }}>{
            s.api.mode === 'http'
              ? `Drafts, attempts and notes are saved to ${s.api.baseUrl} and survive a reload. Export writes a versioned JSON copy you can keep; Import merges one back in without overwriting anything.`
              : 'Drafts, attempts and notes are stored locally and survive a reload. Export writes a versioned JSON copy you can keep; Import merges one back in without overwriting anything.'
          }</p>
        </div>
      </div>

      <div className="grid2 mt-16">
        <div className="card">
          <div className="sectiontitle">
            <h2>Notes &amp; bookmarks</h2>
            <button className="small ghost" onClick={() => s.requestPrompt({
              title: 'Add a note',
              label: 'Note',
              placeholder: `Note for ${course.title} · chapter ${s.activeLesson + 1}`,
              submitLabel: 'Save note',
              onSubmit: (v) => s.addNote(v),
            })}><Icon name="plus" />Add a note</button>
          </div>
          <div className="mt-8">
            {notes.length === 0 ? (
              <div className="empty">No notes in this course yet. Add one while reading a chapter.</div>
            ) : (
              notes.map((n) => (
                <div className="reportrow" key={`${n.kind}-${n.ts}`}>
                  <span><Icon name={n.kind === 'bookmark' ? 'bookmark' : 'note'} style={{ verticalAlign: -2 }} /> {n.text}</span>
                  <span className="muted">
                    Chapter {n.lesson}
                    {/* A bookmark knows where it came from, so it can be opened
                        again; removing either kind is one action. */}
                    {n.kind === 'bookmark' ? (
                      <button className="small ghost" style={{ marginLeft: 8 }} onClick={() => s.openBookmark(n.lesson, n.section)}><Icon name="arrow-right" />Go</button>
                    ) : null}
                    <button className="small ghost" style={{ marginLeft: 8 }} title="Remove from your workbook" aria-label="Remove from your workbook" onClick={() => s.removeWorkbookItem(n.ts)}><Icon name="x" /></button>
                  </span>
                </div>
              ))
            )}
          </div>
        </div>
        <div className="card">
          <h2>Saved queries</h2>
          {saved.length === 0 ? (
            <div className="empty">No saved queries in this course yet. Keep a query or a reference solution and it appears here.</div>
          ) : (
            saved.map((q) => (
              <React.Fragment key={q.ts ?? `${q.course}-${q.lesson}-${q.name}`}>
                <div className="reportrow">
                  <span className="mono" style={{ fontSize: 12 }}>{q.name}</span>
                  <span className="muted">
                    Chapter {q.lesson}
                    <button className="small ghost" style={{ marginLeft: 8 }} onClick={() => runSaved(q)}>Run</button>
                    <button className="small ghost" style={{ marginLeft: 8 }} onClick={() => copySaved(q)}>Copy</button>
                    <button className="small ghost" style={{ marginLeft: 8 }} title="Remove saved query" aria-label="Remove saved query" onClick={() => s.removeSaved(q.ts ?? -1)}><Icon name="x" /></button>
                  </span>
                </div>
                {savedRun && savedRun.ts === q.ts ? (
                  <div className="blocknote">
                    {savedRun.state === 'running' ? (
                      <><span className="spin"></span> Running through {s.api.config.engine}…</>
                    ) : savedRun.state === 'error' ? (
                      <>Ran and failed: <span className="fbcode">{savedRun.message}</span></>
                    ) : (
                      <>Ran · {savedRun.rows} row{savedRun.rows === 1 ? '' : 's'} · {savedRun.cols && savedRun.cols.join(', ')}</>
                    )}
                  </div>
                ) : null}
              </React.Fragment>
            ))
          )}
          <button className="small ghost mt-12" onClick={() => s.showScreen('workbook-progress')}><Icon name="clock" />See full history</button>
        </div>
      </div>
    </div>
  );
}
