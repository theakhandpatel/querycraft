import { useEffect, useRef, useState } from 'react';
import Icon from './ui/Icon';
import Schema from './Schema';
import { useChapterDialect } from './ui/DialectBadge';
import { useStore } from '../state/store';
import { datasetFor } from '../lib/dataset';
import { datasetVersion } from '../api/dataset';
import { dialectLabel } from '../lib/dialect';
import { DEFAULT_DATASET } from '../types';
import type { EnvironmentInfo } from '../types';

/** Everything the drawer can put a focus ring on. */
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Whether an element would receive focus from the browser.
 *
 * `offsetParent` is the usual shortcut, but it is null for anything inside a
 * `position: fixed` panel and for SVG, so it both drops real controls and keeps
 * dead ones. Client rects plus `visibility` is what the browser itself uses:
 * `display: none` and collapsed boxes have no rects, `visibility: hidden` is
 * excluded by name.
 */
function focusable(el: HTMLElement): boolean {
  return el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
}

/**
 * The tables drawer, as a modal dialog.
 *
 * A panel that covers the page has to behave like one for the keyboard:
 *
 *   - Focus moves into it when it opens, and Tab cycles inside it rather than
 *     walking into the page behind, which the learner cannot see.
 *   - Focus goes back to whatever opened it when it closes, so the learner
 *     resumes where they were instead of at the top of the document.
 *   - While closed it carries `inert`, so its controls are neither focusable nor
 *     read out — the slide-out is a visual transition, not a live region.
 */
