import React, { useEffect, useRef } from 'react';
import Icon from './ui/Icon';
import Schema from './Schema';
import { useStore } from '../state/store';

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
  const asideRef = useRef<HTMLElement | null>(null);
  const restoreRef = useRef<HTMLElement | null>(null);

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
            <h2>commerce · dataset_version 1</h2>
            <p className="muted" style={{ fontSize: 12, marginTop: 4 }}>Generated with this course · 4 tables · ~290 rows</p>
          </div>
          <button className="small ghost" aria-label="Close" onClick={() => s.setTablesOpen(false)}><Icon name="x" /></button>
        </div>
        <div className="drawerbody">
          {s.tablesOpen ? <Schema api={s.api} /> : null}
        </div>
      </aside>
    </>
  );
}
