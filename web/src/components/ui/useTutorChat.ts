import { useCallback, useRef, useState } from 'react';
import { asGatewayError } from '../../api/errors';
import { datasetFor } from '../../lib/dataset';
import { useChapterDialect } from './DialectBadge';
import type { ExecutionApi, TutorRunState, TutorTarget } from '../../types';
import type { AiExerciseBundle, AiTutorContext, AiTutorRequest } from '../../api/aiContract';

/*
 * The session half of the Ask tutor drawer.
 *
 * The gateway is stateless for tutor turns, so the conversation lives here,
 * keyed by exercise id: closing and reopening the drawer in this session keeps
 * the history, and a reload starts empty. `turns` is the conversation the next
 * request carries; the learner's newest question is appended before the call
 * and the tutor's reply after it, so what is on screen is exactly what was
 * sent.
 *
 * Nothing here is persisted and nothing is fabricated: the context summary the
 * drawer renders travels back on the response, and a failed call becomes an
 * error state rather than a made-up reply.
 */

export interface TutorTurn {
  role: 'learner' | 'tutor';
  text: string;
  /** The gateway's `reveals_solution` for this reply. False on a learner turn. */
  revealsSolution?: boolean;
}

export type TutorPhase = 'idle' | 'thinking' | 'error' | 'rate_limited';

export interface TutorChatState {
  turns: TutorTurn[];
  phase: TutorPhase;
  errorCode: string | null;
  errorMessage: string | null;
  /** What the gateway said it showed the tutor, on the last successful turn. */
  context: AiTutorContext | null;
}

const EMPTY: TutorChatState = {
  turns: [],
  phase: 'idle',
  errorCode: null,
  errorMessage: null,
  context: null,
};

export interface TutorChat {
  state: TutorChatState;
  ask: (text: string) => void;
  retry: () => void;
  /** Clear a failed question and its error; earlier turns stay on screen. */
  dismiss: () => void;
  /** Forget the whole conversation for this exercise. */
  reset: () => void;
}

/** A short exercise title for the drawer head. The block carries no title, so
    this is the prompt's first sentence, the same material `eventTitle` uses. */
export function tutorTitle(target: TutorTarget | null): string {
  if (!target) return '';
  const text = String(target.block.prompt ?? '').replace(/\s+/g, ' ').trim();
  if (!text) return '';
  const first = text.split('. ')[0];
  return first.length > 58 ? first.slice(0, 55).trimEnd() + '…' : first;
}

/** The `contracts/exercise.schema.json` bundle the gateway reads. The client
    sends facts; the gateway re-runs the reference itself, so the reference
    here only arms the no-solution guard and computes the oracle. */
function exerciseBundle(target: TutorTarget): AiExerciseBundle {
  const b = target.block;
  return {
    id: b.id,
    version: b.version ?? 1,
    lesson_id: target.module.id,
    title: tutorTitle(target),
    prompt: b.prompt ?? '',
    type: b.type,
    contract: b.contract ?? {
      columns: [],
      column_order: 'exact',
      row_order: 'ignored',
      duplicates: 'preserved',
      numeric_tolerance: 0,
    },
    reference_solution: b.solution ?? '',
  };
}

/** The learner-reported half of the request, used only when the gateway cannot
    execute the SQL itself. A successful run travels as a summary, never rows. */
function runFields(run: TutorRunState): Pick<AiTutorRequest, 'error_code' | 'error_message' | 'result_summary'> {
  if (run.status === 'error') return { error_code: run.code, error_message: run.message };
  if (run.status === 'ran') return { result_summary: `${run.rows} row${run.rows === 1 ? '' : 's'} returned` };
  return {};
}

function objectiveFor(target: TutorTarget): string {
  return String(target.block.prompt || target.block.sql || 'Write a correct query for this exercise.');
}

export function useTutorChat(target: TutorTarget | null, api: ExecutionApi): TutorChat {
  const [chats, setChats] = useState<Record<string, TutorChatState>>({});
  /* The ref is the machine's own memory: `ask` reads the turns it should send
     from it, not from a render that may be a click behind. */
  const chatsRef = useRef(chats);
  const dialect = useChapterDialect(target?.module ?? null, target?.course ?? null);
  const id = target?.block.id ?? '';
  const state = chats[id] ?? EMPTY;

  const write = useCallback((key: string, next: TutorChatState) => {
    chatsRef.current = { ...chatsRef.current, [key]: next };
    setChats(chatsRef.current);
  }, []);

  /* One round trip. The turns sent are the ones already on screen: a new ask
     has appended the learner turn, a retry resends the same list. */
  const send = useCallback(
    async (key: string, turns: TutorTurn[]) => {
      if (!target) return;
      const request: AiTutorRequest = {
        sql: target.sql,
        objective: objectiveFor(target),
        dataset: datasetFor({ course: target.course, block: target.block }),
        dialect,
        turns: turns.map((t) => ({ role: t.role, text: t.text })),
        exercise: exerciseBundle(target),
        ...runFields(target.run),
      };
      try {
        const res = await api.tutor(request);
        const current = chatsRef.current[key] ?? EMPTY;
        write(key, {
          turns: [...current.turns, { role: 'tutor', text: res.message, revealsSolution: res.reveals_solution }],
          phase: 'idle',
          errorCode: null,
          errorMessage: null,
          context: res.context ?? current.context,
        });
      } catch (e) {
        const err = asGatewayError(e);
        const current = chatsRef.current[key] ?? EMPTY;
        write(key, {
          ...current,
          phase: err.code === 'AI_RATE_LIMITED' ? 'rate_limited' : 'error',
          errorCode: err.code,
          errorMessage: err.message,
        });
      }
    },
    [api, dialect, target, write],
  );

  const ask = useCallback(
    (text: string) => {
      if (!target) return;
      const key = target.block.id;
      if (!key) return;
      const question = text.trim();
      if (!question) return;
      const current = chatsRef.current[key] ?? EMPTY;
      const turns = [...current.turns, { role: 'learner' as const, text: question }];
      write(key, { ...current, turns, phase: 'thinking', errorCode: null, errorMessage: null });
      void send(key, turns);
    },
    [send, target, write],
  );

  const retry = useCallback(() => {
    if (!target) return;
    const key = target.block.id;
    if (!key) return;
    const current = chatsRef.current[key] ?? EMPTY;
    if (current.phase !== 'error' && current.phase !== 'rate_limited') return;
    write(key, { ...current, phase: 'thinking', errorCode: null, errorMessage: null });
    void send(key, current.turns);
  }, [send, target, write]);

  const dismiss = useCallback(() => {
    if (!target) return;
    const key = target.block.id;
    if (!key) return;
    const current = chatsRef.current[key] ?? EMPTY;
    /* A failed ask left an unanswered learner turn at the end. Dismiss takes it
       back, so the list does not hold a question the tutor never answered. */
    const last = current.turns[current.turns.length - 1];
    const turns = last && last.role === 'learner' ? current.turns.slice(0, -1) : current.turns;
    write(key, { ...current, turns, phase: 'idle', errorCode: null, errorMessage: null });
  }, [target, write]);

  const reset = useCallback(() => {
    if (!target) return;
    const key = target.block.id;
    if (!key) return;
    write(key, EMPTY);
  }, [target, write]);

  return { state, ask, retry, dismiss, reset };
}