export default function TablesDrawer() {
  const s = useStore();
  /* M5. The drawer describes — and resets — the workspace of the course the
     learner is in, not a hard-coded one. `plans` runs on the larger fixture, so
     a drawer that listed commerce here would show the schema next to row counts
     the course's exercises are not graded against. */
  const dataset = datasetFor({ course: s.course });
  /* M6 steps 6/7: the drawer describes the workspace of the chapter the learner
     has open, so it resolves the dialect through the same call the badge and the
     adapter use. Without this a SQLite chapter's drawer would list the
     PostgreSQL relations and "restore my data" would reset the other engine. */
  const activeModule = s.modules ? s.modules[s.activeLesson] ?? null : null;
  const dialect = useChapterDialect(activeModule, s.course);
  const asideRef = useRef<HTMLElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

  /* ADR 0003: the learner's SQL may write, so the tables they see are their own
     workspace and the drawer is where a reset belongs — this is the screen
     about "my data". The reset is destructive, so it takes two presses: the
     first asks, the second does it. */
  const [resetStep, setResetStep] = useState<'idle' | 'confirm' | 'busy'>('idle');
  const [resetError, setResetError] = useState<string | null>(null);
  const [resetNote, setResetNote] = useState<string | null>(null);
  /* Focus follows the control the learner just used: the second press must be a
     keypress away, not a Tab hunt. `wasActive` keeps the effect from stealing
     focus when the drawer simply opens. */
  const idleResetRef = useRef<HTMLButtonElement | null>(null);
  const confirmResetRef = useRef<HTMLButtonElement | null>(null);
  const resetWasActive = useRef(false);
  /* Remount Schema after a reset so the drawer lists the restored tables, not
     the ones the learner replaced them with. */
  const [schemaNonce, setSchemaNonce] = useState(0);
  /* undefined = still asking the adapter; null = no per-learner workspace
     (the shared, read-only dataset, or canned results). */
  const [env, setEnv] = useState<EnvironmentInfo | null | undefined>(undefined);

  useEffect(() => {
    if (!s.tablesOpen) return;
    let alive = true;
    setEnv(undefined);
    s.api
      .getEnvironment()
      .then((next) => {
        if (alive) setEnv(next);
      })
      .catch(() => {
        if (alive) setEnv(null);
      });
    return () => {
      alive = false;
    };
  }, [s.api, s.tablesOpen, schemaNonce]);

  /* A confirmation left open while the drawer closed must not be primed to fire
     on the next open; the learner would have no memory of asking. On open, the
     outcome of an earlier reset is cleared for the same reason — a stale
     "restored in 222 ms" beside a drawer that has just opened, and reset
     nothing, reads as if it happened now. */
  useEffect(() => {
    if (s.tablesOpen) {
      setResetError(null);
      setResetNote(null);
    } else {
      setResetStep((step) => (step === 'confirm' ? 'idle' : step));
    }
  }, [s.tablesOpen]);

  useEffect(() => {
    if (resetStep === 'confirm') {
      resetWasActive.current = true;
      confirmResetRef.current?.focus();
    } else if (resetStep === 'idle' && resetWasActive.current) {
      resetWasActive.current = false;
      /* While closed the footer is inert, so this is a no-op — and the drawer's
         own close handler is the one that returns focus to the opener. */
      idleResetRef.current?.focus();
    }
  }, [resetStep]);

  async function resetWorkspace(): Promise<void> {
    setResetStep('busy');
    setResetError(null);
    setResetNote(null);
    try {
      /* A stored course's environment is the sealed dataset plus that course's
         own setup, so the reset names it and the adapter rebuilds both. A
         built-in course has no such record, so it names none and the request is
         the one it always was. */
      const course = s.course.gateway === true ? s.course : null;
      const out = await s.api.resetEnvironment(dataset, dialect, course);
      setResetNote(`Workspace restored in ${Math.round(out.duration_ms)} ms.`);
      setSchemaNonce((n) => n + 1);
      s.toast('Workspace restored to the original dataset.');
    } catch (e) {
      /* Show the gateway's own words: it knows why (no environment, expired
         session, a busy server) far better than a generic string here. */
      setResetError(e instanceof Error && e.message ? e.message : 'The workspace could not be reset.');
    } finally {
      setResetStep('idle');
    }
  }

  /* While closed, the panel is out of the tab order and out of the a11y tree.
     Done imperatively because `inert` is a newer attribute than the JSX types
     React 18 ships with. */
  useEffect(() => {
    const root = asideRef.current;
    if (!root) return;
    if (s.tablesOpen) root.removeAttribute('inert');
    else root.setAttribute('inert', '');
  }, [s.tablesOpen]);

  useEffect(() => {
    if (!s.tablesOpen) return;
    const opener = document.activeElement as HTMLElement | null;
    restoreRef.current = opener && opener !== document.body ? opener : null;

    const root = asideRef.current;
    const first = root?.querySelector<HTMLElement>(FOCUSABLE);
    (first ?? root)?.focus();

    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Tab' || !root) return;
      const items = Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(focusable);
      if (items.length === 0) return;
      const firstEl = items[0];
      const lastEl = items[items.length - 1];
      const current = document.activeElement as HTMLElement | null;
      const inside = !!current && root.contains(current);
      if (e.shiftKey && (!inside || current === firstEl)) {
        e.preventDefault();
        lastEl.focus();
      } else if (!e.shiftKey && (!inside || current === lastEl)) {
        e.preventDefault();
        firstEl.focus();
      }
    }
    /* Capture phase: the trap has to see the key before the page-level Escape
       handler and before the browser moves focus on its own. */
    document.addEventListener('keydown', onKeyDown, true);
    return () => {
      document.removeEventListener('keydown', onKeyDown, true);
      const back = restoreRef.current;
      if (back && document.contains(back)) back.focus();
    };
  }, [s.tablesOpen]);

  return (
    <>
      <div
        className={`drawer-back${s.tablesOpen ? ' open' : ''}`}
        onClick={() => s.setTablesOpen(false)}
        aria-hidden="true"
      ></div>
      <aside
        ref={asideRef}
        className={`drawer${s.tablesOpen ? ' open' : ''}`}
        role="dialog"
        aria-modal="true"
        aria-label="Database tables"
        tabIndex={-1}
      >
        <div className="drawerhead">
          <div>
            <div className="eyebrow">Course dataset</div>
            <h2>{dataset} · {dialectLabel(dialect)} · dataset_version {datasetVersion}</h2>
            <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>
              {dataset === DEFAULT_DATASET
                ? 'Generated with this course · 4 tables · ~290 rows'
                : "The plan lab's larger fixture · 4 tables · row counts below"}
            </p>
          </div>
          <button className="small ghost" aria-label="Close" onClick={() => s.setTablesOpen(false)}><Icon name="x" /></button>
        </div>
        <div className="drawerbody">
          {s.tablesOpen ? <Schema key={schemaNonce} api={s.api} dataset={dataset} dialect={dialect} /> : null}
        </div>
        <footer className="drawerfoot">
          <div className="eyebrow">Your workspace</div>
          <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>
            {env === undefined
              ? 'Checking the execution environment…'
              : env?.writable
                ? 'Your own database. Writes are kept for this session until you reset.'
                : s.api.mode === 'mock'
                  ? 'Canned results. There is no database to change, and nothing to reset.'
                  : 'The shared dataset. Your SQL cannot change it.'}
          </p>
          {resetStep === 'idle' ? (
            <div style={{ marginTop: 10 }}>
              <button
                ref={idleResetRef}
                className="small ghost"
                onClick={() => {
                  setResetError(null);
                  setResetNote(null);
                  setResetStep('confirm');
                }}
              >
                <Icon name="refresh" /> Reset workspace
              </button>
            </div>
          ) : (
            <div style={{ marginTop: 10 }}>
              <p className="muted" style={{ fontSize: 12, color: 'var(--red)' }}>
                This restores the original dataset and discards your own changes and tables. It cannot be undone.
              </p>
              <div style={{ display: 'flex', gap: 8, marginTop: 8, flexWrap: 'wrap' }}>
                <button
                  ref={confirmResetRef}
                  className="small"
                  disabled={resetStep === 'busy'}
                  onClick={() => void resetWorkspace()}
                  onKeyDown={(e) => {
                    if (e.key === 'Escape') setResetStep('idle');
                  }}
                >
                  <Icon name="refresh" /> {resetStep === 'busy' ? 'Resetting…' : 'Reset workspace'}
                </button>
                <button className="small ghost" disabled={resetStep === 'busy'} onClick={() => setResetStep('idle')}>
                  Cancel
                </button>
              </div>
            </div>
          )}
          {resetError || resetNote ? (
            <p
              className={resetError ? 'fail' : 'pass'}
              style={{ fontSize: 12, marginTop: 8 }}
              role="status"
              aria-live="polite"
            >
              {resetError ?? resetNote}
            </p>
          ) : null}
        </footer>
      </aside>
    </>
  );
}
