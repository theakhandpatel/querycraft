import { useEffect, useRef } from 'react';

/*
 * The focus contract every drawer in this app shares, extracted from
 * TablesDrawer.tsx's proven mechanics.
 *
 * While closed, the panel is out of the tab order and out of the a11y tree
 * (`inert`, set imperatively because the attribute is newer than the JSX types
 * React 18 ships with). While open, the first control takes focus, Tab is
 * trapped in capture phase with a visibility-aware candidate list, focus that
 * somehow escaped the panel is pulled back in, and closing restores focus to
 * the opener — or to the main region when the opener has left the document,
 * which happens when the drawer's own button changed the screen.
 */
const FOCUSABLE = 'a[href],button:not([disabled]),input,select,textarea,[tabindex]:not([tabindex="-1"])';

/* `position: fixed` panels and SVG icons both drop real controls and keep dead
   ones from the query alone. Client rects plus `visibility` is what the browser
   itself uses: `display: none` and collapsed boxes have no rects, and
   `visibility: hidden` is excluded by name. */
function focusable(el: HTMLElement): boolean {
  return el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
}

export function useDrawerFocus(open: boolean, ref: React.RefObject<HTMLElement | null>) {
  const openedRef = useRef(false);

  useEffect(() => {
    const root = ref.current;
    if (!root) return;
    if (open) root.removeAttribute('inert');
    else root.setAttribute('inert', '');
  }, [open, ref]);

  useEffect(() => {
    if (!open) return;
    const opener = document.activeElement as HTMLElement | null;
    openedRef.current = true;
    const restoreTo = opener && opener !== document.body ? opener : null;

    const root = ref.current;
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
      /* The opener may have unmounted (a drawer button that navigated), so the
         guard falls back to the main region instead of dropping the keyboard
         learner at the top of the document. */
      if (restoreTo && document.contains(restoreTo) && openedRef.current) restoreTo.focus();
      else document.getElementById('main')?.focus({ preventScroll: true });
      openedRef.current = false;
    };
  }, [open, ref]);
}