/* Jumping to a node inside the app shell.
 *
 * The shell scrolls `main.workspace`, not the document, so a jump has to move
 * that container — and `scrollIntoView` is the only call that gets the whole
 * ancestor chain right without re-deriving the layout here.
 *
 * The reason this is a function and not a bare `scrollIntoView` at each call
 * site: `{behavior:'smooth'}` is **silently a no-op** in any host that does not
 * animate scrolling — a browser invoked with smooth scrolling disabled, a
 * headless host, a remote-viewer tab that never composites. The click then does
 * nothing at all, which is worse than a jump the learner did not expect, and it
 * is invisible to every audit in this repo because none of them renders a page.
 * (Measured here: with `behavior:'smooth'` the container's `scrollTop` stayed at
 * 0 through twelve 200 ms samples; the same call without it landed at once, with
 * the target's top at 16 px.)
 *
 * So: ask for the animation, then check that it happened, and fall back to the
 * instant jump. In a host that animates, the fallback never fires — by the time
 * the timer runs the container has already moved — and the learner sees the
 * glide they saw before.
 */
const SMOOTH_SETTLE_MS = 260;

/** The scroll container the shell uses, or null when the node is not inside it. */
function scrollerFor(node: HTMLElement): HTMLElement | null {
  return node.closest<HTMLElement>('.workspace');
}

/**
 * Scroll `node` to the top of its container, animating when the host allows it.
 *
 * Returns nothing: the outcome of a scroll is whether the node ended up in
 * view, not a value, and the caller has already navigated by the time this runs.
 */
export function scrollToNode(node: HTMLElement | null): void {
  if (!node) return;

  /* The Appearance screen's reduced-motion setting is an explicit request to
     stop animating things, and an animated jump is one of them. */
  const reduced = document.documentElement.getAttribute('data-motion') === 'reduced';
  if (reduced) {
    node.scrollIntoView({ block: 'start' });
    return;
  }

  const scroller = scrollerFor(node);
  const read = () => (scroller ? scroller.scrollTop : window.scrollY);
  const before = read();

  node.scrollIntoView({ behavior: 'smooth', block: 'start' });

  window.setTimeout(() => {
    /* Still where it was a quarter of a second in: the host is not animating.
       Jump instead of leaving the learner at the top of a chapter they just
       asked to be taken into the middle of. */
    if (read() === before) node.scrollIntoView({ block: 'start' });
  }, SMOOTH_SETTLE_MS);
}
