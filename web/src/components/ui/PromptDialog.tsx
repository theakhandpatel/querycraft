import { useEffect, useRef, useState } from 'react';
import Icon from './Icon';
import { useStore } from '../../state/store';
import type { PromptRequest } from '../../types';

const INPUT_ID = 'prompt-dialog-input';

/** Everything the dialog can put a focus ring on. */
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

/**
 * Whether an element would receive focus from the browser.
 *
 * `offsetParent` is null for anything inside a `position: fixed` panel, so it
 * drops real controls. Client rects plus `visibility` is what the browser uses.
 */
function focusable(el: HTMLElement): boolean {
  return el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
}

function PromptForm({ req, onCancel }: { req: PromptRequest; onCancel: () => void }) {
  const [value, setValue] = useState(req.defaultValue ?? '');
  const inputRef = useRef<HTMLInputElement>(null);
  const formRef = useRef<HTMLFormElement>(null);

  useEffect(() => {
    setValue(req.defaultValue ?? '');
    const input = inputRef.current;
    if (input) {
      input.focus();
      input.select();
    }
  }, [req]);

  useEffect(() => {
    /* The prompt covers the page, so Tab has to cycle inside it. A Tab that
       walks into the shell behind would move focus to a control the learner
       cannot see. Escape and the backdrop are the exits. */
    function onKeyDown(e: KeyboardEvent) {
      if (e.key !== 'Tab') return;
      const root = formRef.current;
      if (!root) return;
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
    /* Capture phase: the trap has to see the key before the form's Escape
       handler and before the browser moves focus on its own. */
    document.addEventListener('keydown', onKeyDown, true);
    return () => document.removeEventListener('keydown', onKeyDown, true);
  }, []);

  const trimmed = value.trim();
  const blocked = req.required !== false && trimmed === '';

  function submit() {
    if (blocked) return;
    req.onSubmit(trimmed);
    onCancel();
  }

  return (
    /* Deliberate deferral: the native `<dialog>` element would give the modal
       semantics for free, but this app renders four modals from one pattern and
       migrating one of them is a redesign, not a fix. */
    <div
      className="modalback open"
      onClick={(e) => { if (e.target === e.currentTarget) onCancel(); }}
      /* The backdrop click is a pointer convenience. Escape and the Close button
         are the keyboard path, and the form below traps Tab. */
    >
      <form
        ref={formRef}
        className="modal"
        role="dialog"
        aria-modal="true"
        aria-label={req.title}
        onSubmit={(e) => { e.preventDefault(); submit(); }}
        onKeyDown={(e) => { if (e.key === 'Escape') { e.preventDefault(); onCancel(); } }}
      >
        <div className="modalhead">
          <div>
            <div className="eyebrow">{req.label}</div>
            <h2>{req.title}</h2>
          </div>
          <button type="button" className="small" aria-label="Close" onClick={onCancel}><Icon name="x" /></button>
        </div>
        <div className="field">
          <label htmlFor={INPUT_ID}>{req.label}</label>
          <input
            id={INPUT_ID}
            ref={inputRef}
            value={value}
            placeholder={req.placeholder}
            onChange={(e) => setValue(e.target.value)}
          />
        </div>
        <div className="modalfoot">
          <button type="button" className="ghost" onClick={onCancel}>Cancel</button>
          <button type="submit" className="primary" disabled={blocked}>{req.submitLabel || 'Save'}</button>
        </div>
      </form>
    </div>
  );
}

export default function PromptDialog() {
  const { promptRequest, closePrompt } = useStore();
  if (!promptRequest) return null;
  return <PromptForm req={promptRequest} onCancel={closePrompt} />;
}
