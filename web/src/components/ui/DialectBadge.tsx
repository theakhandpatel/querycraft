import { useEffect, useState, useSyncExternalStore } from 'react';
import Icon from './Icon';
import { useStore } from '../../state/store';
import { dialectLabel, dialectSupported } from '../../lib/dialect';
import {
  chapterDialect,
  chooseDialect,
  dialectChoiceVersion,
  servedDialects,
  subscribeDialectChoice,
  switchTarget,
} from '../../lib/dialectChoice';
import type { Course, CourseModule, Dialect, EngineCapabilities, ExecutionApi } from '../../types';

/* The dialect badge, and the refusal that goes with it (M6 step 7).
 *
 * The badge says which dialect a chapter is on; its tooltip says which engine
 * is actually running, read from the seam's own `capabilities()` answer rather
 * than a constant — so the UI can never say "SQLite" while the queries go to
 * PostgreSQL. While that answer is still `null` (an engine booting, a gateway
 * that has not replied) the tooltip says "starting…" and
 * `dialectSupported()` returns `null`: no control is disabled on a guess.
 *
 * The refusal is the same two facts, said out loud, next to the Run control
 * that is off because of them, plus the one control that fixes it. That control
 * records a local choice for the chapter (see `lib/dialectChoice`), which keeps
 * the learner's SQL exactly where it was: the choice changes which dialect the
 * chapter is being read as, never the editor's contents. */

/** One shared reader of the seam's capability answer, for a badge that mounts
    before the store's own read has landed. The store polls `capabilities()`
    itself and re-reads it on a session change; this is only the first call, and
    only while the store is silent. It caches nothing, so it cannot pin a stale
    answer in front of a later one. */
let pendingProbe: Promise<EngineCapabilities | null> | null = null;

function probe(api: ExecutionApi): Promise<EngineCapabilities | null> {
  if (!pendingProbe) {
    pendingProbe = api
      .capabilities()
      .catch(() => null)
      .then((caps) => {
        pendingProbe = null;
        return caps;
      });
  }
  return pendingProbe;
}

/** Where SQL actually runs, as the adapter reported it. `null` means the engine
    has not answered yet — never a claim about the engine. */
export function useEngineCapabilities(): EngineCapabilities | null {
  const s = useStore();
  const [probed, setProbed] = useState<EngineCapabilities | null>(null);
  const caps = s.engineCaps ?? probed;
  useEffect(() => {
    if (caps) return;
    let alive = true;
    probe(s.api).then((next) => {
      if (alive && next) setProbed(next);
    });
    return () => {
      alive = false;
    };
  }, [caps, s.api]);
  return caps;
}

/** The chapter's dialect: the learner's own choice when they made one, and the
    authored one otherwise. Subscribed to the choice store, so a switch made in
    one cell is reflected by the header badge and every other cell at once. */
export function useChapterDialect(mod?: CourseModule | null, course?: Course | null): Dialect {
  useSyncExternalStore(subscribeDialectChoice, dialectChoiceVersion, dialectChoiceVersion);
  return chapterDialect(mod, course);
}

export interface DialectSupport {
  /** Whether the engine can run this chapter. `null` until the engine has
      answered: the caller must not disable a control on a guess. */
  supported: boolean | null;
  /** The dialect the active engine itself runs, or null when unknown. */
  engine: Dialect | null;
  /** Every dialect the engine can serve, or null when unknown. */
  served: Dialect[] | null;
  /** The dialect the one fix switches this chapter to, or null when nothing
      needs fixing. */
  switchTo: Dialect | null;
  /** The badge's tooltip: the chapter's dialect plus the engine's honest state. */
  badgeTitle: string;
  /** The refusal sentence, or null when there is nothing to refuse. */
  refusal: string | null;
}

/** The words for where the engine ended up. `placement` is the adapter's own
    answer and is never softened. */
function placementWords(placement: EngineCapabilities['placement']): string {
  switch (placement) {
    case 'worker':
      return 'in a Web Worker';
    case 'main':
      return 'on the main thread';
    case 'server':
      return 'on the gateway';
    default:
      return 'nowhere';
  }
}

