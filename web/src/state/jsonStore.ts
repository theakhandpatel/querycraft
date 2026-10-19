/* JSON over localStorage, guarded at both ends. A refused or full store is not
   a crash, and a value that will not parse reads as absent. The provider and the
   progress store read and write the same keys, so the two guards live here
   once. */

export function loadJSON<T>(k: string, fallback: T): T {
  try {
    const v = localStorage.getItem(k);
    return v == null ? fallback : JSON.parse(v);
  } catch {
    return fallback;
  }
}

export function saveJSON<T>(k: string, v: T): void {
  try {
    localStorage.setItem(k, JSON.stringify(v));
  } catch {}
}
