import { dialectFor, dialectLabel } from './dialect';
import type { Course, CourseModule, Dialect } from '../types';

/* The learner's dialect choice (M6 step 7), and the one rule for naming an
 * engine in the UI.
 *
 * A chapter is authored for exactly one dialect, and the engine the app is
 * talking to may be a different one. The UI says both facts out loud; when they
 * disagree, Run is refused with a single control that fixes it. That control
 * records a *local* choice — "run this chapter on the dialect this engine
 * serves" — because the adapter itself is chosen once at boot
 * (`VITE_API_MODE`, or the single binary's own-origin probe) and cannot be
 * swapped underneath an open chapter. The choice is a preference the learner
 * made, not curriculum data, so it lives in localStorage beside `qc.theme` and
 * `qc.state`, keyed by module id, and a reload keeps it: a chapter must not
 * silently flip back to a dialect the engine cannot serve while the learner has
 * it open.
 *
 * Nothing here executes SQL and nothing here invents a capability: the badge
 * and the refusal read the running engine's own `capabilities()` answer
 * (through the store), and this module only decides which words are printed for
 * a dialect and which one a learner asked for.
 *
 * The precedence itself lives in `lib/dialect.ts` (`dialectFor`, M6 step 6): the
 * learner's choice is handed to it as the *explicit* dialect, so a switched
 * chapter resolves through exactly one resolver — the same one the adapter
 * reads when it decides which engine runs the SQL. */

/** Where the choice is kept. A preference, not course content. */
const CHOICE_KEY = 'qc.dialectChoice';

type ChoiceMap = Record<string, Dialect>;

let cache: ChoiceMap | null = null;
let version = 0;
const listeners = new Set<() => void>();

function isDialectValue(value: unknown): value is Dialect {
  return value === 'postgresql' || value === 'sqlite';
}

/** The stored choices, decoded once. An unreadable entry is dropped rather
    than guessed at, so a hand-edited localStorage cannot put the UI on a
    dialect the app does not have. */
function load(): ChoiceMap {
  if (cache) return cache;
  const map: ChoiceMap = {};
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      const raw = window.localStorage.getItem(CHOICE_KEY);
      if (raw) {
        const parsed: unknown = JSON.parse(raw);
        if (parsed && typeof parsed === 'object') {
          for (const [id, value] of Object.entries(parsed as Record<string, unknown>)) {
            if (isDialectValue(value)) map[id] = value;
          }
        }
      }
    }
  } catch (e) {
    /* storage unavailable or holding junk: no choice is the honest default */
  }
  cache = map;
  return map;
}

function save(next: ChoiceMap): void {
  cache = next;
  version++;
  try {
    if (typeof window !== 'undefined' && window.localStorage) {
      window.localStorage.setItem(CHOICE_KEY, JSON.stringify(next));
    }
  } catch (e) {
    /* the choice still applies for this session; it just will not survive */
  }
  for (const listener of listeners) listener();
}

/** The dialect the learner chose for one chapter, or null when they chose
    none. (`dialectChoice` itself is the resolver in `lib/dialect.ts`; this is
    the stored preference that resolver is given.) */
export function storedDialectChoice(moduleId?: string | null): Dialect | null {
  if (!moduleId) return null;
  return load()[moduleId] ?? null;
}

/** Record a learner's choice for a chapter. Persisted, and the change is
    pushed to every mounted screen through `subscribeDialectChoice`, so the
    badge, the refusal and the Run control cannot disagree after the click. */
export function chooseDialect(moduleId: string, dialect: Dialect): void {
  const current = load();
  if (current[moduleId] === dialect) return;
  save({ ...current, [moduleId]: dialect });
}

/** Forget a learner's choice for a chapter, putting it back on the dialect it
    was authored for. */
export function resetDialectChoice(moduleId: string): void {
  const current = load();
  if (!(moduleId in current)) return;
  const next = { ...current };
  delete next[moduleId];
  save(next);
}

/** The dialect a chapter runs on: the learner's own choice when they made one,
    otherwise the chapter's authored dialect — block → module → course → the app
    default, resolved by `dialectFor` so this screen and the adapter never
    disagree about which engine the SQL goes to. */
export function chapterDialect(mod?: CourseModule | null, course?: Course | null): Dialect {
  return dialectFor({ dialect: storedDialectChoice(mod?.id) ?? undefined, module: mod, course });
}

/** React 18's `useSyncExternalStore` contract: a stable snapshot that changes
    when the choice does. */
export function dialectChoiceVersion(): number {
  return version;
}

export function subscribeDialectChoice(listener: () => void): () => void {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
}

/** What a screen may print as the engine behind a dialect.
 *
 * `dialect` is the contract's machine-readable answer and it always wins: the
 * engine's own free text is shown only when it names that same dialect, so a
 * result row, a badge or a tooltip can never read "PostgreSQL" for an engine
 * that reported `sqlite` (or the reverse). `null`, an empty string and an
 * unrecognised label all fall back to the dialect's own word. */
export function engineLabelFor(dialect: Dialect, engine?: string | null): string {
  const text = (engine ?? '').trim();
  if (!text) return dialectLabel(dialect);
  const names = dialect === 'sqlite' ? 'sqlite' : 'postgres';
  return text.toLowerCase().startsWith(names) ? text : dialectLabel(dialect);
}

/** The dialects an engine can serve, in contract order: a gateway advertises
    the set, a single-engine adapter answers with the one it runs. `null` while
    the engine has not answered — the caller must not disable anything then. */
export function servedDialects(caps: { dialect: Dialect; dialects?: Dialect[] } | null): Dialect[] | null {
  if (!caps) return null;
  const advertised = (caps.dialects ?? []).filter(isDialectValue);
  return advertised.length > 0 ? advertised : [caps.dialect];
}

/** The one dialect the engine would run this chapter on if the learner accepts
    the fix, or null when nothing needs fixing. It prefers the dialect the
    engine itself runs, so the switch lands on the engine that is actually
    serving the page. */
export function switchTarget(dialect: Dialect, caps: { dialect: Dialect; dialects?: Dialect[] } | null): Dialect | null {
  const served = servedDialects(caps);
  if (!caps || !served || served.includes(dialect)) return null;
  return served.includes(caps.dialect) ? caps.dialect : served[0];
}