/** "PostgreSQL", "PostgreSQL or SQLite", "PostgreSQL, SQLite or …" */
function joinLabels(labels: string[]): string {
  if (labels.length <= 1) return labels[0] ?? '';
  return `${labels.slice(0, -1).join(', ')} or ${labels[labels.length - 1]}`;
}

/** Everything the badge and the refusal need, from the seam's answer alone. */
export function useDialectSupport(dialect: Dialect): DialectSupport {
  const caps = useEngineCapabilities();
  const label = dialectLabel(dialect);
  const served = servedDialects(caps);
  const supported = dialectSupported(dialect, served);
  const engine = caps ? caps.dialect : null;
  const servedWords = served ? joinLabels(served.map(dialectLabel)) : '';
  const switchTo = switchTarget(dialect, caps);

  /* The engine's own version string ("PostgreSQL 18 · PGlite (in-browser)") is
     deliberately not in the badge: one adapter can host two engines, so a
     label that names one of them would be wrong the moment the chapter is on
     the other. The badge names the *dialects* the engine serves and where it
     runs, which is the same statement for every chapter. */
  let badgeTitle: string;
  if (!caps || !served) {
    badgeTitle = `Engine starting… — the running engine has not answered yet, so this chapter's dialect (${label}) is not confirmed.`;
  } else if (caps.placement === 'none') {
    badgeTitle = `${label} — no engine runs here; this adapter returns canned results.`;
  } else if (supported) {
    badgeTitle = `${label} — the active engine serves ${servedWords} ${placementWords(caps.placement)}.`;
  } else {
    badgeTitle = `This chapter is ${label}. The active engine serves ${servedWords}, not ${label}.`;
  }

  /* The refusal names the dialect the engine *can* run — the `dialects` the
     seam reported, which is what the fix switches the chapter onto. It is
     deliberately built from the served set rather than `caps.dialect`: a
     gateway can advertise both dialects while its own `dialect` field names
     one, and "the engine is X but can serve Y" would confuse the two. */
  const refusal =
    supported === false && served
      ? `This chapter is ${label}. The active engine can serve ${joinLabels(
          served.map(dialectLabel)
        )}, not ${label}, so Run is off.`
      : null;

  return { supported, engine, served, switchTo, badgeTitle, refusal };
}

/** One word for the dialect a chapter runs on, in the app's existing pill
    idiom, with the engine's own state as its tooltip. */
export default function DialectBadge({ dialect, className }: { dialect: Dialect; className?: string }) {
  const support = useDialectSupport(dialect);
  const state = support.supported === null ? 'starting' : support.supported ? 'served' : 'mismatch';
  return (
    <span
      className={`dialbadge ${state}${className ? ` ${className}` : ''}`}
      title={support.badgeTitle}
      data-dialect={dialect}
      data-dialect-state={state}
      data-engine={support.engine ?? ''}
    >
      <Icon name="database" />
      {dialectLabel(dialect)}
    </span>
  );
}

/** The refusal: why Run is off, the dialect the engine can run, and the single
    control that switches this chapter onto it. The switch is a local choice
    (persisted — see `lib/dialectChoice`), and the cell's own draft is never
    touched: the editor keeps whatever the learner typed. */
export function DialectRefusal({
  id,
  moduleId,
  dialect,
  support,
  onSwitched,
}: {
  /** Ties the Run control to the reason through `aria-describedby`. */
  id?: string;
  moduleId: string;
  dialect: Dialect;
  support: DialectSupport;
  onSwitched?: (to: Dialect) => void;
}) {
  if (support.supported !== false || !support.switchTo || !support.refusal) return null;
  const to = support.switchTo;
  return (
    <div className="dialrefusal" id={id} role="note" data-dialect={dialect} data-engine={support.engine ?? ''}>
      <Icon name="alert" />
      <span>{support.refusal}</span>
      <button
        className="small"
        onClick={() => {
          chooseDialect(moduleId, to);
          onSwitched?.(to);
        }}
      >
        <Icon name="refresh" />
        Switch this chapter to {dialectLabel(to)}
      </button>
      <span className="muted">Your SQL draft is kept.</span>
    </div>
  );
}
