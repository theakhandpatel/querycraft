/* A pause a cancel can cut short, so leaving a screen never waits out the
   interval before it can stop. Both the build's poll gap and the lab's pacing
   use it, so a cancel behaves the same on either. */
export function sleep(ms: number, signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    if (signal.aborted) {
      resolve();
      return;
    }
    function done(): void {
      clearTimeout(timer);
      signal.removeEventListener('abort', done);
      resolve();
    }
    const timer = setTimeout(done, ms);
    signal.addEventListener('abort', done, { once: true });
  });
}
