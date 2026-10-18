import { useCallback, useState } from 'react';

/** One step's state. A step only moves when something reported on it. */
type StepState = 'idle' | 'running' | 'done';

/** The checklist every paced run draws: one state per step, advanced from what
    reported, never on a timer. The steps are passed whole so the state array and
    the list it renders cannot drift apart; `idleWord` is the word the screen
    prints before a step starts, which the pipeline spells "Queued" and the lab
    spells "Idle". */
export function useChecklist<T>(steps: readonly T[], idleWord: string) {
  const [states, setStates] = useState<StepState[]>(() => steps.map(() => 'idle'));

  const mark = useCallback((i: number, state: StepState): void => {
    setStates((prev) => {
      if (prev[i] === state) return prev;
      const next = prev.slice();
      next[i] = state;
      return next;
    });
  }, []);

  const reset = useCallback((): void => {
    setStates(steps.map(() => 'idle'));
  }, [steps]);

  return { states, mark, reset, idleWord };
}
